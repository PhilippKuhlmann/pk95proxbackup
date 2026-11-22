package core

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"pk95proxbackup/internal/prox"
)

// Time plan: everything that runs on a schedule – backup jobs of Proxmox VE, copy, prune and
// verify jobs and the garbage collection of the Backup Servers, USB disks, restore tests, host
// backups and automatic updates – with its start times in a window of days and how long its
// last runs took, so overlaps show and times can be moved.

// TimelineItem is one scheduled thing.
type TimelineItem struct {
	Key       string      `json:"key"`   // pve:<cluster>:<job>, sync|prune|verify:<server>:<job>, gc:<server>:<store>, usb:…, rt:…, host:…, update:…
	Group     string      `json:"group"` // backup, copy, usb, verify, prune, gc, restoretest, host, update
	Title     string      `json:"title"`
	Sub       string      `json:"sub"`
	Schedule  string      `json:"schedule"` // as configured
	Text      string      `json:"text"`     // readable
	Editable  bool        `json:"editable"` // the time can be changed here
	Enabled   bool        `json:"enabled"`
	Link      string      `json:"link"`
	Stores    []string    `json:"stores,omitempty"` // "server/store" it reads or writes
	Starts    []int64     `json:"starts"`
	Duration  int64       `json:"duration"`          // seconds drawn: the last run, else an estimate
	Typical   int64       `json:"typical,omitempty"` // median of the last runs
	Estimated bool        `json:"estimated,omitempty"`
	Runs      []JobRun    `json:"runs"`              // oldest first, at most timelineRuns
	Problem   string      `json:"problem,omitempty"` // a schedule the appliance cannot draw
	Simple    *SimplePlan `json:"simple,omitempty"`  // once a day on chosen weekdays
}

// SimplePlan is a schedule "at this time on these weekdays" (1 = Monday).
type SimplePlan struct {
	Time     string `json:"time"`
	Weekdays []int  `json:"weekdays"`
}

// Timeline is the time plan of a window of days.
type Timeline struct {
	From   int64          `json:"from"`
	To     int64          `json:"to"`
	Now    int64          `json:"now"`
	Items  []TimelineItem `json:"items"`
	Errors []string       `json:"errors"`
	Work   WorkHours      `json:"work"`
}

// WorkHours are the hours people work: cleanup, garbage collection and verification are
// planned outside of them, and the time plan shades them.
type WorkHours struct {
	Enabled  bool   `json:"enabled"`
	Weekdays []int  `json:"weekdays"` // 1 = Monday … 7 = Sunday
	From     string `json:"from"`     // "07:00"
	To       string `json:"to"`       // "18:00"
}

// minutes is the working time as minutes of the day.
func (w WorkHours) minutes() (from, to int, ok bool) {
	a, err1 := time.Parse("15:04", w.From)
	b, err2 := time.Parse("15:04", w.To)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	from, to = a.Hour()*60+a.Minute(), b.Hour()*60+b.Minute()
	return from, to, from < to
}

// overlaps tells whether something starting on weekday wd (0 = Monday) at minute start and
// running dur minutes touches the working time.
func (w WorkHours) overlaps(wd, start, dur int) bool {
	from, to, ok := w.minutes()
	if !w.Enabled || !ok {
		return false
	}
	days := map[int]bool{}
	for _, d := range w.Weekdays {
		days[d-1] = true
	}
	for m := 0; m < dur && m < 7*dayMin; m++ {
		t := start + m
		if d, min := (wd+t/dayMin)%7, t%dayMin; days[d] && min >= from && min < to {
			return true
		}
	}
	return false
}

// WorkHours returns the working time (default Monday to Friday, 07:00 to 18:00, off).
func (c *Core) WorkHours() WorkHours {
	w := WorkHours{Weekdays: []int{1, 2, 3, 4, 5}, From: "07:00", To: "18:00"}
	_ = c.St.GetSetting("work-hours", &w)
	if w.Weekdays == nil {
		w.Weekdays = []int{}
	}
	return w
}

