package core

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/prox"
)

// Backup coverage: per guest the chain backup → copy on a second server → USB disk → restore
// test, each link with its age and a traffic light. USB disks can only be read while they are
// plugged in, so the appliance remembers the content of every datastore whenever it reads it.

// GroupSeen is a backup group of a datastore: newest backup and number of backups.
type GroupSeen struct {
	Last  int64 `json:"last"`
	Count int   `json:"count"`
}

// StoreContent is what a datastore held when it was last read.
type StoreContent struct {
	At     int64                `json:"at"`
	Groups map[string]GroupSeen `json:"groups"` // "vm/100"
}

// SaveContent remembers the groups of a datastore.
func (c *Core) SaveContent(srvID, store string, groups []prox.Group) {
	sc := StoreContent{At: time.Now().Unix(), Groups: map[string]GroupSeen{}}
	for _, g := range groups {
		sc.Groups[g.Type+"/"+g.ID] = GroupSeen{Last: g.LastBackup, Count: g.Count}
	}
	_ = c.St.SetSetting("content:"+usageKey(srvID, store), sc)
}

// StoreContentCached returns what a datastore held when it was last read (At 0: never).
func (c *Core) StoreContentCached(srvID, store string) StoreContent {
	var sc StoreContent
	_ = c.St.GetSetting("content:"+usageKey(srvID, store), &sc)
	return sc
}

const contentScanEvery = 10 * time.Minute

var contentMu sync.Mutex

func (c *Core) scheduleContentScan(now time.Time) {
	var last int64
	_ = c.St.GetSetting("contentscan", &last)
	if now.Unix()-last < int64(contentScanEvery.Seconds()) {
		return
	}
	_ = c.St.SetSetting("contentscan", now.Unix())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		c.RefreshCoverage(ctx)
	}()
}

// RefreshCoverage reads all datastores (remembering their content) and stores the summary.
func (c *Core) RefreshCoverage(ctx context.Context) CoverageSummary {
	guests, _ := c.Coverage(ctx)
	c.saveCoverageSummary(guests)
	return c.CoverageSummaryInfo()
}

// BackupRef is where backups of a guest lie.
type BackupRef struct {
	Server     string `json:"server"`
	ServerName string `json:"server_name"`
	Store      string `json:"store"`
	Last       int64  `json:"last"`
	Count      int    `json:"count"`
	Kind       string `json:"kind"`           // fixed, usb, archive
	Seen       int64  `json:"seen,omitempty"` // USB disk not plugged in: when its content was read
	Label      string `json:"label,omitempty"`
}

// Link is one link of the chain.
type Link struct {
	State string `json:"state"` // ok, warn, bad, none (not expected)
	Last  int64  `json:"last,omitempty"`
	Where string `json:"where,omitempty"`
	Note  string `json:"note,omitempty"`
}

// Chain is the backup chain of a guest.
type Chain struct {
	Backup Link   `json:"backup"`
	Copy   Link   `json:"copy"`
	USB    Link   `json:"usb"`
	Test   Link   `json:"test"`
	State  string `json:"state"` // worst link: ok, warn, bad
}

// GuestCoverage is a guest with its backups and its chain.
type GuestCoverage struct {
	prox.Guest
	Cluster     string      `json:"cluster"`
	ClusterName string      `json:"cluster_name"`
	InJob       bool        `json:"in_job"`
	Backups     []BackupRef `json:"backups"` // readable now (for restoring)
	Seen        []BackupRef `json:"seen"`    // on USB disks that are not plugged in
	LastBackup  int64       `json:"last_backup"`
	Chain       Chain       `json:"chain"`
}

// age judges how old a link is: ok up to okDays, warn up to warnDays, then bad.
func age(last int64, now time.Time, okDays, warnDays int) string {
	d := now.Sub(time.Unix(last, 0))
	switch {
	case d <= time.Duration(okDays)*24*time.Hour:
		return "ok"
	case d <= time.Duration(warnDays)*24*time.Hour:
		return "warn"
	}
	return "bad"
}

