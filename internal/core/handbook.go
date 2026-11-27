package core

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/prox"
)

// Emergency handbook: one printable document with everything needed when the systems are
// gone – where each guest lies, servers and addresses, disks and swap plan, keys (fingerprints
// only), accounts (no passwords) and the steps for each kind of failure. The appliance keeps a
// hash per section of what was printed and reports when the paper no longer matches.

const (
	handbookSetting = "handbook"
	handbookState   = "handbook-state"
	handbookMaxAge  = 180 * 24 * time.Hour // print again at least twice a year
	handbookCheck   = 6 * time.Hour
)

// HandbookSettings are the texts the user writes and what was printed.
type HandbookSettings struct {
	Contacts      string            `json:"contacts"` // who to call
	Order         string            `json:"order"`    // what to restore first
	Notes         string            `json:"notes"`    // where things are kept (envelope, safe …)
	PrintedAt     int64             `json:"printed_at"`
	PrintedBy     string            `json:"printed_by,omitempty"`
	PrintedHashes map[string]string `json:"printed_hashes,omitempty"`
}

// HandbookStatus is how current the printed handbook is.
type HandbookStatus struct {
	State     string   `json:"state"` // never, current, changed, old
	PrintedAt int64    `json:"printed_at"`
	PrintedBy string   `json:"printed_by,omitempty"`
	Changed   []string `json:"changed"` // section titles that differ from the print
	CheckedAt int64    `json:"checked_at"`
}

type handbookStateData struct {
	Hashes    map[string]string `json:"hashes"`
	CheckedAt int64             `json:"checked_at"`
}

// Handbook is the content of the document.
type Handbook struct {
	Built      int64             `json:"built"`
	Host       string            `json:"host"`
	Version    string            `json:"version"`
	IPs        []string          `json:"ips"`
	Settings   HandbookSettings  `json:"settings"`
	Servers    []HBServer        `json:"servers"`
	Clusters   []HBCluster       `json:"clusters"`
	Guests     []HBGuest         `json:"guests"`
	Rotations  []HBRotation      `json:"rotations"`
	Archives   []HBArchive       `json:"archives"`
	Keys       []HBKey           `json:"keys"`
	Accounts   []HBAccount       `json:"accounts"`
	Shares     []HBShare         `json:"shares"`
	SelfBackup SelfBackupInfo    `json:"self_backup"`
	Problems   []string          `json:"problems"` // what could not be read
	Hashes     map[string]string `json:"hashes"`
	Status     HandbookStatus    `json:"status"`
	Titles     map[string]string `json:"titles"`
	extra      map[string][]string
}

// HBServer is a Backup Server with its datastores.
type HBServer struct {
	Name        string    `json:"name"`
	Address     string    `json:"address"`
	Port        int       `json:"port"`
	Fingerprint string    `json:"fingerprint"`
	Local       bool      `json:"local"`
	SSH         bool      `json:"ssh"`
	Stores      []HBStore `json:"stores"`
}

// HBStore is a datastore.
type HBStore struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"` // Platte, USB-Wechselplatte, Cloud (S3), Freigabe
	Where string `json:"where"`
	Used  int64  `json:"used"`
	Total int64  `json:"total"`
}

// HBCluster is a Proxmox VE connection with its nodes and backup storages.
type HBCluster struct {
	Name     string      `json:"name"`
	Address  string      `json:"address"`
	Nodes    []HBNode    `json:"nodes"`
	Storages []HBStorage `json:"storages"`
}

// HBNode is a node with its host backup.
type HBNode struct {
	Name       string `json:"name"`
	IP         string `json:"ip"`
	HostBackup int64  `json:"host_backup"` // last good host backup (0 = none)
	HostOn     bool   `json:"host_on"`
}

// HBStorage is a PBS storage of Proxmox VE.
type HBStorage struct {
	Name      string `json:"name"`
	Target    string `json:"target"`
	Encrypted bool   `json:"encrypted"`
	KeyName   string `json:"key_name,omitempty"`
	Guard     string `json:"guard"` // protected, open, …
}

// HBGuest is where a guest is backed up.
type HBGuest struct {
	ID       int      `json:"id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"` // VM, CT
	Node     string   `json:"node"`
	Cluster  string   `json:"cluster"`
	Fixed    []string `json:"fixed"` // "server / store"
	USB      []string `json:"usb"`   // disk labels
	Archive  []string `json:"archive"`
	Last     int64    `json:"last"`
	LastTest int64    `json:"last_test"`
	InJob    bool     `json:"in_job"`
}

