package core

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
)

// State of the Backup Servers themselves: the running PBS version against the installed one
// (services not restarted after an upgrade), a newer kernel than the running one (reboot
// needed), and available updates. Checked every six hours.

const serverHealthEvery = 6 * time.Hour

// ServerHealth is what the appliance knows about the software of a Backup Server.
type ServerHealth struct {
	ServerID      string   `json:"server_id"`
	Name          string   `json:"name"`
	Checked       int64    `json:"checked"`
	Error         string   `json:"error,omitempty"`
	Running       string   `json:"running"`   // "4.2.6"
	Installed     string   `json:"installed"` // "4.2.6-1"
	Available     string   `json:"available"` // "4.2.10-1" when newer than installed
	RestartNeeded bool     `json:"restart_needed"`
	Kernel        string   `json:"kernel"`        // running
	NewestKernel  string   `json:"newest_kernel"` // newest installed
	RebootNeeded  bool     `json:"reboot_needed"`
	Updates       int      `json:"updates"`
	UpdateList    []string `json:"update_list"` // "proxmox-backup-server 4.2.6-1 → 4.2.10-1", important first
	Uptime        int64    `json:"uptime"`
}

var kernelPkgRe = regexp.MustCompile(`^proxmox-kernel-(\d+\.\d+\.\d+-\d+)-pve(-signed)?$`)

// versionLess compares Debian-like versions numerically ("4.2.6-1" < "4.2.10-1").
func versionLess(a, b string) bool {
	num := func(s string) []int {
		var out []int
		for _, f := range regexp.MustCompile(`\d+`).FindAllString(s, -1) {
			n, _ := strconv.Atoi(f)
			out = append(out, n)
		}
		return out
	}
	x, y := num(a), num(b)
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return len(x) < len(y)
}

// EvaluateServerHealth derives the state from the package versions, updates and kernel.
func EvaluateServerHealth(versions, updates []prox.AptPackage, kernel string) ServerHealth {
	var h ServerHealth
	h.Kernel = kernel
	for _, p := range versions {
		installed := p.OldVersion
		if installed == "" {
			installed = p.Version
		}
		switch {
		case p.Package == "proxmox-backup-server":
			h.Installed = installed
			if strings.HasPrefix(p.ExtraInfo, "running version:") {
				h.Running = strings.TrimSpace(strings.TrimPrefix(p.ExtraInfo, "running version:"))
			}
			if p.Version != "" && versionLess(installed, p.Version) {
				h.Available = p.Version
			}
		case kernelPkgRe.MatchString(p.Package):
			if p.OldVersion == "" {
				continue // listed, but not installed
			}
			rel := kernelPkgRe.FindStringSubmatch(p.Package)[1] + "-pve"
			if h.NewestKernel == "" || versionLess(h.NewestKernel, rel) {
				h.NewestKernel = rel
			}
		}
	}
	// "4.2.6" runs, "4.2.6-1" is installed: the same.
	if h.Running != "" && h.Installed != "" && !strings.HasPrefix(h.Installed, h.Running+"-") && h.Installed != h.Running {
		h.RestartNeeded = versionLess(h.Running, h.Installed)
	}
	h.RebootNeeded = h.Kernel != "" && h.NewestKernel != "" && versionLess(h.Kernel, h.NewestKernel)
	h.Updates = len(updates)
	var important, rest []string
	for _, u := range updates {
		line := u.Package + " " + u.OldVersion + " → " + u.Version
		if strings.HasPrefix(u.Package, "proxmox-backup") || strings.HasPrefix(u.Package, "proxmox-kernel") {
			important = append(important, line)
		} else {
			rest = append(rest, line)
		}
	}
	sort.Strings(important)
	sort.Strings(rest)
	h.UpdateList = append(important, rest...)
	if len(h.UpdateList) > 30 {
		h.UpdateList = h.UpdateList[:30]
	}
	if h.UpdateList == nil {
		h.UpdateList = []string{}
	}
	return h
}

