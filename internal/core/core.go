// Package core holds the appliance logic: connections to Backup Servers and Proxmox VE,
// network shares, USB rotation sets, the monitor and notifications.
package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"pk95proxbackup/internal/hostexec"
	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
	"pk95proxbackup/internal/shares"
	"pk95proxbackup/internal/store"
)

// Object kinds in the store.
const (
	KindPBS      = "pbs"
	KindPVE      = "pve"
	KindShare    = "share"
	KindRotation = "rotation"
)

// PBSServer is a connection to a Proxmox Backup Server.
type PBSServer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Local       bool   `json:"local"` // PBS inside this appliance
	Host        string `json:"host"`
	Port        int    `json:"port"`
	PublicHost  string `json:"public_host"` // address Proxmox VE uses to reach this PBS
	AuthID      string `json:"auth_id"`
	Secret      string `json:"secret,omitempty"`
	Fingerprint string `json:"fingerprint"`
	SSHEnabled  bool   `json:"ssh_enabled"`
	SSHHost     string `json:"ssh_host,omitempty"`
	SSHPort     int    `json:"ssh_port,omitempty"`
	SSHUser     string `json:"ssh_user,omitempty"`
	SSHHostKey  string `json:"ssh_host_key,omitempty"`
}

// Address is how others reach this server.
func (s PBSServer) Address() string {
	if s.PublicHost != "" {
		return s.PublicHost
	}
	return s.Host
}

// PVECluster is a connection to Proxmox VE (a single node or a cluster).
type PVECluster struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	TokenID     string `json:"token_id"`
	Secret      string `json:"secret,omitempty"`
	Fingerprint string `json:"fingerprint"`
}

// Core is the appliance logic shared by the API and the monitor.
type Core struct {
	St      *store.Store
	DataDir string
	Version string
	Signer  ssh.Signer
	// LocalRunner runs commands on this machine (replaced in demo mode).
	LocalRunner hostexec.Runner
	// SelfUUID returns the SMBIOS UUID of this VM (for USB passthrough).
	SelfUUID func() string
	// EnsureStorage returns (and if needed adds) the PVE storage of a datastore (set by the API).
	EnsureStorage func(ctx context.Context, pve *prox.PVE, cl PVECluster, srv PBSServer, store string) (string, error)

	mu      sync.Mutex
	rotBusy map[string]bool
	rotRT   map[string]*diskRuntime
	// sync tasks seen running at the last RunningTasks call
	runningSync map[string]bool
	ov          Overview
	ovAt        time.Time
	ovMu        sync.Mutex
	refresh     chan struct{}
}

// New creates the core.
func New(st *store.Store, dataDir, version string, signer ssh.Signer) *Core {
	return &Core{
		St: st, DataDir: dataDir, Version: version, Signer: signer,
		LocalRunner: hostexec.Local{},
		rotBusy:     map[string]bool{},
		rotRT:       map[string]*diskRuntime{},
		refresh:     make(chan struct{}, 1),
	}
}

// NewID returns a short random id.
func NewID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- connections ----

// Servers lists the Backup Servers.
func (c *Core) Servers() ([]PBSServer, error) { return store.List[PBSServer](c.St, KindPBS) }

// Server loads one Backup Server.
func (c *Core) Server(id string) (PBSServer, error) {
	var s PBSServer
	err := c.St.Get(KindPBS, id, &s)
	return s, err
}

// Clusters lists the Proxmox VE connections.
func (c *Core) Clusters() ([]PVECluster, error) { return store.List[PVECluster](c.St, KindPVE) }

// Cluster loads one Proxmox VE connection.
func (c *Core) Cluster(id string) (PVECluster, error) {
	var s PVECluster
	err := c.St.Get(KindPVE, id, &s)
	return s, err
}

// PBSClient returns an API client for a Backup Server.
func (c *Core) PBSClient(s PBSServer) *prox.PBS {
	port := s.Port
	if port == 0 {
		port = 8007
	}
	return prox.NewPBS(s.Host, port, s.Fingerprint, s.AuthID, s.Secret)
}

// PBS loads a server and returns its client.
func (c *Core) PBS(id string) (*prox.PBS, PBSServer, error) {
	s, err := c.Server(id)
	if err != nil {
		return nil, s, err
	}
	return c.PBSClient(s), s, nil
}