// HBRotation is a set of rotation disks.
type HBRotation struct {
	Name   string   `json:"name"`
	Server string   `json:"server"`
	Rhythm string   `json:"rhythm"`
	Disks  []HBDisk `json:"disks"`
}

// HBDisk is a disk of a set.
type HBDisk struct {
	Label string `json:"label"`
	Store string `json:"store"`
	Plan  string `json:"plan"`
}

// HBArchive is an archive disk.
type HBArchive struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	Store    string `json:"store"`
	Created  int64  `json:"created"`
	Servers  string `json:"servers"`
}

// HBKey is an encryption key (fingerprint only).
type HBKey struct {
	Name        string   `json:"name"`
	Fingerprint string   `json:"fingerprint"`
	Saved       int64    `json:"saved"`
	Storages    []string `json:"storages"`
}

// HBAccount is an account of the appliance.
type HBAccount struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	TOTP     bool   `json:"totp"`
}

// HBShare is an NFS/SMB share.
type HBShare struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Source string `json:"source"`
	Server string `json:"server"`
	Store  string `json:"store"`
}

// handbookTitles names the sections (also in "changed since printed").
var handbookTitles = map[string]string{
	"contacts": "Ansprechpartner und Hinweise", "appliance": "Appliance", "servers": "Backup-Server und Datastores",
	"clusters": "Proxmox VE", "guests": "Wo liegt welcher Server", "usb": "Wechsel- und Archiv-Platten",
	"keys": "Schlüssel", "accounts": "Konten", "shares": "Freigaben",
}

// HandbookSettingsOf returns the stored texts.
func (c *Core) HandbookSettingsOf() HandbookSettings {
	var s HandbookSettings
	_ = c.St.GetSetting(handbookSetting, &s)
	return s
}

var handbookMu sync.Mutex

// SetHandbookTexts stores contacts, order and notes.
func (c *Core) SetHandbookTexts(contacts, order, notes string) error {
	handbookMu.Lock()
	defer handbookMu.Unlock()
	s := c.HandbookSettingsOf()
	for _, t := range []string{contacts, order, notes} {
		if len(t) > 4000 {
			return fmt.Errorf("höchstens 4000 Zeichen je Feld")
		}
	}
	s.Contacts, s.Order, s.Notes = strings.TrimSpace(contacts), strings.TrimSpace(order), strings.TrimSpace(notes)
	return c.St.SetSetting(handbookSetting, s)
}

// MarkHandbookPrinted remembers the content of the printed handbook.
func (c *Core) MarkHandbookPrinted(ctx context.Context, by string) (HandbookStatus, error) {
	h := c.BuildHandbook(ctx)
	handbookMu.Lock()
	s := c.HandbookSettingsOf()
	s.PrintedAt, s.PrintedBy, s.PrintedHashes = time.Now().Unix(), by, h.Hashes
	err := c.St.SetSetting(handbookSetting, s)
	handbookMu.Unlock()
	if err != nil {
		return HandbookStatus{}, err
	}
	c.Event("info", "Appliance", "Notfallhandbuch ausgedruckt", "Stand "+time.Now().Format("02.01.2006")+", vermerkt von "+by+".")
	return c.HandbookStatusInfo(), nil
}

// HandbookStatusInfo compares the last check with what was printed (no requests).
func (c *Core) HandbookStatusInfo() HandbookStatus {
	s := c.HandbookSettingsOf()
	var st handbookStateData
	_ = c.St.GetSetting(handbookState, &st)
	return handbookStatus(s, st.Hashes, st.CheckedAt, time.Now())
}

func handbookStatus(s HandbookSettings, now map[string]string, checked int64, t time.Time) HandbookStatus {
	out := HandbookStatus{State: "current", PrintedAt: s.PrintedAt, PrintedBy: s.PrintedBy, Changed: []string{}, CheckedAt: checked}
	if s.PrintedAt == 0 {
		out.State = "never"
		return out
	}
	keys := make([]string, 0, len(now))
	for k := range now {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return sectionOrder(keys[i]) < sectionOrder(keys[j]) })
	for _, k := range keys {
		if s.PrintedHashes[k] != now[k] {
			out.Changed = append(out.Changed, handbookTitles[k])
		}
	}
	switch {
	case len(out.Changed) > 0:
		out.State = "changed"
	case t.Sub(time.Unix(s.PrintedAt, 0)) > handbookMaxAge:
		out.State = "old"
	}
	return out
}