// ServerHealths returns the last known state of all Backup Servers.
func (c *Core) ServerHealths() []ServerHealth {
	servers, _ := c.Servers()
	out := []ServerHealth{}
	for _, s := range servers {
		var h ServerHealth
		if c.St.GetSetting("srvhealth:"+s.ID, &h) == nil && h.Checked > 0 {
			h.Name = s.Name
			out = append(out, h)
		}
	}
	return out
}

func (c *Core) scheduleServerHealth(now time.Time) {
	servers, _ := c.Servers()
	for _, s := range servers {
		var h ServerHealth
		_ = c.St.GetSetting("srvhealth:"+s.ID, &h)
		if now.Unix()-h.Checked >= int64(serverHealthEvery.Seconds()) {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				_, _ = c.CheckServerHealth(ctx, s.ID)
			}()
		}
	}
}

// CheckServerHealth reads the state of one Backup Server now.
func (c *Core) CheckServerHealth(ctx context.Context, id string) (ServerHealth, error) {
	pbs, srv, err := c.PBS(id)
	if err != nil {
		return ServerHealth{}, err
	}
	var old ServerHealth
	_ = c.St.GetSetting("srvhealth:"+id, &old)
	h := ServerHealth{ServerID: id, Name: srv.Name, Checked: time.Now().Unix(), UpdateList: []string{}}
	versions, err := pbs.AptVersions(ctx)
	if err != nil {
		h.Error = err.Error()
		_ = c.St.SetSetting("srvhealth:"+id, h)
		return h, err
	}
	updates, _ := pbs.AptUpdates(ctx)
	kernel, _ := pbs.RunningKernel(ctx)
	e := EvaluateServerHealth(versions, updates, kernel)
	e.ServerID, e.Name, e.Checked = id, srv.Name, h.Checked
	if ns, err := pbs.NodeStatus(ctx); err == nil {
		e.Uptime = ns.Uptime
	}
	_ = c.St.SetSetting("srvhealth:"+id, e)
	// Report a new need for a restart or reboot once.
	if e.RestartNeeded && !old.RestartNeeded {
		c.EventDetails("warning", srv.Name, "Dienste laufen noch mit der alten Version",
			"Installiert ist Proxmox Backup Server "+e.Installed+", die Dienste laufen aber noch mit "+e.Running+". Unter Verbindungen „Dienste neu laden“ wählen.",
			&notify.Details{Status: "warning", Link: "/verbindungen"})
	}
	if e.RebootNeeded && !old.RebootNeeded {
		c.EventDetails("warning", srv.Name, "Neustart nötig: neuer Kernel installiert",
			"Installiert ist der Kernel "+e.NewestKernel+", es läuft noch "+e.Kernel+". Bitte den Backup-Server bei Gelegenheit neu starten (nicht während einer Sicherung).",
			&notify.Details{Status: "warning", Link: "/verbindungen"})
	}
	return e, nil
}

// ReloadServerServices reloads the PBS services gracefully (new version after an upgrade,
// handles of pulled disks let go). Running tasks finish in the old processes.
func (c *Core) ReloadServerServices(ctx context.Context, id string) (ServerHealth, error) {
	pbs, srv, err := c.PBS(id)
	if err != nil {
		return ServerHealth{}, err
	}
	for _, svc := range []string{"proxmox-backup", "proxmox-backup-proxy"} {
		if err := pbs.ReloadService(ctx, svc); err != nil {
			return ServerHealth{}, err
		}
	}
	c.Event("info", srv.Name, "Dienste neu geladen", "proxmox-backup und proxmox-backup-proxy wurden neu geladen; laufende Aufgaben arbeiten im alten Prozess zu Ende.")
	time.Sleep(3 * time.Second)
	return c.CheckServerHealth(ctx, id)
}
