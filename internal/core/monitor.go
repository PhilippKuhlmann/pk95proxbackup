package core

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
	"pk95proxbackup/internal/shares"
)

// ServerState is the shown state of a Backup Server.
type ServerState struct {
	PBSServer
	Online     bool                  `json:"online"`
	Error      string                `json:"error,omitempty"`
	Version    string                `json:"version,omitempty"`
	CPU        float64               `json:"cpu"`
	MemTotal   int64                 `json:"mem_total"`
	MemUsed    int64                 `json:"mem_used"`
	Uptime     int64                 `json:"uptime"`
	Datastores []prox.DatastoreUsage `json:"datastores"`
	Shell      bool                  `json:"shell"`
}

// ClusterState is the shown state of a Proxmox VE connection.
type ClusterState struct {
	PVECluster
	Online      bool   `json:"online"`
	Error       string `json:"error,omitempty"`
	Version     string `json:"version,omitempty"`
	Nodes       int    `json:"nodes"`
	Guests      int    `json:"guests"`
	NotBackedUp int    `json:"not_backed_up"`
	Jobs        int    `json:"jobs"`
	// Missing are privileges the API token lacks (it then sees no guests or jobs).
	Missing []string `json:"missing,omitempty"`
}

// ShareState is the shown state of a share.
type ShareState struct {
	shares.Share
	shares.State
	ServerName string `json:"server_name"`
}

// TaskInfo is a task of a PBS or PVE.
type TaskInfo struct {
	Source    string `json:"source"` // pbs:<id> / pve:<id>
	Server    string `json:"server"`
	UPID      string `json:"upid"`
	Node      string `json:"node"`
	Type      string `json:"type"`
	ID        string `json:"id"`
	User      string `json:"user"`
	StartTime int64  `json:"starttime"`
	EndTime   int64  `json:"endtime"`
	Status    string `json:"status"`
}

// Overview is the dashboard snapshot built by the monitor.
type Overview struct {
	Built     int64           `json:"built"`
	Servers   []ServerState   `json:"servers"`
	Clusters  []ClusterState  `json:"clusters"`
	Shares    []ShareState    `json:"shares"`
	Rotations []RotationState `json:"rotations"`
	Tasks     []TaskInfo      `json:"tasks"`
}

// CachedOverview returns the last overview (building one if there is none).
func (c *Core) CachedOverview(ctx context.Context) Overview {
	c.ovMu.Lock()
	ov, at := c.ov, c.ovAt
	c.ovMu.Unlock()
	if at.IsZero() || time.Since(at) > 2*time.Minute {
		return c.BuildOverview(ctx)
	}
	return ov
}

// BuildOverview queries all servers.
func (c *Core) BuildOverview(ctx context.Context) Overview {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	ov := Overview{Built: time.Now().Unix(), Servers: []ServerState{}, Clusters: []ClusterState{}, Shares: []ShareState{}, Tasks: []TaskInfo{}}
	servers, _ := c.Servers()
	clusters, _ := c.Clusters()
	var wg sync.WaitGroup
	var mu sync.Mutex
	ov.Servers = make([]ServerState, len(servers))
	for i, s := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, tasks := c.serverState(ctx, s)
			mu.Lock()
			ov.Servers[i] = st
			ov.Tasks = append(ov.Tasks, tasks...)
			mu.Unlock()
		}()
	}
	ov.Clusters = make([]ClusterState, len(clusters))
	for i, cl := range clusters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, tasks := c.clusterState(ctx, cl)
			mu.Lock()
			ov.Clusters[i] = st
			ov.Tasks = append(ov.Tasks, tasks...)
			mu.Unlock()
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ss := c.ShareStates(ctx)
		mu.Lock()
		ov.Shares = ss
		mu.Unlock()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		rs := c.RotationStates(ctx)
		mu.Lock()
		ov.Rotations = rs
		mu.Unlock()
	}()
	wg.Wait()
	for i := range ov.Servers {
		ov.Servers[i].Secret = ""
	}
	for i := range ov.Clusters {
		ov.Clusters[i].Secret = ""
	}
	sort.Slice(ov.Tasks, func(i, j int) bool { return ov.Tasks[i].StartTime > ov.Tasks[j].StartTime })
	c.ovMu.Lock()
	c.ov, c.ovAt = ov, time.Now()
	c.ovMu.Unlock()
	return ov
}