func sectionOrder(k string) int {
	for i, x := range []string{"contacts", "appliance", "servers", "clusters", "guests", "usb", "keys", "accounts", "shares"} {
		if x == k {
			return i
		}
	}
	return 99
}

// scheduleHandbook checks every six hours whether the printed handbook is still current.
func (c *Core) scheduleHandbook(now time.Time) {
	var st handbookStateData
	_ = c.St.GetSetting(handbookState, &st)
	if now.Sub(time.Unix(st.CheckedAt, 0)) < handbookCheck {
		return
	}
	st.CheckedAt = now.Unix() // no second run while this one reads
	_ = c.St.SetSetting(handbookState, st)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		before := c.HandbookStatusInfo()
		c.BuildHandbook(ctx)
		after := c.HandbookStatusInfo()
		if after.State == "changed" && before.State != "changed" {
			c.Event("warning", "Appliance", "Notfallhandbuch veraltet", "Seit dem Ausdruck vom "+time.Unix(after.PrintedAt, 0).Format("02.01.2006")+
				" hat sich geändert: "+strings.Join(after.Changed, ", ")+". Bitte neu ausdrucken (Verwaltung → Notfallhandbuch).")
		}
	}()
}

// BuildHandbook reads everything the handbook shows and stores the section hashes.
func (c *Core) BuildHandbook(ctx context.Context) Handbook {
	host, _ := os.Hostname()
	h := Handbook{Built: time.Now().Unix(), Host: host, Version: c.Version, IPs: LocalIPs(), Settings: c.HandbookSettingsOf(),
		Servers: []HBServer{}, Clusters: []HBCluster{}, Guests: []HBGuest{}, Rotations: []HBRotation{}, Archives: []HBArchive{},
		Keys: []HBKey{}, Accounts: []HBAccount{}, Shares: []HBShare{}, Problems: []string{}, Titles: handbookTitles}
	if h.IPs == nil {
		h.IPs = []string{}
	}
	h.SelfBackup = c.SelfBackup()
	servers, _ := c.Servers()
	srvName := map[string]string{}
	for _, s := range servers {
		srvName[s.ID] = s.Name
	}
	shareOf := map[string]string{} // server|store → share source
	shs, _ := c.Shares()
	for _, sh := range shs {
		shareOf[sh.ServerID+"|"+sh.Store] = sh.Source
		h.Shares = append(h.Shares, HBShare{Name: sh.Name, Type: map[string]string{"nfs": "NFS", "cifs": "SMB"}[sh.Type], Source: sh.Source, Server: srvName[sh.ServerID], Store: sh.Store})
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	h.Servers = make([]HBServer, len(servers))
	for i, s := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			port := s.Port
			if port == 0 {
				port = 8007
			}
			hs := HBServer{Name: s.Name, Address: s.Address(), Port: port, Fingerprint: s.Fingerprint, Local: s.Local, SSH: s.Local || s.SSHEnabled, Stores: []HBStore{}}
			pbs := c.PBSClient(s)
			cfgs, err := pbs.Datastores(ctx)
			if err != nil {
				mu.Lock()
				h.Problems = append(h.Problems, s.Name+": "+err.Error())
				mu.Unlock()
			}
			use := map[string]prox.DatastoreUsage{}
			if us, err := pbs.Usage(ctx); err == nil {
				for _, u := range us {
					use[u.Store] = u
				}
			}
			for _, d := range cfgs {
				st := HBStore{Name: d.Name, Kind: "Platte", Where: d.Path, Used: use[d.Name].Used, Total: use[d.Name].Total}
				switch {
				case d.Backend != "":
					st.Kind, st.Where = "Cloud (S3)", strings.TrimPrefix(d.Backend, "type=s3,")
				case d.BackingDevice != "":
					st.Kind, st.Where = "USB-Platte", "Gerät "+d.BackingDevice
				case shareOf[s.ID+"|"+d.Name] != "":
					st.Kind, st.Where = "Freigabe", shareOf[s.ID+"|"+d.Name]
				}
				hs.Stores = append(hs.Stores, st)
			}
			sort.Slice(hs.Stores, func(a, b int) bool { return hs.Stores[a].Name < hs.Stores[b].Name })
			h.Servers[i] = hs
		}()
	}

	keys := c.EncKeys()
	keyName := map[string]string{}
	keyStores := map[string][]string{}
	for _, k := range keys {
		keyName[prox.NormalizeFingerprint(k.Fingerprint)] = k.Name
	}
	clusters, _ := c.Clusters()
	hostConfs, hostStates := c.hostConfs(), c.hostStates()
	h.Clusters = make([]HBCluster, len(clusters))
	for i, cl := range clusters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hc := HBCluster{Name: cl.Name, Address: cl.Host, Nodes: []HBNode{}, Storages: []HBStorage{}}
			pve := c.PVEClient(cl)
			addrs, _, names, err := hostAddrs(ctx, pve, cl)
			if err != nil {
				mu.Lock()
				h.Problems = append(h.Problems, cl.Name+": "+err.Error())
				mu.Unlock()
			}
			for _, n := range names {
				hc.Nodes = append(hc.Nodes, HBNode{Name: n, IP: addrs[n], HostOn: hostConfs[cl.ID].Enabled, HostBackup: hostStates[cl.ID][n].LastOK})
			}
			if storages, err := pve.Storages(ctx); err == nil {
				for _, st := range storages {
					if st.Type != "pbs" {
						continue
					}
					target := st.Server + " / " + st.Datastore
					if srv, ok := c.MatchServer(st); ok {
						target = srv.Name + " / " + st.Datastore
					}
					if st.Namespace != "" {
						target += " / " + st.Namespace
					}
					hs := HBStorage{Name: st.Storage, Target: target, Encrypted: st.EncryptionKey != ""}
					if hs.Encrypted {
						hs.KeyName = keyName[prox.NormalizeFingerprint(st.EncryptionKey)]
						mu.Lock()
						fp := prox.NormalizeFingerprint(st.EncryptionKey)
						keyStores[fp] = append(keyStores[fp], cl.Name+": "+st.Storage)
						mu.Unlock()
					}
					hc.Storages = append(hc.Storages, hs)
				}
			}
			h.Clusters[i] = hc
		}()
	}
	var guests []GuestCoverage
	wg.Add(1)
	go func() {
		defer wg.Done()
		guests, _ = c.Coverage(ctx)
	}()
	var guards []StorageGuard
	wg.Add(1)
	go func() {
		defer wg.Done()
		guards, _ = c.StorageGuards(ctx)
	}()
	wg.Wait()

	for ci := range h.Clusters {
		for si := range h.Clusters[ci].Storages {
			for _, g := range guards {
				if g.ClusterName == h.Clusters[ci].Name && g.Storage == h.Clusters[ci].Storages[si].Name {
					h.Clusters[ci].Storages[si].Guard = g.State
				}
			}
		}
	}
	for _, g := range guests {
		if g.Template == 1 {
			continue
		}
		hg := HBGuest{ID: g.VMID, Name: g.Name, Type: map[bool]string{true: "CT", false: "VM"}[g.Type == "lxc"], Node: g.Node, Cluster: g.ClusterName,
			Fixed: []string{}, USB: []string{}, Archive: []string{}, Last: g.LastBackup, LastTest: g.Chain.Test.Last, InJob: g.InJob}
		add := func(list *[]string, v string) {
			for _, x := range *list {
				if x == v {
					return
				}
			}
			*list = append(*list, v)
		}
		for _, b := range append(append([]BackupRef{}, g.Backups...), g.Seen...) {
			switch b.Kind {
			case "usb":
				add(&hg.USB, cmp.Or(b.Label, b.Store))
			case "archive":
				add(&hg.Archive, cmp.Or(b.Label, b.Store))
			default:
				add(&hg.Fixed, b.ServerName+" / "+b.Store)
			}
		}
		sort.Strings(hg.Fixed)
		sort.Strings(hg.USB)
		h.Guests = append(h.Guests, hg)
	}
	sort.Slice(h.Guests, func(i, j int) bool { return h.Guests[i].ID < h.Guests[j].ID })

	rots, _ := c.Rotations()
	for _, r := range rots {
		hr := HBRotation{Name: r.Name, Server: srvName[r.ServerID], Rhythm: map[string]string{"weekly": "wöchentlich tauschen", "daily": "täglich tauschen"}[r.Rhythm], Disks: []HBDisk{}}
		if hr.Rhythm == "" {
			hr.Rhythm = "täglich tauschen"
		}
		for _, d := range r.Disks {
			hr.Disks = append(hr.Disks, HBDisk{Label: d.Label, Store: d.Store, Plan: d.PlanText()})
		}
		h.Rotations = append(h.Rotations, hr)
	}
	sort.Slice(h.Rotations, func(i, j int) bool { return h.Rotations[i].Name < h.Rotations[j].Name })
	archs, _ := c.Archives()
	for _, a := range archs {
		what := "alle Server"
		if len(a.Groups) > 0 {
			what = strings.Join(a.Groups, ", ")
		}
		if a.Latest {
			what += ", neuester Stand"
		}
		h.Archives = append(h.Archives, HBArchive{Name: a.Name, Location: a.Location, Store: a.Store, Created: a.Created, Servers: what})
	}
	sort.Slice(h.Archives, func(i, j int) bool { return h.Archives[i].Created < h.Archives[j].Created })
	for _, k := range keys {
		st := keyStores[prox.NormalizeFingerprint(k.Fingerprint)]
		if st == nil {
			st = []string{}
		}
		sort.Strings(st)
		h.Keys = append(h.Keys, HBKey{Name: k.Name, Fingerprint: k.Fingerprint, Saved: k.Saved, Storages: st})
	}
	if users, err := c.St.Users(); err == nil {
		for _, u := range users {
			h.Accounts = append(h.Accounts, HBAccount{Username: u.Username, Role: u.Role, TOTP: u.TOTPRequired})
		}
	}
	sort.Slice(h.Accounts, func(i, j int) bool { return h.Accounts[i].Username < h.Accounts[j].Username })

	h.Hashes = h.sectionHashes()
	if len(h.Problems) == 0 { // a server that is down must not look like a change
		_ = c.St.SetSetting(handbookState, handbookStateData{Hashes: h.Hashes, CheckedAt: time.Now().Unix()})
	}
	var st handbookStateData
	_ = c.St.GetSetting(handbookState, &st)
	h.Status = handbookStatus(h.Settings, h.Hashes, st.CheckedAt, time.Now())
	return h
}

