package core

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/prox"
)

// Job runs for the overview: every backup job of Proxmox VE, every copy job and every set of
// rotation disks gets a card with its last runs. The appliance records each finished run
// (backup tasks of one job on all nodes count as one run) and keeps the last 30.

const (
	jobRunsKeep  = 30
	jobRunsEvery = 2 * time.Minute
	jobRunsBack  = 40 * 24 * time.Hour // first look: read this far back
)

// JobRun is one run of a job.
type JobRun struct {
	Start  int64  `json:"start"`
	End    int64  `json:"end"`
	Status string `json:"status"` // ok, warning, error
	OK     int    `json:"ok,omitempty"`
	Failed int    `json:"failed,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
	Label  string `json:"label,omitempty"` // rotation disk
}

type jobRunsState struct {
	Runs map[string][]JobRun `json:"runs"`
	Seen map[string]int64    `json:"seen"` // task → its start (to forget old ones)
	Read map[string]int64    `json:"read"` // server → last full look
}

type jobRunsRuntime struct {
	mu    sync.Mutex
	at    time.Time
	cards []JobCard
	built time.Time
}

func (c *Core) jobRunsState() jobRunsState {
	var s jobRunsState
	_ = c.St.GetSetting("jobruns", &s)
	if s.Runs == nil {
		s.Runs = map[string][]JobRun{}
	}
	if s.Seen == nil {
		s.Seen = map[string]int64{}
	}
	if s.Read == nil {
		s.Read = map[string]int64{}
	}
	return s
}

func addRun(s *jobRunsState, key string, r JobRun) {
	runs := append(s.Runs[key], r)
	sort.Slice(runs, func(i, j int) bool { return runs[i].Start < runs[j].Start })
	if len(runs) > jobRunsKeep {
		runs = runs[len(runs)-jobRunsKeep:]
	}
	s.Runs[key] = runs
}

// recordJobRuns adds the finished runs since the last look (every two minutes at most).
func (c *Core) recordJobRuns(ctx context.Context, ov *Overview, now time.Time) {
	c.jobRuns.mu.Lock()
	if now.Sub(c.jobRuns.at) < jobRunsEvery {
		c.jobRuns.mu.Unlock()
		return
	}
	c.jobRuns.at = now
	c.jobRuns.mu.Unlock()
	s := c.jobRunsState()
	c.recordBackupRuns(ctx, ov, &s)
	c.recordSyncRuns(ctx, &s, now)
	for upid, t := range s.Seen {
		if now.Unix()-t > int64((jobRunsBack + 7*24*time.Hour).Seconds()) {
			delete(s.Seen, upid)
		}
	}
	_ = c.St.SetSetting("jobruns", s)
	c.jobRuns.mu.Lock()
	c.jobRuns.built = time.Time{} // cards are built anew
	c.jobRuns.mu.Unlock()
}

// recordBackupRuns reads the logs of finished backup tasks of Proxmox VE and assigns them to
// their job: by --job-id, else by storage and guest selection.
func (c *Core) recordBackupRuns(ctx context.Context, ov *Overview, s *jobRunsState) {
	var done, running []TaskInfo
	for _, t := range ov.Tasks {
		if t.Type != "vzdump" || !strings.HasPrefix(t.Source, "pve:") {
			continue
		}
		if t.EndTime == 0 {
			running = append(running, t)
		} else if s.Seen[t.UPID] == 0 {
			done = append(done, t)
		}
	}
	sort.Slice(done, func(i, j int) bool { return done[i].StartTime < done[j].StartTime })
	type group struct{ tasks []TaskInfo }
	var groups []*group
	for _, t := range done {
		var g *group
		for _, x := range groups {
			if x.tasks[0].Source == t.Source && time.Duration(t.StartTime-x.tasks[0].StartTime)*time.Second <= backupWindow {
				g = x
			}
		}
		if g == nil {
			g = &group{}
			groups = append(groups, g)
		}
		g.tasks = append(g.tasks, t)
	}
	jobs := map[string][]prox.BackupJob{}
	for _, g := range groups {
		start := g.tasks[0].StartTime
		busy := false
		for _, r := range running {
			if r.Source == g.tasks[0].Source && r.StartTime >= start-int64(backupWindow.Seconds()) && r.StartTime <= start+int64(backupWindow.Seconds()) {
				busy = true
			}
		}
		if busy {
			continue // the other nodes of the run are not done yet
		}
		clusterID := strings.TrimPrefix(g.tasks[0].Source, "pve:")
		pve, _, err := c.PVE(clusterID)
		if err != nil {
			continue
		}
		if _, ok := jobs[clusterID]; !ok {
			jobs[clusterID], _ = pve.BackupJobs(ctx)
		}
		run := JobRun{Start: start}
		jobID := ""
		failedTask, readAll := false, true
		for _, t := range g.tasks {
			if t.EndTime > run.End {
				run.End = t.EndTime
			}
			if t.Status != "OK" && !strings.HasPrefix(t.Status, "WARNINGS") {
				failedTask = true
			}
			lines, err := pve.TaskLog(ctx, t.UPID)
			if err != nil {
				readAll = false
				break
			}
			if vj, ok := prox.ParseVzdumpJob(lines); ok && jobID == "" {
				jobID = matchBackupJob(jobs[clusterID], vj)
			}
			for _, gb := range prox.ParseVzdump(lines).Guests {
				if gb.OK {
					run.OK++
					run.Bytes += prox.ParseSize(gb.Transferred)
				} else {
					run.Failed++
				}
			}
		}
		if !readAll {
			continue // try again later
		}
		for _, t := range g.tasks {
			s.Seen[t.UPID] = t.StartTime
		}
		if jobID == "" {
			continue // a backup started by hand, no job
		}
		switch {
		case run.OK+run.Failed == 0 || run.OK == 0:
			run.Status = "error"
		case run.Failed == 0 && !failedTask:
			run.Status = "ok"
		default:
			run.Status = "warning"
		}
		addRun(s, "pve:"+clusterID+":"+jobID, run)
	}
}

func sameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	x, y = slices.DeleteFunc(x, func(s string) bool { return s == "" }), slices.DeleteFunc(y, func(s string) bool { return s == "" })
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}

// matchBackupJob finds the job of a backup task from its command line.
func matchBackupJob(jobs []prox.BackupJob, vj prox.VzdumpJob) string {
	if vj.JobID != "" {
		for _, j := range jobs {
			if j.ID == vj.JobID {
				return j.ID
			}
		}
	}
	var exact, sameStore []string
	for _, j := range jobs {
		if j.Storage != vj.Storage {
			continue
		}
		sameStore = append(sameStore, j.ID)
		if vj.All && j.All == 1 && sameSet(strings.Split(j.Exclude, ","), vj.Exclude) ||
			!vj.All && j.All == 0 && sameSet(strings.Split(j.VMID, ","), vj.IDs) {
			exact = append(exact, j.ID)
		}
	}
	if len(exact) == 1 {
		return exact[0]
	}
	// "Run now" of a job sends its guests as a list: one job on this storage is enough.
	if len(exact) == 0 && len(sameStore) == 1 && len(vj.IDs) > 1 {
		return sameStore[0]
	}
	return ""
}

// recordSyncRuns reads the finished copy jobs of every Backup Server (rotation disks count
// for their set).
func (c *Core) recordSyncRuns(ctx context.Context, s *jobRunsState, now time.Time) {
	servers, _ := c.Servers()
	rots, _ := c.Rotations()
	disk := map[string]struct{ rot, label string }{}
	for _, r := range rots {
		for _, d := range r.Disks {
			disk[r.SyncJobID(d)] = struct{ rot, label string }{r.ID, d.Label}
		}
	}
	for _, srv := range servers {
		pbs := c.PBSClient(srv)
		jobs, err := pbs.Jobs(ctx, "sync")
		if err != nil {
			continue
		}
		ids := map[string]bool{}
		for _, j := range jobs {
			ids[j.ID] = true
		}
		since := now.Add(-3 * 24 * time.Hour).Unix()
		if s.Read[srv.ID] == 0 {
			since = now.Add(-jobRunsBack).Unix()
		}
		tasks, err := pbs.TasksOfType(ctx, "syncjob", since)
		if err != nil {
			continue
		}
		s.Read[srv.ID] = now.Unix()
		for _, t := range tasks {
			if t.EndTime == 0 || s.Seen[t.UPID] != 0 {
				continue
			}
			s.Seen[t.UPID] = t.StartTime
			job := ""
			// worker id: <remote>:<remote-store>:<store>:<ns>:<job id> (systemd-escaped)
			for _, part := range strings.Split(unescapeWorkerID(t.WorkerID), ":") {
				if ids[part] {
					job = part
				}
			}
			if job == "" {
				continue
			}
			run := JobRun{Start: t.StartTime, End: t.EndTime, Status: "error"}
			switch {
			case t.Status == "OK":
				run.Status = "ok"
			case strings.HasPrefix(t.Status, "WARNINGS"):
				run.Status = "warning"
			}
			if d, ok := disk[job]; ok {
				run.Label = d.label
				addRun(s, "usb:"+d.rot, run)
				continue
			}
			addRun(s, "sync:"+srv.ID+":"+job, run)
		}
	}
}

// JobCard is one job on the overview.
type JobCard struct {
	Key       string   `json:"key"`
	Kind      string   `json:"kind"` // pve, sync, usb
	Title     string   `json:"title"`
	Sub       string   `json:"sub"`
	Status    string   `json:"status"` // ok, warning, error, running, none, off
	What      string   `json:"what"`
	Target    string   `json:"target"`
	Schedule  string   `json:"schedule"`
	Retention string   `json:"retention,omitempty"`
	NextRun   int64    `json:"next_run,omitempty"`
	LastOK    int64    `json:"last_ok,omitempty"`
	Runs      []JobRun `json:"runs"`
	Link      string   `json:"link"`
}

// JobCards lists the jobs with their last runs (built at most once a minute).
func (c *Core) JobCards(ctx context.Context) []JobCard {
	c.jobRuns.mu.Lock()
	if time.Since(c.jobRuns.built) < time.Minute && c.jobRuns.cards != nil {
		out := c.jobRuns.cards
		c.jobRuns.mu.Unlock()
		return out
	}
	c.jobRuns.mu.Unlock()
	out := c.buildJobCards(ctx)
	c.jobRuns.mu.Lock()
	c.jobRuns.cards, c.jobRuns.built = out, time.Now()
	c.jobRuns.mu.Unlock()
	return out
}

func (c *Core) buildJobCards(ctx context.Context) []JobCard {
	s := c.jobRunsState()
	out := []JobCard{}
	finish := func(card JobCard, running bool, enabled bool) JobCard {
		card.Runs = s.Runs[card.Key]
		if card.Runs == nil {
			card.Runs = []JobRun{}
		}
		card.Status = "none"
		if n := len(card.Runs); n > 0 {
			card.Status = card.Runs[n-1].Status
		}
		for _, r := range card.Runs {
			if r.Status != "error" && r.End > card.LastOK {
				card.LastOK = r.End
			}
		}
		if running {
			card.Status = "running"
		}
		if !enabled {
			card.Status = "off"
		}
		return card
	}
	clusters, _ := c.Clusters()
	names := c.guestNames(ctx)
	for _, cl := range clusters {
		pve := c.PVEClient(cl)
		jobs, err := pve.BackupJobs(ctx)
		if err != nil {
			continue
		}
		storages, _ := pve.Storages(ctx)
		target := map[string]string{}
		for _, st := range storages {
			if st.Type != "pbs" {
				continue
			}
			if srv, ok := c.MatchServer(st); ok {
				target[st.Storage] = srv.Name + " / " + st.Datastore
			}
		}
		for _, j := range jobs {
			card := JobCard{Key: "pve:" + cl.ID + ":" + j.ID, Kind: "pve", Title: firstNonEmpty(j.Comment, j.ID), Sub: "Proxmox VE · " + cl.Name,
				Schedule: j.Schedule, NextRun: j.NextRun, Retention: strings.ReplaceAll(j.PruneBackups, ",", ", "), Link: "/jobs"}
			switch {
			case j.All == 1 && j.Exclude != "":
				card.What = fmt.Sprintf("Alle VMs und Container außer %d", len(strings.Split(j.Exclude, ",")))
			case j.All == 1:
				card.What = "Alle VMs und Container"
			default:
				var list []string
				for _, id := range strings.Split(j.VMID, ",") {
					n := names["vm/"+id] + names["ct/"+id]
					list = append(list, strings.TrimSpace(id+" "+n))
				}
				card.What = strings.Join(list, ", ")
			}
			if j.Node != "" {
				card.What += " (nur " + j.Node + ")"
			}
			card.Target = j.Storage
			if t := target[j.Storage]; t != "" {
				card.Target = t
			}
			if card.Retention == "" {
				card.Retention = "vom Datastore"
			}
			out = append(out, finish(card, false, j.IsEnabled()))
		}
	}
	servers, _ := c.Servers()
	srvName := map[string]string{}
	for _, srv := range servers {
		srvName[srv.ID] = srv.Name
	}
	for _, srv := range servers {
		jobs, err := c.PBSClient(srv).Jobs(ctx, "sync")
		if err != nil {
			continue
		}
		for _, j := range jobs {
			if strings.HasPrefix(j.ID, "pk95r-") || strings.HasPrefix(j.ID, "pk95a-") {
				continue // rotation and archive disks have their own card
			}
			from := j.RemoteStore
			if j.Remote != "" {
				from = j.Remote + " / " + j.RemoteStore
			}
			card := JobCard{Key: "sync:" + srv.ID + ":" + j.ID, Kind: "sync", Title: firstNonEmpty(j.Comment, j.ID), Sub: "Kopierjob · " + srv.Name,
				What: from, Target: srv.Name + " / " + j.Store, Schedule: firstNonEmpty(j.Schedule, "von Hand"), NextRun: j.NextRun, Link: "/kopierjobs?server=" + srv.ID}
			if j.TransferLast > 0 {
				card.Retention = "nur die neuesten " + strconv.Itoa(j.TransferLast) + " Stände"
			}
			out = append(out, finish(card, j.LastRunUPID != "" && j.LastRunEnd == 0, !j.Disable))
		}
	}
	rots, _ := c.Rotations()
	for _, r := range rots {
		var labels []string
		for _, d := range r.Disks {
			labels = append(labels, d.Label)
		}
		sched := "beim Anstecken"
		if r.Time != "" {
			sched = "täglich " + r.Time
			if r.RunOnMount {
				sched += " und beim Anstecken"
			}
		}
		card := JobCard{Key: "usb:" + r.ID, Kind: "usb", Title: r.Name, Sub: "Wechselplatten · " + srvName[r.ServerID],
			What: srvName[r.SourceServer] + " / " + r.SourceStore, Target: strings.Join(labels, ", "), Schedule: sched, Link: "/wechselplatten"}
		if r.TransferLast > 0 {
			card.Retention = "nur die neuesten " + strconv.Itoa(r.TransferLast) + " Stände"
		}
		out = append(out, finish(card, false, true))
	}
	return out
}