// SetWorkHours stores the working time.
func (c *Core) SetWorkHours(w WorkHours) error {
	for _, t := range []string{w.From, w.To} {
		if _, err := time.Parse("15:04", t); err != nil {
			return fmt.Errorf("Uhrzeit „%s“: bitte als HH:MM angeben", t)
		}
	}
	if _, _, ok := w.minutes(); !ok {
		return fmt.Errorf("die Arbeitszeit muss vor %s enden, nicht davor beginnen", w.To)
	}
	seen := map[int]bool{}
	days := []int{}
	for _, d := range w.Weekdays {
		if d < 1 || d > 7 {
			return fmt.Errorf("Wochentag %d gibt es nicht", d)
		}
		if !seen[d] {
			seen[d] = true
			days = append(days, d)
		}
	}
	sort.Ints(days)
	if w.Enabled && len(days) == 0 {
		return fmt.Errorf("mindestens einen Arbeitstag wählen")
	}
	w.Weekdays = days
	return c.St.SetSetting("work-hours", w)
}

const timelineRuns = 10

// timelineMaxRun: a longer run is a misread time, not a duration to draw.
const timelineMaxRun = 3 * 24 * 3600

// timelineGuess is the assumed duration of something that has not run yet.
var timelineGuess = map[string]time.Duration{
	"backup": 30 * time.Minute, "copy": 15 * time.Minute, "usb": 30 * time.Minute, "verify": 30 * time.Minute,
	"prune": 2 * time.Minute, "gc": 10 * time.Minute, "restoretest": 20 * time.Minute, "host": 2 * time.Minute,
	"update": 15 * time.Minute,
}

// CalendarText makes a calendar event readable: "täglich 21:00", "Mo–Fr 02:30", "Sa 04:00".
func CalendarText(spec string) string {
	c, err := ParseCalendar(spec)
	if err != nil {
		return spec
	}
	hm, days, ok := c.Simple()
	if !ok {
		return spec
	}
	if len(days) == 7 {
		return "täglich " + hm
	}
	// consecutive days as a range
	var parts []string
	for i := 0; i < len(days); {
		j := i
		for j+1 < len(days) && days[j+1] == days[j]+1 {
			j++
		}
		switch {
		case j-i >= 2:
			parts = append(parts, weekdayShort[days[i]]+"–"+weekdayShort[days[j]])
		case j > i:
			parts = append(parts, weekdayShort[days[i]], weekdayShort[days[j]])
		default:
			parts = append(parts, weekdayShort[days[i]])
		}
		i = j + 1
	}
	return strings.Join(parts, ", ") + " " + hm
}

// plan fills start times and the readable text from a calendar event.
func (it *TimelineItem) plan(spec string, from, to time.Time) {
	it.Schedule, it.Text, it.Starts = spec, CalendarText(spec), []int64{}
	c, err := ParseCalendar(spec)
	if err != nil {
		it.Problem = "Zeitplan „" + spec + "“ kann die Appliance nicht darstellen"
		return
	}
	for _, t := range c.Between(from, to, 600) {
		it.Starts = append(it.Starts, t.Unix())
	}
	if hm, days, ok := c.Simple(); ok {
		it.Simple = &SimplePlan{Time: hm, Weekdays: days}
	}
}

// took sets runs and durations: the last run is drawn, the median of the last ones is shown.
func (it *TimelineItem) took(runs []JobRun) {
	var done []JobRun
	for _, r := range runs {
		if r.End > r.Start && r.Start > 0 && r.End-r.Start <= timelineMaxRun {
			done = append(done, r)
		}
	}
	if len(done) > timelineRuns {
		done = done[len(done)-timelineRuns:]
	}
	it.Runs = done
	if it.Runs == nil {
		it.Runs = []JobRun{}
	}
	if len(done) == 0 {
		it.Duration, it.Estimated = int64(timelineGuess[it.Group].Seconds()), true
		return
	}
	it.Duration = done[len(done)-1].End - done[len(done)-1].Start
	ds := make([]int64, len(done))
	for i, r := range done {
		ds[i] = r.End - r.Start
	}
	sort.Slice(ds, func(a, b int) bool { return ds[a] < ds[b] })
	it.Typical = ds[len(ds)/2]
}