// PVEClient returns an API client for a Proxmox VE connection.
func (c *Core) PVEClient(s PVECluster) *prox.PVE {
	port := s.Port
	if port == 0 {
		port = 8006
	}
	return prox.NewPVE(s.Host, port, s.Fingerprint, s.TokenID, s.Secret)
}

// PVE loads a cluster and returns its client.
func (c *Core) PVE(id string) (*prox.PVE, PVECluster, error) {
	s, err := c.Cluster(id)
	if err != nil {
		return nil, s, err
	}
	return c.PVEClient(s), s, nil
}

// ErrNoShell means a server can only be reached over its API.
var ErrNoShell = errors.New("für diese Funktion braucht die Appliance Zugriff auf die Kommandozeile des Backup-Servers: SSH-Zugang beim Server einrichten")

// Runner returns the command runner for a Backup Server.
func (c *Core) Runner(s PBSServer) (hostexec.Runner, error) {
	if s.Local {
		return c.LocalRunner, nil
	}
	if !s.SSHEnabled {
		return nil, ErrNoShell
	}
	host := s.SSHHost
	if host == "" {
		host = s.Host
	}
	user := s.SSHUser
	if user == "" {
		user = "root"
	}
	id := s.ID
	return &hostexec.SSH{
		Host: host, Port: s.SSHPort, User: user, Signer: c.Signer, HostKey: s.SSHHostKey,
		OnLearn: func(key string) {
			// Trust on first use: remember the host key.
			if cur, err := c.Server(id); err == nil && cur.SSHHostKey == "" {
				cur.SSHHostKey = key
				_ = c.St.Put(KindPBS, id, cur)
			}
		},
	}, nil
}

// ---- shares ----

// Shares lists the network shares.
func (c *Core) Shares() ([]shares.Share, error) { return store.List[shares.Share](c.St, KindShare) }

// ---- events and notifications ----

// NotifySettings returns the notification settings.
func (c *Core) NotifySettings() notify.Settings {
	s := notify.Defaults()
	_ = c.St.GetSetting("notify", &s)
	return s
}

// Event records an event and sends it as notification when configured.
func (c *Core) Event(severity, source, title, body string) {
	c.EventDetails(severity, source, title, body, nil)
}

// EventDetails records an event with structured details (guests, facts, log excerpt).
func (c *Core) EventDetails(severity, source, title, body string, d *notify.Details) {
	ev := store.Event{Severity: severity, Source: source, Title: title, Body: body}
	if d != nil {
		if b, err := json.Marshal(d); err == nil {
			ev.Details = b
		}
	}
	if _, err := c.St.AddEvent(ev); err != nil {
		log.Printf("event: %v", err)
	}
	log.Printf("[%s] %s: %s %s", severity, source, title, strings.ReplaceAll(body, "\n", " "))
	ns := c.NotifySettings()
	if (ns.MailEnabled || ns.HookEnabled) && ns.Wants(severity) {
		msg := c.Message(severity, source, title, body, d)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := notify.Send(ctx, ns, msg); err != nil {
				log.Printf("notify: %v", err)
			}
		}()
	}
}

// Message builds a notification with the appliance's name and address.
func (c *Core) Message(severity, source, title, body string, d *notify.Details) notify.Message {
	host, _ := os.Hostname()
	return notify.Message{Severity: severity, Source: source, Title: title, Body: body, Details: d,
		Host: host, URL: c.PublicURL(), Version: c.Version, Time: time.Now()}
}

// RequestRefresh asks the monitor to rebuild the overview soon.
func (c *Core) RequestRefresh() {
	select {
	case c.refresh <- struct{}{}:
	default:
	}
}

// JobID builds a PBS job id (max 32 chars, [A-Za-z0-9_-]).
func JobID(prefix, name string) string {
	id := prefix + "-" + name
	if len(id) <= 32 {
		return id
	}
	return id[:23] + "-" + NewID(4)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func fmtTime(ts int64) string {
	if ts == 0 {
		return "nie"
	}
	return time.Unix(ts, 0).Format("02.01.2006 15:04")
}

var _ = fmt.Sprintf