func worse(a, b string) string {
	rank := map[string]int{"none": 0, "ok": 1, "warn": 2, "bad": 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// Coverage builds the chain of every guest of the connected clusters.
func (c *Core) Coverage(ctx context.Context) ([]GuestCoverage, []string) {
	contentMu.Lock()
	defer contentMu.Unlock()
	now := time.Now()
	clusters, _ := c.Clusters()
	servers, _ := c.Servers()
	rots, _ := c.Rotations()
	archives, _ := c.Archives()
	type rotDisk struct {
		rot   *Rotation
		label string
	}
	rotOf := map[string]rotDisk{}
	for i, r := range rots {
		for _, d := range r.Disks {
			rotOf[usageKey(r.ServerID, d.Store)] = rotDisk{&rots[i], d.Label}
		}
	}
	archiveOf := map[string]string{}
	for _, a := range archives {
		archiveOf[usageKey(a.ServerID, a.Store)] = a.Name
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var errs []string
	refs := map[string][]BackupRef{} // "vm/100" → where
	fixedServers := map[string]bool{}
	for _, srv := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pbs := c.PBSClient(srv)
			usage, err := pbs.Usage(ctx)
			if err != nil {
				mu.Lock()
				errs = append(errs, srv.Name+": "+err.Error())
				mu.Unlock()
				return
			}
			for _, u := range usage {
				k := usageKey(srv.ID, u.Store)
				kind, label := "fixed", ""
				if rd, ok := rotOf[k]; ok {
					kind, label = "usb", rd.rot.Name+" · "+rd.label
				} else if name, ok := archiveOf[k]; ok {
					kind, label = "archive", name
				}
				var content StoreContent
				seen := int64(0)
				if u.MountStatus == "notmounted" {
					content = c.StoreContentCached(srv.ID, u.Store)
					if content.At == 0 {
						continue
					}
					seen = content.At
				} else {
					groups, err := pbs.Groups(ctx, u.Store)
					if err != nil {
						continue
					}
					c.SaveContent(srv.ID, u.Store, groups)
					content = c.StoreContentCached(srv.ID, u.Store)
				}
				mu.Lock()
				for g, x := range content.Groups {
					refs[g] = append(refs[g], BackupRef{Server: srv.ID, ServerName: srv.Name, Store: u.Store, Last: x.Last, Count: x.Count, Kind: kind, Seen: seen, Label: label})
					if kind == "fixed" && (strings.HasPrefix(g, "vm/") || strings.HasPrefix(g, "ct/")) {
						fixedServers[srv.ID] = true
					}
				}
				mu.Unlock()
			}
		}()
	}
	out := []GuestCoverage{}
	for _, cl := range clusters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pve := c.PVEClient(cl)
			list, err := pve.Guests(ctx)
			if err != nil {
				mu.Lock()
				errs = append(errs, cl.Name+": "+err.Error())
				mu.Unlock()
				return
			}
			nb := map[int]bool{}
			if nbs, err := pve.NotBackedUp(ctx); err == nil {
				for _, g := range nbs {
					nb[g.VMID] = true
				}
			}
			mu.Lock()
			for _, g := range list {
				if g.Template == 0 {
					out = append(out, GuestCoverage{Guest: g, Cluster: cl.ID, ClusterName: cl.Name, InJob: !nb[g.VMID]})
				}
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	plans, _ := c.RestoreTestPlans()
	runs := c.RestoreTestRuns()
	twoServers := len(fixedServers) >= 2
	unreadSets := map[string]bool{} // rotation sets none of whose disks was ever read
	for _, r := range rots {
		read := false
		for _, d := range r.Disks {
			if c.StoreContentCached(r.ServerID, d.Store).At > 0 {
				read = true
			}
		}
		unreadSets[r.ID] = !read
	}
	for i := range out {
		c.chainOf(&out[i], refs, rots, unreadSets, twoServers, plans, runs, now)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ClusterName != out[j].ClusterName {
			return out[i].ClusterName < out[j].ClusterName
		}
		return out[i].VMID < out[j].VMID
	})
	return out, errs
}

func (c *Core) chainOf(g *GuestCoverage, refs map[string][]BackupRef, rots []Rotation, unreadSets map[string]bool, twoServers bool,
	plans []RestoreTestPlan, runs []RestoreTestRun, now time.Time) {
	typ := "vm"
	if g.Type == "lxc" {
		typ = "ct"
	}
	vmid := strconv.Itoa(g.VMID)
	g.Backups, g.Seen = []BackupRef{}, []BackupRef{}
	var fixed, usb []BackupRef
	for _, r := range refs[typ+"/"+vmid] {
		if r.Seen == 0 {
			g.Backups = append(g.Backups, r)
		} else {
			g.Seen = append(g.Seen, r)
		}
		switch r.Kind {
		case "fixed":
			fixed = append(fixed, r)
		case "usb":
			usb = append(usb, r)
		}
	}
	sort.SliceStable(g.Backups, func(a, b int) bool {
		if (g.Backups[a].Server == "local") != (g.Backups[b].Server == "local") {
			return g.Backups[a].Server == "local"
		}
		return g.Backups[a].Last > g.Backups[b].Last
	})
	// The original: the newest; at the same time (a copy has the times of the original) the
	// appliance's own server, then the one with more backups.
	local := c.localServerID()
	sort.SliceStable(fixed, func(a, b int) bool {
		if fixed[a].Last != fixed[b].Last {
			return fixed[a].Last > fixed[b].Last
		}
		if (fixed[a].Server == local) != (fixed[b].Server == local) {
			return fixed[a].Server == local
		}
		return fixed[a].Count > fixed[b].Count
	})
	sort.Slice(usb, func(a, b int) bool { return usb[a].Last > usb[b].Last })
	ch := &g.Chain

	// 1. backup
	if len(fixed) > 0 {
		g.LastBackup = fixed[0].Last
		ch.Backup = Link{State: age(fixed[0].Last, now, 2, 8), Last: fixed[0].Last, Where: fixed[0].ServerName + " / " + fixed[0].Store}
	} else {
		ch.Backup = Link{State: "bad", Note: "noch nie gesichert"}
	}
	if !g.InJob {
		ch.Backup.State, ch.Backup.Note = "bad", "in keinem Sicherungsjob"
	}

	// 2. copy on a second server
	switch {
	case !twoServers:
		ch.Copy = Link{State: "none", Note: "kein zweiter Backup-Server"}
	case len(fixed) == 0 && !g.InJob:
		ch.Copy = Link{State: "none", Note: "—"}
	case len(fixed) == 0:
		ch.Copy = Link{State: "bad", Note: "fehlt"}
	default:
		ch.Copy = Link{State: "bad", Note: "nur auf " + fixed[0].ServerName}
		for _, f := range fixed {
			if f.Server != fixed[0].Server {
				ch.Copy = Link{State: age(f.Last, now, 3, 10), Last: f.Last, Where: f.ServerName + " / " + f.Store}
				break
			}
		}
	}

	// 3. USB disk: expected when the guest is in the source of a rotation set
	var sets []Rotation
	for _, r := range rots {
		for _, f := range fixed {
			if f.Server == r.SourceServer && f.Store == r.SourceStore {
				sets = append(sets, r)
				break
			}
		}
	}
	if len(sets) == 0 {
		ch.USB = Link{State: "none", Note: "kein Wechselplatten-Satz"}
	} else {
		okD, warnD, unknown := 3, 10, true
		for _, r := range sets {
			if r.Rhythm == "weekly" {
				okD, warnD = 10, 21
			}
			if !unreadSets[r.ID] {
				unknown = false
			}
		}
		switch {
		case len(usb) > 0:
			ch.USB = Link{State: age(usb[0].Last, now, okD, warnD), Last: usb[0].Last, Where: usb[0].Label}
		case unknown:
			ch.USB = Link{State: "none", Note: "Inhalt der Platten noch unbekannt – wird beim nächsten Anstecken gelesen"}
		default:
			ch.USB = Link{State: "bad", Note: "auf keiner Platte"}
		}
	}

	// 4. restore test
	planIDs := map[string]bool{}
	for _, p := range plans {
		if p.Enabled && p.Cluster == g.Cluster && p.wants(vmid) {
			planIDs[p.ID] = true
		}
	}
	if len(planIDs) == 0 {
		ch.Test = Link{State: "none", Note: "kein Restore-Test geplant"}
	} else {
		ch.Test = Link{State: "warn", Note: "noch nie getestet"}
	found:
		for _, run := range runs { // newest first
			if !planIDs[run.PlanID] {
				continue
			}
			for _, r := range run.Results {
				if r.VMID != vmid || (r.Status != "ok" && r.Status != "warning" && r.Status != "error") {
					continue
				}
				at := run.End
				if at == 0 {
					at = run.Start
				}
				ch.Test = Link{Last: at, Where: run.PlanName}
				switch r.Status {
				case "ok":
					ch.Test.State = age(at, now, 45, 100)
				case "warning":
					ch.Test.State, ch.Test.Note = "warn", "mit Hinweisen"
				default:
					ch.Test.State, ch.Test.Note = "bad", "fehlgeschlagen"
				}
				break found
			}
		}
	}
	ch.State = "ok"
	for _, l := range []Link{ch.Backup, ch.Copy, ch.USB, ch.Test} {
		ch.State = worse(ch.State, l.State)
	}
}

// localServerID is the Backup Server inside this appliance ("" if there is none).
func (c *Core) localServerID() string {
	servers, _ := c.Servers()
	for _, s := range servers {
		if s.Local {
			return s.ID
		}
	}
	return ""
}

// CoverageSummary counts the chains of the guests in a backup job (for the overview).
type CoverageSummary struct {
	At    int64    `json:"at"`
	Total int      `json:"total"`
	OK    int      `json:"ok"`
	Warn  int      `json:"warn"`
	Bad   int      `json:"bad"`
	Names []string `json:"names,omitempty"` // a few broken ones
}

func (c *Core) saveCoverageSummary(guests []GuestCoverage) {
	s := CoverageSummary{At: time.Now().Unix()}
	for _, g := range guests {
		if !g.InJob {
			continue // reported on its own
		}
		s.Total++
		switch g.Chain.State {
		case "ok":
			s.OK++
		case "warn":
			s.Warn++
		case "bad":
			s.Bad++
			if len(s.Names) < 3 {
				s.Names = append(s.Names, fmt.Sprintf("%d %s", g.VMID, g.Name))
			}
		}
	}
	_ = c.St.SetSetting("coverage-summary", s)
}

// CoverageSummaryInfo returns the last summary.
func (c *Core) CoverageSummaryInfo() CoverageSummary {
	var s CoverageSummary
	_ = c.St.GetSetting("coverage-summary", &s)
	return s
}