// lastRun is the run a PBS job reports (start from its task ID).
func lastRun(upid string, end int64, state string) []JobRun {
	start := prox.UPIDStart(upid)
	if start == 0 || end <= start {
		return nil
	}
	st := "ok"
	if state != "" && state != "OK" {
		st = "error"
		if strings.HasPrefix(state, "WARNINGS") {
			st = "warning"
		}
	}
	return []JobRun{{Start: start, End: end, Status: st}}
}

// Timeline builds the time plan for the day before now until a week ahead.
func (c *Core) Timeline(ctx context.Context, now time.Time) Timeline {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	from, to := day.AddDate(0, 0, -1), day.AddDate(0, 0, 8)
	tl := Timeline{From: from.Unix(), To: to.Unix(), Now: now.Unix(), Items: []TimelineItem{}, Errors: []string{}, Work: c.WorkHours()}
	runs := c.jobRunsState().Runs
	add := func(it TimelineItem) { tl.Items = append(tl.Items, it) }

	// Backup jobs of Proxmox VE
	clusters, _ := c.Clusters()
	for _, cl := range clusters {
		pve := c.PVEClient(cl)
		jobs, err := pve.BackupJobs(ctx)
		if err != nil {
			tl.Errors = append(tl.Errors, cl.Name+": "+err.Error())
			continue
		}
		target := map[string]string{}
		if storages, err := pve.Storages(ctx); err == nil {
			for _, st := range storages {
				if srv, ok := c.MatchServer(st); ok && st.Type == "pbs" {
					target[st.Storage] = srv.ID + "/" + st.Datastore
				}
			}
		}
		for _, j := range jobs {
			it := TimelineItem{Key: "pve:" + cl.ID + ":" + j.ID, Group: "backup", Title: firstNonEmpty(j.Comment, j.ID),
				Sub: "Sicherungsjob · " + cl.Name, Editable: true, Enabled: j.IsEnabled(), Link: "/jobs"}
			if t := target[j.Storage]; t != "" {
				it.Stores = []string{t}
			}
			it.plan(j.Schedule, from, to)
			it.took(runs[it.Key])
			add(it)
		}
	}

	// Jobs and garbage collection of the Backup Servers
	servers, _ := c.Servers()
	srvName := map[string]string{}
	for _, srv := range servers {
		srvName[srv.ID] = srv.Name
	}
	for _, srv := range servers {
		pbs := c.PBSClient(srv)
		for _, kind := range []string{"sync", "prune", "verify"} {
			jobs, err := pbs.Jobs(ctx, kind)
			if err != nil {
				tl.Errors = append(tl.Errors, srv.Name+": "+err.Error())
				break
			}
			for _, j := range jobs {
				if j.Schedule == "" || strings.HasPrefix(j.ID, "pk95r-") || strings.HasPrefix(j.ID, "pk95a-") {
					continue // by hand, or a rotation or archive disk (planned by the appliance)
				}
				it := TimelineItem{Key: kind + ":" + srv.ID + ":" + j.ID, Editable: true, Enabled: !j.Disable,
					Stores: []string{srv.ID + "/" + j.Store}}
				where := j.Store
				if j.NS != "" {
					where += " / " + j.NS
				}
				switch kind {
				case "sync":
					it.Group, it.Title, it.Sub, it.Link = "copy", firstNonEmpty(j.Comment, j.ID), "Kopierjob · "+srv.Name+" / "+j.Store, "/kopierjobs?server="+srv.ID
					if j.Remote == "" && j.RemoteStore != "" {
						it.Stores = append(it.Stores, srv.ID+"/"+j.RemoteStore)
					}
				case "prune":
					it.Group, it.Title, it.Sub, it.Link = "prune", "Aufräumen "+where, "alte Stände löschen · "+srv.Name, "/datastores?server="+srv.ID
				case "verify":
					it.Group, it.Title, it.Sub, it.Link = "verify", "Prüfen "+where, "Sicherungen lesen und prüfen · "+srv.Name, "/datastores?server="+srv.ID
				}
				it.plan(j.Schedule, from, to)
				r := runs["sync:"+srv.ID+":"+j.ID]
				if kind != "sync" || len(r) == 0 {
					r = lastRun(j.LastRunUPID, j.LastRunEnd, j.LastRunState)
				}
				it.took(r)
				add(it)
			}
		}
		cfgs, err := pbs.Datastores(ctx)
		if err != nil {
			continue
		}
		gcs := map[string]prox.GCJob{}
		if list, err := pbs.GCJobs(ctx); err == nil {
			for _, g := range list {
				gcs[g.Store] = g
			}
		}
		for _, d := range cfgs {
			if d.GCSchedule == "" {
				continue
			}
			it := TimelineItem{Key: "gc:" + srv.ID + ":" + d.Name, Group: "gc", Title: "Speicher freigeben " + d.Name,
				Sub: "gelöschte Stände räumen · " + srv.Name, Editable: true, Enabled: true, Link: "/datastores?server=" + srv.ID,
				Stores: []string{srv.ID + "/" + d.Name}}
			it.plan(d.GCSchedule, from, to)
			var r []JobRun
			if g, ok := gcs[d.Name]; ok {
				if start := prox.UPIDStart(g.LastRunUPID); start > 0 && g.Duration > 0 {
					r = []JobRun{{Start: start, End: start + g.Duration, Status: map[bool]string{true: "ok", false: "error"}[g.LastRunState == "OK"]}}
				} else {
					r = lastRun(g.LastRunUPID, g.LastRunEnd, g.LastRunState)
				}
			}
			it.took(r)
			add(it)
		}
		// automatic updates of the Backup Server
		if a := c.AutoUpdate(srv.ID); a.Enabled && a.Time != "" && len(a.Weekdays) > 0 {
			if spec, err := SimpleCalendar(a.Time, a.Weekdays); err == nil {
				it := TimelineItem{Key: "update:" + srv.ID, Group: "update", Title: "Updates " + srv.Name, Sub: "Debian- und Proxmox-Pakete",
					Enabled: true, Link: "/einstellungen/aktualisierung"}
				if a.Reboot {
					it.Sub += ", Neustart wenn nötig"
				}
				it.plan(spec, from, to)
				it.took(nil)
				add(it)
			}
		}
	}

	// USB disks planned by the appliance
	rots, _ := c.Rotations()
	for _, r := range rots {
		if r.Time == "" {
			continue
		}
		it := TimelineItem{Key: "usb:" + r.ID, Group: "usb", Title: r.Name, Sub: "Wechselplatten · " + srvName[r.ServerID],
			Enabled: true, Link: "/wechselplatten", Stores: []string{r.SourceServer + "/" + r.SourceStore}}
		it.plan(r.Time, from, to)
		it.took(runs[it.Key])
		add(it)
	}

	// Restore tests
	plans, _ := c.RestoreTestPlans()
	all := c.RestoreTestRuns() // newest first
	for _, p := range plans {
		if !p.Enabled {
			continue
		}
		it := TimelineItem{Key: "rt:" + p.ID, Group: "restoretest", Title: "Restore-Test " + p.Name, Sub: p.Node + " · " + srvName[p.PBS] + " / " + p.Store,
			Schedule: p.ScheduleText(), Text: p.ScheduleText(), Enabled: true, Link: "/restoretests", Starts: []int64{},
			Stores: []string{p.PBS + "/" + p.Store}}
		for _, t := range p.NextRuns(from, 60) {
			if t < to.Unix() {
				it.Starts = append(it.Starts, t)
			}
		}
		var rr []JobRun
		for i := len(all) - 1; i >= 0; i-- {
			if run := all[i]; run.PlanID == p.ID && run.End > 0 {
				rr = append(rr, JobRun{Start: run.Start, End: run.End, Status: run.Status})
			}
		}
		it.took(rr)
		add(it)
	}

	// Host backups of Proxmox VE
	confs, states := c.hostConfs(), c.hostStates()
	for _, cl := range clusters {
		conf := confs[cl.ID]
		if !conf.Enabled || conf.Time == "" {
			continue
		}
		it := TimelineItem{Key: "host:" + cl.ID, Group: "host", Title: "Host-Sicherung " + cl.Name, Sub: "/etc, /root und Systeminfo aller Knoten",
			Enabled: true, Link: "/hosts"}
		it.plan(conf.Time, from, to)
		// all nodes in one run: from the first start to the last end
		var run JobRun
		for _, st := range states[cl.ID] {
			if st.LastRun == 0 || st.Duration <= 0 {
				continue
			}
			if run.Start == 0 || st.LastRun < run.Start {
				run.Start = st.LastRun
			}
			if e := st.LastRun + st.Duration; e > run.End {
				run.End = e
			}
		}
		if run.Start > 0 {
			run.Status = "ok"
			it.took([]JobRun{run})
		} else {
			it.took(nil)
		}
		add(it)
	}

	order := map[string]int{"backup": 0, "copy": 1, "usb": 2, "verify": 3, "prune": 4, "gc": 5, "restoretest": 6, "host": 7, "update": 8}
	sort.SliceStable(tl.Items, func(i, j int) bool {
		a, b := tl.Items[i], tl.Items[j]
		if order[a.Group] != order[b.Group] {
			return order[a.Group] < order[b.Group]
		}
		return a.Title < b.Title
	})
	return tl
}