func (c *Core) serverState(ctx context.Context, s PBSServer) (ServerState, []TaskInfo) {
	st := ServerState{PBSServer: s, Datastores: []prox.DatastoreUsage{}, Shell: s.Local || s.SSHEnabled}
	pbs := c.PBSClient(s)
	v, err := pbs.Version(ctx)
	if err != nil {
		st.Error = err.Error()
		return st, nil
	}
	st.Online = true
	st.Version = v.Version + "-" + v.Release
	if ns, err := pbs.NodeStatus(ctx); err == nil {
		st.CPU, st.MemTotal, st.MemUsed, st.Uptime = ns.CPU, ns.Memory.Total, ns.Memory.Used, ns.Uptime
	}
	if us, err := pbs.Usage(ctx); err == nil {
		st.Datastores = us
	}
	var tasks []TaskInfo
	if ts, err := pbs.Tasks(ctx, 60, false); err == nil {
		for _, t := range ts {
			tasks = append(tasks, TaskInfo{Source: "pbs:" + s.ID, Server: s.Name, UPID: t.UPID, Node: t.Node, Type: t.Type, ID: t.WorkerID,
				User: t.User, StartTime: t.StartTime, EndTime: t.EndTime, Status: t.Status})
		}
	}
	return st, tasks
}

func (c *Core) clusterState(ctx context.Context, cl PVECluster) (ClusterState, []TaskInfo) {
	st := ClusterState{PVECluster: cl}
	pve := c.PVEClient(cl)
	v, err := pve.Version(ctx)
	if err != nil {
		st.Error = err.Error()
		return st, nil
	}
	st.Online = true
	st.Version = v.Version
	if perms, err := pve.Permissions(ctx); err == nil {
		st.Missing = prox.MissingPrivs(perms)
	}
	if n, err := pve.Nodes(ctx); err == nil {
		st.Nodes = len(n)
	}
	if g, err := pve.Guests(ctx); err == nil {
		for _, x := range g {
			if x.Template == 0 {
				st.Guests++
			}
		}
	}
	if g, err := pve.NotBackedUp(ctx); err == nil {
		st.NotBackedUp = len(g)
	}
	if j, err := pve.BackupJobs(ctx); err == nil {
		st.Jobs = len(j)
	}
	var tasks []TaskInfo
	if ts, err := pve.Tasks(ctx); err == nil {
		for _, t := range ts {
			if t.Type != "vzdump" && t.Type != "qmrestore" && t.Type != "vzrestore" {
				continue
			}
			tasks = append(tasks, TaskInfo{Source: "pve:" + cl.ID, Server: cl.Name, UPID: t.UPID, Node: t.Node, Type: t.Type, ID: t.ID,
				User: t.User, StartTime: t.StartTime, EndTime: t.EndTime, Status: t.Status})
		}
	}
	return st, tasks
}

// ShareStates reads the mount state of all shares.
func (c *Core) ShareStates(ctx context.Context) []ShareState {
	list, _ := c.Shares()
	out := make([]ShareState, len(list))
	var wg sync.WaitGroup
	for i, s := range list {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ss := ShareState{Share: s}
			ss.Password = ""
			srv, err := c.Server(s.ServerID)
			if err != nil {
				ss.State.Error = "Backup-Server gelöscht"
				out[i] = ss
				return
			}
			ss.ServerName = srv.Name
			r, err := c.Runner(srv)
			if err != nil {
				ss.State.Error = err.Error()
			} else {
				ss.State = shares.Status(ctx, r, &s)
			}
			out[i] = ss
		}()
	}
	wg.Wait()
	return out
}