// sectionHashes hash what belongs on paper: names, addresses, places and plans – not sizes
// or times that change every day.
func (h Handbook) sectionHashes() map[string]string {
	sum := func(v any) string {
		b, _ := json.Marshal(v)
		s := sha256.Sum256(b)
		return hex.EncodeToString(s[:8])
	}
	type srv struct {
		Name, Address, Fingerprint string
		Port                       int
		Stores                     []string
	}
	var servers []srv
	for _, s := range h.Servers {
		x := srv{Name: s.Name, Address: s.Address, Fingerprint: s.Fingerprint, Port: s.Port}
		for _, st := range s.Stores {
			x.Stores = append(x.Stores, st.Name+"|"+st.Kind+"|"+st.Where)
		}
		servers = append(servers, x)
	}
	type cl struct {
		Name, Address string
		Nodes         []string
		Storages      []string
	}
	var clusters []cl
	for _, c := range h.Clusters {
		x := cl{Name: c.Name, Address: c.Address}
		for _, n := range c.Nodes {
			x.Nodes = append(x.Nodes, fmt.Sprintf("%s|%s|%v", n.Name, n.IP, n.HostOn))
		}
		for _, s := range c.Storages {
			x.Storages = append(x.Storages, fmt.Sprintf("%s|%s|%v|%s", s.Name, s.Target, s.Encrypted, s.Guard))
		}
		clusters = append(clusters, x)
	}
	var guests []string
	for _, g := range h.Guests {
		guests = append(guests, fmt.Sprintf("%d|%s|%s|%v|%v|%v", g.ID, g.Name, g.Type, g.Fixed, g.USB, g.Archive))
	}
	var keys []string
	for _, k := range h.Keys {
		keys = append(keys, k.Name+"|"+k.Fingerprint+"|"+strings.Join(k.Storages, ","))
	}
	return map[string]string{
		"contacts":  sum([]string{h.Settings.Contacts, h.Settings.Order, h.Settings.Notes}),
		"appliance": sum([]any{h.Host, h.IPs, h.SelfBackup.Enabled, h.SelfBackup.Targets}),
		"servers":   sum(servers),
		"clusters":  sum(clusters),
		"guests":    sum(guests),
		"usb":       sum([]any{h.Rotations, h.Archives}),
		"keys":      sum(keys),
		"accounts":  sum(h.Accounts),
		"shares":    sum(h.Shares),
	}
}