// SetSchedule changes when a job runs (PVE backup job, PBS copy, prune or verify job, garbage
// collection) and returns its name for the event log.
func (c *Core) SetSchedule(ctx context.Context, key, spec string) (title string, err error) {
	spec = strings.TrimSpace(spec)
	if _, err := ParseCalendar(spec); err != nil {
		return "", fmt.Errorf("Zeitplan: %w", err)
	}
	parts := strings.SplitN(key, ":", 3)
	if len(parts) != 3 {
		return "", fmt.Errorf("„%s“ wird auf seiner eigenen Seite eingestellt", key)
	}
	kind, owner, id := parts[0], parts[1], parts[2]
	switch kind {
	case "pve":
		cl, err := c.Cluster(owner)
		if err != nil {
			return "", err
		}
		if err := c.PVEClient(cl).UpdateBackupJob(ctx, id, prox.Params{"schedule": spec}); err != nil {
			return "", err
		}
		title = "Sicherungsjob " + id + " (" + cl.Name + ")"
	case "sync", "prune", "verify", "gc":
		srv, err := c.Server(owner)
		if err != nil {
			return "", err
		}
		pbs := c.PBSClient(srv)
		if kind == "gc" {
			err = pbs.UpdateDatastore(ctx, id, prox.Params{"gc-schedule": spec})
			title = "Speicher freigeben " + id + " (" + srv.Name + ")"
		} else {
			err = pbs.UpdateJob(ctx, kind, id, prox.Params{"schedule": spec})
			title = map[string]string{"sync": "Kopierjob ", "prune": "Aufräumjob ", "verify": "Prüfjob "}[kind] + id + " (" + srv.Name + ")"
		}
		if err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("„%s“ wird auf seiner eigenen Seite eingestellt", key)
	}
	c.jobRuns.mu.Lock()
	c.jobRuns.built = time.Time{} // job cards show the new schedule
	c.jobRuns.mu.Unlock()
	return title, nil
}