// Run is the monitor loop: overview every minute (or on request), checks and schedules.
func (c *Core) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	var prev *Overview
	localTry := time.Time{}
	go c.afterStart(ctx)
	for {
		if time.Since(localTry) > 2*time.Minute {
			localTry = time.Now()
			if err := c.EnsureLocalServer(ctx); err != nil {
				log.Printf("local pbs: %v", err)
			}
		}
		ov := c.BuildOverview(ctx)
		c.check(ctx, prev, &ov)
		c.RecordUsage(&ov)
		c.reportBackups(ctx, &ov)
		c.scheduleRotations(ctx, ov.Rotations, time.Now())
		c.checkArchivesUnplugged(ctx, time.Now())
		c.scheduleRestoreTests(time.Now())
		c.scheduleDiskHealth(time.Now())
		c.scheduleServerHealth(time.Now())
		c.scheduleMonthly(time.Now())
		c.enforcePVEMail(time.Now())
		c.scheduleUpdateCheck(time.Now())
		c.scheduleSelfBackup(time.Now())
		c.scheduleAutoProtect(time.Now())
		c.scheduleDeletionWatch(time.Now())
		c.scheduleContentScan(time.Now())
		prev = &ov
		_ = c.St.PruneSessions()
		_ = c.St.PruneEvents(180 * 24 * time.Hour)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-c.refresh:
			// short pause so a burst of requests causes one rebuild
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}
}

// check compares two overviews and raises events for changes.
func (c *Core) check(ctx context.Context, prev, cur *Overview) {
	if prev == nil {
		c.checkFailedTasks(ctx, cur)
		return
	}
	was := map[string]ServerState{}
	for _, s := range prev.Servers {
		was[s.ID] = s
	}
	for _, s := range cur.Servers {
		p, ok := was[s.ID]
		if !ok {
			continue
		}
		if p.Online && !s.Online {
			c.Event("critical", s.Name, "Backup-Server nicht erreichbar", s.Error)
		} else if !p.Online && s.Online {
			c.Event("info", s.Name, "Backup-Server wieder erreichbar", "")
		}
		pu := map[string]prox.DatastoreUsage{}
		for _, d := range p.Datastores {
			pu[d.Store] = d
		}
		for _, d := range s.Datastores {
			if d.Total == 0 {
				continue
			}
			pct, ppct := pctOf(d), pctOf(pu[d.Store])
			if pct >= 90 && ppct < 90 {
				c.Event("critical", s.Name, "Datastore "+d.Store+" fast voll", fmt.Sprintf("%d %% belegt.", pct))
			} else if pct >= 80 && ppct < 80 {
				c.Event("warning", s.Name, "Datastore "+d.Store+" zu 80 % belegt", fmt.Sprintf("%d %% belegt.", pct))
			}
		}
	}
	wasPVE := map[string]ClusterState{}
	for _, s := range prev.Clusters {
		wasPVE[s.ID] = s
	}
	for _, s := range cur.Clusters {
		if p, ok := wasPVE[s.ID]; ok {
			if p.Online && !s.Online {
				c.Event("critical", s.Name, "Proxmox VE nicht erreichbar", s.Error)
			} else if !p.Online && s.Online {
				c.Event("info", s.Name, "Proxmox VE wieder erreichbar", "")
			}
		}
	}
	wasShare := map[string]ShareState{}
	for _, s := range prev.Shares {
		wasShare[s.ID] = s
	}
	for _, s := range cur.Shares {
		if p, ok := wasShare[s.ID]; ok && p.Mounted && !s.Mounted {
			c.Event("critical", "Freigaben", "Freigabe "+s.Name+" ist nicht mehr eingehängt", s.Source+" "+s.State.Error)
			if srv, err := c.Server(s.ServerID); err == nil {
				if r, err := c.Runner(srv); err == nil {
					sh := s.Share
					go func() { _ = shares.Mount(context.Background(), r, &sh, true) }()
				}
			}
		} else if ok && !p.Mounted && s.Mounted {
			c.Event("info", "Freigaben", "Freigabe "+s.Name+" wieder eingehängt", s.Source)
		}
	}
	c.checkFailedTasks(ctx, cur)
}

func pctOf(d prox.DatastoreUsage) int {
	if d.Total == 0 {
		return 0
	}
	return int(d.Used * 100 / d.Total)
}

// checkFailedTasks reports tasks that ended with an error, with an excerpt of their log.
// Backups of Proxmox VE are reported by reportBackups (with the list of guests).
func (c *Core) checkFailedTasks(ctx context.Context, cur *Overview) {
	var seen map[string]bool
	_ = c.St.GetSetting("seen-failed", &seen)
	if seen == nil {
		seen = map[string]bool{}
	}
	first := len(seen) == 0
	changed := false
	keep := map[string]bool{}
	for _, t := range cur.Tasks {
		if t.EndTime == 0 || t.Status == "" || t.Status == "OK" || strings.HasPrefix(t.Status, "WARNINGS") {
			continue
		}
		keep[t.UPID] = true
		if seen[t.UPID] {
			continue
		}
		changed = true
		if first || time.Since(time.Unix(t.EndTime, 0)) > 24*time.Hour || (t.Type == "vzdump" && strings.HasPrefix(t.Source, "pve:")) {
			continue
		}
		// A busy disk is let go by the appliance itself (unmountStore); it reports when that fails.
		if t.Type == "unmount-device" && strings.Contains(t.Status, "target is busy") {
			continue
		}
		d := &notify.Details{Status: "error", Link: "/aufgaben", Facts: []notify.Fact{
			{Label: "Server", Value: t.Server},
			{Label: "Aufgabe", Value: TaskLabel(t.Type) + " " + t.ID},
			{Label: "Beginn", Value: time.Unix(t.StartTime, 0).Format("02.01.2006 15:04")},
			{Label: "Dauer", Value: humanDuration(t.EndTime - t.StartTime)},
			{Label: "Ergebnis", Value: t.Status},
		}}
		kind, id, _ := strings.Cut(t.Source, ":")
		var lines []prox.TaskLine
		var err error
		if kind == "pbs" {
			if pbs, _, e := c.PBS(id); e == nil {
				lines, err = pbs.TaskLog(ctx, t.UPID)
			}
		} else if pve, _, e := c.PVE(id); e == nil {
			lines, err = pve.TaskLog(ctx, t.UPID)
		}
		if err == nil && len(lines) > 0 {
			d.Log = taskLogExcerpt(lines)
		}
		c.EventDetails("critical", t.Server, TaskLabel(t.Type)+" fehlgeschlagen", strings.TrimSpace(t.ID+" "+t.Node)+": "+t.Status, d)
	}
	if changed || len(keep) != len(seen) {
		if len(keep) == 0 {
			keep["-"] = true
		}
		_ = c.St.SetSetting("seen-failed", keep)
	}
}

// TaskLabel names a task type in German.
func TaskLabel(t string) string {
	m := map[string]string{
		"vzdump": "Sicherung", "backup": "Sicherung", "syncjob": "Kopierjob", "sync": "Kopierjob",
		"verify": "Prüfung", "verificationjob": "Prüfjob", "verify_snapshot": "Prüfung", "verify_group": "Prüfung",
		"garbage_collection": "Speicher freigeben", "prune": "Aufräumen", "prunejob": "Aufräumjob",
		"qmrestore": "Wiederherstellung VM", "vzrestore": "Wiederherstellung CT", "reader": "Lesezugriff",
		"mount-device": "Platte einhängen", "unmount-device": "Platte aushängen", "create-datastore": "Datastore anlegen",
		"delete-datastore": "Datastore löschen", "dircreate": "Platte vorbereiten", "wipedisk": "Platte löschen",
	}
	if l, ok := m[t]; ok {
		return l
	}
	return t
}
