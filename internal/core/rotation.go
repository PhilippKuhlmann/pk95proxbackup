package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
	"pk95proxbackup/internal/store"
)

// Rotation is a set of removable disks (USB) that take turns holding a copy of a datastore.
// Each disk is a removable datastore on the target server with its own sync job.
type Rotation struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ServerID      string `json:"server_id"`     // where the disks are plugged in
	SourceServer  string `json:"source_server"` // where the backups are
	SourceStore   string `json:"source_store"`
	SourceNS      string `json:"source_ns,omitempty"`
	Rhythm        string `json:"rhythm"`   // daily, weekly
	SwapDay       int    `json:"swap_day"` // weekly: 1 = Monday … 7 = Sunday
	Time          string `json:"time"`     // HH:MM daily copy, "" = only when plugged in
	RunOnMount    bool   `json:"run_on_mount"`
	UnmountOnDone bool   `json:"unmount_on_done"`
	// TransferLast copies only the newest N backups of each VM/CT (0 = all).
	TransferLast int       `json:"transfer_last"`
	KeepLast     int       `json:"keep_last"`
	KeepDaily    int       `json:"keep_daily"`
	KeepWeekly   int       `json:"keep_weekly"`
	KeepMonthly  int       `json:"keep_monthly"`
	Disks        []RotDisk `json:"disks"`
}

// RotDisk is one disk of a rotation set.
type RotDisk struct {
	Key   string `json:"key"`   // short id for job names
	Store string `json:"store"` // removable datastore on the target server
	Label string `json:"label"` // e.g. "Montag", "Woche A"
	Added int64  `json:"added"`
}

// SyncJobID is the PBS sync job that copies onto the disk.
func (r Rotation) SyncJobID(d RotDisk) string { return "pk95r-" + r.ID + "-" + d.Key }

// RhythmDays is the swap interval in days.
func (r Rotation) RhythmDays() int {
	if r.Rhythm == "weekly" {
		return 7
	}
	return 1
}

var timeRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// Validate checks the definition.
func (r *Rotation) Validate() map[string]string {
	errs := map[string]string{}
	if strings.TrimSpace(r.Name) == "" {
		errs["name"] = "Bitte einen Namen angeben."
	}
	if r.ServerID == "" {
		errs["server_id"] = "Backup-Server wählen, an dem die Platten stecken."
	}
	if r.SourceServer == "" || r.SourceStore == "" {
		errs["source_store"] = "Quelle (Datastore mit den Sicherungen) wählen."
	}
	if r.Rhythm != "daily" && r.Rhythm != "weekly" {
		errs["rhythm"] = "Täglich oder wöchentlich."
	}
	if r.Rhythm == "weekly" && (r.SwapDay < 1 || r.SwapDay > 7) {
		r.SwapDay = 5
	}
	if r.Time != "" && !timeRe.MatchString(r.Time) {
		errs["time"] = "Uhrzeit als HH:MM, z. B. 21:30."
	}
	if r.Time == "" && !r.RunOnMount {
		errs["time"] = "Uhrzeit angeben oder „beim Anstecken kopieren“ einschalten."
	}
	if r.TransferLast < 0 || r.TransferLast > 1000 {
		errs["transfer_last"] = "0 (alle) bis 1000."
	}
	for _, k := range []int{r.KeepLast, r.KeepDaily, r.KeepWeekly, r.KeepMonthly} {
		if k < 0 || k > 1000 {
			errs["keep_last"] = "Werte zwischen 0 und 1000."
		}
	}
	return errs
}

// Rotations lists the rotation sets.
func (c *Core) Rotations() ([]Rotation, error) { return store.List[Rotation](c.St, KindRotation) }

// Rotation loads one set.
func (c *Core) Rotation(id string) (Rotation, error) {
	var r Rotation
	err := c.St.Get(KindRotation, id, &r)
	return r, err
}

// remoteFor makes sure the target server has a remote pointing at the source server.
// Returns "" when both are the same server (local sync).
func (c *Core) RemoteFor(ctx context.Context, target *prox.PBS, targetSrv PBSServer, sourceID string) (string, error) {
	if sourceID == targetSrv.ID {
		return "", nil
	}
	src, err := c.Server(sourceID)
	if err != nil {
		return "", fmt.Errorf("Quell-Server: %w", err)
	}
	name := JobID("pk95", src.ID)
	port := src.Port
	if port == 0 {
		port = 8007
	}
	host := src.Address()
	if src.Local && !targetSrv.Local {
		host = src.PublicHost
	}
	err = target.PutRemote(ctx, prox.Remote{
		Name: name, Host: host, Port: port, AuthID: src.AuthID, Fingerprint: src.Fingerprint, Comment: "PK95 ProxBackup: " + src.Name,
	}, src.Secret)
	if err != nil {
		return "", fmt.Errorf("Remote auf %s anlegen: %w", targetSrv.Name, err)
	}
	return name, nil
}

// SyncParams returns the PBS parameters of the sync job for one disk.
func (c *Core) syncParams(r Rotation, d RotDisk, remote string) prox.Params {
	p := prox.Params{
		"store":           d.Store,
		"remote-store":    r.SourceStore,
		"remove-vanished": false,
		"run-on-mount":    r.RunOnMount,
		"comment":         "PK95 Wechselplatte " + d.Label + " (" + r.Name + ")",
	}
	if remote != "" {
		p["remote"] = remote
	}
	if r.SourceNS != "" {
		p["remote-ns"] = r.SourceNS
	}
	if r.TransferLast > 0 {
		p["transfer-last"] = r.TransferLast
	}
	return p
}

// ApplyRotation (re)creates the sync jobs of all disks of a set on the target server.
func (c *Core) ApplyRotation(ctx context.Context, r Rotation) error {
	pbs, srv, err := c.PBS(r.ServerID)
	if err != nil {
		return err
	}
	remote, err := c.RemoteFor(ctx, pbs, srv, r.SourceServer)
	if err != nil {
		return err
	}
	jobs, err := pbs.Jobs(ctx, "sync")
	if err != nil {
		return err
	}
	exists := map[string]bool{}
	for _, j := range jobs {
		exists[j.ID] = true
	}
	for _, d := range r.Disks {
		id := r.SyncJobID(d)
		p := c.syncParams(r, d, remote)
		if exists[id] {
			var del []string
			if remote == "" {
				del = append(del, "remote")
			}
			if r.TransferLast == 0 {
				del = append(del, "transfer-last")
			}
			if len(del) > 0 {
				p["delete"] = del
			}
			err = pbs.UpdateJob(ctx, "sync", id, p)
		} else {
			p["id"] = id
			err = pbs.CreateJob(ctx, "sync", p)
		}
		if err != nil {
			return fmt.Errorf("Kopierjob für %s: %w", d.Label, err)
		}
	}
	return nil
}

// RemoveRotationDisk deletes the sync job of a disk (the datastore and its data stay).
func (c *Core) RemoveRotationDisk(ctx context.Context, r Rotation, d RotDisk) error {
	pbs, _, err := c.PBS(r.ServerID)
	if err != nil {
		return err
	}
	if err := pbs.DeleteJob(ctx, "sync", r.SyncJobID(d)); err != nil && !prox.IsStatus(err, 404) && !strings.Contains(err.Error(), "does not exist") {
		return err
	}
	return nil
}

// ---- runtime ----

type diskRuntime struct {
	SeenUPID   string `json:"seen_upid"`
	LastOK     int64  `json:"last_ok"`
	LastFail   int64  `json:"last_fail"`
	LastError  string `json:"last_error,omitempty"`
	LastGC     int64  `json:"last_gc"`
	Phase      string `json:"phase,omitempty"` // mounting, syncing, pruning, gc, unmounting
	PhaseSince int64  `json:"phase_since,omitempty"`
	DoneAt     int64  `json:"done_at"` // aftercare finished: disk can be swapped
	LastDay    string `json:"last_day,omitempty"`
	Remind     int64  `json:"remind,omitempty"`
}

func rtKey(r Rotation, d RotDisk) string { return r.ID + "/" + d.Key }

func (c *Core) runtime(key string) *diskRuntime {
	c.mu.Lock()
	defer c.mu.Unlock()
	rt := c.rotRT[key]
	if rt == nil {
		rt = &diskRuntime{}
		_ = c.St.GetSetting("rt:"+key, rt)
		c.rotRT[key] = rt
	}
	return rt
}

func (c *Core) saveRuntime(key string, rt *diskRuntime) {
	c.mu.Lock()
	cp := *rt
	c.mu.Unlock()
	_ = c.St.SetSetting("rt:"+key, cp)
}

func (c *Core) setPhase(key string, rt *diskRuntime, phase string) {
	c.mu.Lock()
	if rt.Phase != phase {
		rt.PhaseSince = time.Now().Unix()
	}
	rt.Phase = phase
	c.mu.Unlock()
	c.saveRuntime(key, rt)
	c.RequestRefresh()
}

func (c *Core) tryBusy(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rotBusy[key] {
		return false
	}
	c.rotBusy[key] = true
	return true
}

func (c *Core) doneBusy(key string) {
	c.mu.Lock()
	delete(c.rotBusy, key)
	c.mu.Unlock()
}

// DiskState is the shown state of one rotation disk.
type DiskState struct {
	RotDisk
	Present   bool   `json:"present"`
	Mounted   bool   `json:"mounted"`
	State     string `json:"state"` // absent, idle, running, ready, error
	Phase     string `json:"phase,omitempty"`
	LastOK    int64  `json:"last_ok"`
	LastFail  int64  `json:"last_fail"`
	LastError string `json:"last_error,omitempty"`
	Running   bool   `json:"running"`
	Total     int64  `json:"total"`
	Used      int64  `json:"used"`
	Stale     bool   `json:"stale"`
	Error     string `json:"error,omitempty"`
}

// RotationState is the shown state of a rotation set.
type RotationState struct {
	Rotation
	Disks      []DiskState `json:"disks"`
	ServerName string      `json:"server_name"`
	SourceName string      `json:"source_name"`
	LastCopy   int64       `json:"last_copy"`
	Overdue    bool        `json:"overdue"`
	NextSwap   string      `json:"next_swap"`
	Hint       string      `json:"hint"`
	Error      string      `json:"error,omitempty"`
}

type pbsFacts struct {
	usage map[string]prox.DatastoreUsage
	cfg   map[string]prox.DatastoreConfig
	uuids map[string]bool
	jobs  map[string]prox.Job
	err   error
}

func (c *Core) facts(ctx context.Context, pbs *prox.PBS) pbsFacts {
	f := pbsFacts{usage: map[string]prox.DatastoreUsage{}, cfg: map[string]prox.DatastoreConfig{}, uuids: map[string]bool{}, jobs: map[string]prox.Job{}}
	cfgs, err := pbs.Datastores(ctx)
	if err != nil {
		f.err = err
		return f
	}
	for _, d := range cfgs {
		f.cfg[d.Name] = d
	}
	if us, err := pbs.Usage(ctx); err == nil {
		for _, u := range us {
			f.usage[u.Store] = u
		}
	}
	if disks, err := pbs.Disks(ctx); err == nil {
		for _, d := range disks {
			for _, p := range d.Partitions {
				if p.UUID != "" {
					f.uuids[strings.ToLower(p.UUID)] = true
				}
			}
		}
	}
	if jobs, err := pbs.Jobs(ctx, "sync"); err == nil {
		for _, j := range jobs {
			f.jobs[j.ID] = j
		}
	}
	return f
}

// RotationStates evaluates all rotation sets (used by the monitor and the API).
func (c *Core) RotationStates(ctx context.Context) []RotationState {
	rots, _ := c.Rotations()
	factsBy := map[string]pbsFacts{}
	clients := map[string]*prox.PBS{}
	names := map[string]string{}
	if servers, err := c.Servers(); err == nil {
		for _, s := range servers {
			names[s.ID] = s.Name
		}
	}
	out := []RotationState{}
	for _, r := range rots {
		st := RotationState{Rotation: r, Disks: []DiskState{}, ServerName: names[r.ServerID], SourceName: names[r.SourceServer]}
		f, ok := factsBy[r.ServerID]
		if !ok {
			pbs, _, err := c.PBS(r.ServerID)
			if err != nil {
				f.err = err
			} else {
				clients[r.ServerID] = pbs
				f = c.facts(ctx, pbs)
			}
			factsBy[r.ServerID] = f
		}
		if f.err != nil {
			st.Error = f.err.Error()
		}
		now := time.Now()
		for _, d := range r.Disks {
			key := rtKey(r, d)
			rt := c.runtime(key)
			ds := DiskState{RotDisk: d, State: "absent"}
			cfg, known := f.cfg[d.Store]
			u := f.usage[d.Store]
			if f.err == nil && !known {
				ds.Error = "Datastore " + d.Store + " gibt es auf dem Server nicht mehr"
			}
			ds.Mounted = u.MountStatus == "mounted"
			ds.Present = ds.Mounted || (cfg.BackingDevice != "" && f.uuids[strings.ToLower(cfg.BackingDevice)])
			ds.Total, ds.Used = u.Total, u.Used
			if j, ok := f.jobs[r.SyncJobID(d)]; ok && f.err == nil {
				if pbs := clients[r.ServerID]; pbs != nil {
					c.trackJob(ctx, pbs, r, d, rt, j)
				}
				ds.Running = j.LastRunUPID != "" && j.LastRunEnd == 0
			} else if f.err == nil && known {
				ds.Error = "Kopierjob fehlt – „Speichern“ beim Satz legt ihn neu an"
			}
			c.mu.Lock()
			ds.Phase, ds.LastOK, ds.LastFail, ds.LastError = rt.Phase, rt.LastOK, rt.LastFail, rt.LastError
			done := rt.DoneAt
			c.mu.Unlock()
			switch {
			case ds.Running || ds.Phase != "":
				ds.State = "running"
			case ds.LastFail > ds.LastOK && ds.Present:
				ds.State = "error"
			case ds.Present && !ds.Mounted && done > 0 && done >= ds.LastOK:
				ds.State = "ready"
			case ds.Present:
				ds.State = "idle"
			}
			if ds.LastOK > st.LastCopy {
				st.LastCopy = ds.LastOK
			}
			limit := time.Duration(len(r.Disks)*r.RhythmDays()+r.RhythmDays()+1) * 24 * time.Hour
			ref := ds.LastOK
			if ref == 0 {
				ref = d.Added
			}
			ds.Stale = len(r.Disks) > 1 && now.Sub(time.Unix(ref, 0)) > limit
			st.Disks = append(st.Disks, ds)
		}
		grace := time.Duration(r.RhythmDays()+1) * 24 * time.Hour
		ref := st.LastCopy
		if ref == 0 && len(r.Disks) > 0 {
			ref = r.Disks[0].Added
		}
		st.Overdue = len(r.Disks) > 0 && ref > 0 && now.Sub(time.Unix(ref, 0)) > grace
		st.NextSwap, st.Hint = swapHint(r, st, now)
		out = append(out, st)
	}
	return out
}

var weekdays = []string{"", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag", "Sonntag"}

func swapHint(r Rotation, st RotationState, now time.Time) (string, string) {
	if len(r.Disks) == 0 {
		return "", "Noch keine Platte im Satz. Unter „Platte hinzufügen“ eine USB-Platte vorbereiten."
	}
	var plugged, ready []string
	for _, d := range st.Disks {
		if d.Present {
			plugged = append(plugged, d.Label)
		}
		if d.State == "ready" {
			ready = append(ready, d.Label)
		}
	}
	next := ""
	if r.Rhythm == "weekly" {
		wd := int(now.Weekday())
		if wd == 0 {
			wd = 7
		}
		days := (r.SwapDay - wd + 7) % 7
		if days == 0 {
			next = "heute (" + weekdays[r.SwapDay] + ")"
		} else {
			next = weekdays[r.SwapDay] + ", " + now.AddDate(0, 0, days).Format("02.01.")
		}
	} else {
		next = "täglich"
	}
	switch {
	case len(ready) > 0:
		return next, "Kopie fertig, " + strings.Join(ready, ", ") + " ist ausgehängt und kann abgezogen werden."
	case len(plugged) == 0:
		return next, "Keine Platte des Satzes angesteckt. Nächste Platte anstecken."
	default:
		return next, "Angesteckt: " + strings.Join(plugged, ", ") + "."
	}
}

// trackJob follows the runs of a disk's sync job and starts the aftercare when one finished.
func (c *Core) trackJob(ctx context.Context, pbs *prox.PBS, r Rotation, d RotDisk, rt *diskRuntime, j prox.Job) {
	key := rtKey(r, d)
	c.mu.Lock()
	seen := rt.SeenUPID
	c.mu.Unlock()
	if j.LastRunUPID == "" || j.LastRunUPID == seen || j.LastRunEnd == 0 {
		return
	}
	c.mu.Lock()
	rt.SeenUPID = j.LastRunUPID
	c.mu.Unlock()
	if seen == "" && rt.LastOK == 0 && rt.LastFail == 0 && time.Since(time.Unix(j.LastRunEnd, 0)) > time.Hour {
		// First look at an old run (e.g. after an update): only remember it.
		c.saveRuntime(key, rt)
		return
	}
	ok := j.LastRunState == "OK" || strings.HasPrefix(j.LastRunState, "WARNINGS")
	c.mu.Lock()
	if ok {
		rt.LastOK = j.LastRunEnd
		rt.LastError = ""
	} else {
		rt.LastFail = j.LastRunEnd
		rt.LastError = j.LastRunState
	}
	c.mu.Unlock()
	c.saveRuntime(key, rt)
	if !ok {
		det := &notify.Details{Status: "error", Link: "/wechselplatten", Facts: []notify.Fact{
			{Label: "Satz", Value: r.Name}, {Label: "Platte", Value: d.Label + " (" + d.Store + ")"}, {Label: "Ergebnis", Value: j.LastRunState}}}
		if lines, err := pbs.TaskLog(ctx, j.LastRunUPID); err == nil {
			det.Log = taskLogExcerpt(lines)
		}
		c.EventDetails("critical", "Wechselplatten", "Kopie auf "+d.Label+" fehlgeschlagen",
			fmt.Sprintf("Satz „%s“, Datastore %s: %s", r.Name, d.Store, j.LastRunState), det)
		return
	}
	if c.tryBusy(key) {
		go func() {
			defer c.doneBusy(key)
			c.aftercare(context.WithoutCancel(ctx), pbs, r, d, rt)
		}()
	}
}

// aftercare prunes the disk, collects garbage once a week and unmounts it.
func (c *Core) aftercare(ctx context.Context, pbs *prox.PBS, r Rotation, d RotDisk, rt *diskRuntime) {
	key := rtKey(r, d)
	defer c.setPhase(key, rt, "")
	var problems []string
	if r.KeepLast+r.KeepDaily+r.KeepWeekly+r.KeepMonthly > 0 {
		c.setPhase(key, rt, "pruning")
		if err := c.prune(ctx, pbs, d.Store, r); err != nil {
			problems = append(problems, "Aufräumen: "+err.Error())
		}
	}
	c.mu.Lock()
	gcDue := time.Since(time.Unix(rt.LastGC, 0)) > 6*24*time.Hour
	c.mu.Unlock()
	if gcDue {
		c.setPhase(key, rt, "gc")
		if upid, err := pbs.GC(ctx, d.Store); err != nil {
			problems = append(problems, "Speicher freigeben: "+err.Error())
		} else if _, err := pbs.WaitTask(ctx, upid, 12*time.Hour); err != nil {
			problems = append(problems, "Speicher freigeben: "+err.Error())
		} else {
			c.mu.Lock()
			rt.LastGC = time.Now().Unix()
			c.mu.Unlock()
		}
	}
	msg := fmt.Sprintf("Satz „%s“: Die Sicherungen von %s sind auf %s kopiert (%s).", r.Name, r.SourceStore, d.Label, fmtTime(rt.LastOK))
	// What is on the disk now (read before it is unmounted).
	det := &notify.Details{Status: "ok", RowsTitle: "Auf der Platte " + d.Label, Rows: c.diskContents(ctx, pbs, d.Store), Link: "/wechselplatten"}
	det.Facts = []notify.Fact{{Label: "Satz", Value: r.Name}, {Label: "Platte", Value: d.Label + " (" + d.Store + ")"}, {Label: "Quelle", Value: r.SourceStore}}
	if us, err := pbs.Usage(ctx); err == nil {
		for _, u := range us {
			if u.Store == d.Store && u.Total > 0 {
				det.Facts = append(det.Facts, notify.Fact{Label: "Belegung", Value: fmt.Sprintf("%s von %s (%d %%)", fmtBytes(u.Used), fmtBytes(u.Total), u.Used*100/u.Total)})
			}
		}
	}
	if r.UnmountOnDone {
		c.setPhase(key, rt, "unmounting")
		if upid, err := pbs.Unmount(ctx, d.Store); err != nil {
			problems = append(problems, "Aushängen: "+err.Error())
		} else if _, err := pbs.WaitTask(ctx, upid, 10*time.Minute); err != nil {
			problems = append(problems, "Aushängen: "+err.Error())
		} else {
			c.mu.Lock()
			rt.DoneAt = time.Now().Unix()
			c.mu.Unlock()
			msg += " Die Platte ist ausgehängt und kann abgezogen werden."
		}
	} else {
		c.mu.Lock()
		rt.DoneAt = time.Now().Unix()
		c.mu.Unlock()
	}
	c.saveRuntime(key, rt)
	if r.UnmountOnDone && rt.DoneAt > 0 {
		det.Facts = append(det.Facts, notify.Fact{Label: "Platte", Value: "ausgehängt – kann abgezogen werden"})
	}
	if len(problems) > 0 {
		det.Status, det.Log = "warning", problems
		c.EventDetails("warning", "Wechselplatten", "Kopie auf "+d.Label+" fertig, mit Problemen", msg, det)
		return
	}
	c.EventDetails("info", "Wechselplatten", "Kopie auf "+d.Label+" fertig", msg, det)
}

func (c *Core) prune(ctx context.Context, pbs *prox.PBS, store string, r Rotation) error {
	p := prox.Params{}
	for k, v := range map[string]int{"keep-last": r.KeepLast, "keep-daily": r.KeepDaily, "keep-weekly": r.KeepWeekly, "keep-monthly": r.KeepMonthly} {
		if v > 0 {
			p[k] = v
		}
	}
	if r.SourceNS != "" {
		p["ns"] = r.SourceNS
	}
	upid, err := pbs.StartTask(ctx, "/admin/datastore/"+url.PathEscape(store)+"/prune-datastore", p)
	if err != nil {
		return err
	}
	_, err = pbs.WaitTask(ctx, upid, 2*time.Hour)
	return err
}

// RunRotationDisk copies onto a disk now: mount it (which starts the job when run-on-mount
// is set) or run the job directly when it is already mounted.
func (c *Core) RunRotationDisk(ctx context.Context, r Rotation, d RotDisk) error {
	pbs, _, err := c.PBS(r.ServerID)
	if err != nil {
		return err
	}
	usage, err := pbs.Usage(ctx)
	if err != nil {
		return err
	}
	mounted := false
	for _, u := range usage {
		if u.Store == d.Store {
			mounted = u.MountStatus == "mounted" || u.MountStatus == "nonremovable"
		}
	}
	key := rtKey(r, d)
	rt := c.runtime(key)
	c.mu.Lock()
	rt.DoneAt = 0
	c.mu.Unlock()
	if !mounted {
		upid, err := pbs.Mount(ctx, d.Store)
		if err != nil {
			return fmt.Errorf("Platte %s einhängen: %w (steckt sie?)", d.Label, err)
		}
		if _, err := pbs.WaitTask(ctx, upid, 5*time.Minute); err != nil {
			return fmt.Errorf("Platte %s einhängen: %w", d.Label, err)
		}
		if r.RunOnMount {
			c.RequestRefresh()
			return nil
		}
	}
	err = pbs.RunJob(ctx, "sync", r.SyncJobID(d))
	c.RequestRefresh()
	return err
}

// EjectRotationDisk unmounts a disk so it can be pulled.
func (c *Core) EjectRotationDisk(ctx context.Context, r Rotation, d RotDisk) error {
	pbs, _, err := c.PBS(r.ServerID)
	if err != nil {
		return err
	}
	upid, err := pbs.Unmount(ctx, d.Store)
	if err != nil {
		return err
	}
	if _, err := pbs.WaitTask(ctx, upid, 10*time.Minute); err != nil {
		return err
	}
	rt := c.runtime(rtKey(r, d))
	c.mu.Lock()
	rt.DoneAt = time.Now().Unix()
	if rt.LastOK > rt.DoneAt {
		rt.DoneAt = rt.LastOK
	}
	c.mu.Unlock()
	c.saveRuntime(rtKey(r, d), rt)
	c.RequestRefresh()
	return nil
}

// scheduleRotations runs the daily copies and swap reminders (called every minute).
func (c *Core) scheduleRotations(ctx context.Context, states []RotationState, now time.Time) {
	for _, st := range states {
		r := st.Rotation
		day := now.Format("2006-01-02")
		if r.Time != "" && now.Format("15:04") >= r.Time {
			for _, d := range st.Disks {
				if !d.Present || d.Running || d.Phase != "" {
					continue
				}
				key := rtKey(r, d.RotDisk)
				rt := c.runtime(key)
				c.mu.Lock()
				doneToday := rt.LastDay == day
				// A copy that ran today already (e.g. when it was plugged in) counts.
				if time.Unix(rt.LastOK, 0).Format("2006-01-02") == day && rt.LastOK >= dayAt(now, r.Time) {
					doneToday = true
				}
				if !doneToday {
					rt.LastDay = day
				}
				c.mu.Unlock()
				if doneToday {
					continue
				}
				c.saveRuntime(key, rt)
				if err := c.RunRotationDisk(ctx, r, d.RotDisk); err != nil {
					c.Event("warning", "Wechselplatten", "Geplante Kopie auf "+d.Label+" nicht gestartet", err.Error())
				}
			}
		}
		c.remind(r, st, now)
	}
}

func dayAt(now time.Time, hhmm string) int64 {
	t, err := time.ParseInLocation("15:04", hhmm, now.Location())
	if err != nil {
		return 0
	}
	return time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location()).Unix()
}

// remind warns once a day about overdue copies and disks that were not plugged in for long.
func (c *Core) remind(r Rotation, st RotationState, now time.Time) {
	if now.Hour() < 9 {
		return
	}
	key := "remind:" + r.ID
	var last string
	_ = c.St.GetSetting(key, &last)
	day := now.Format("2006-01-02")
	if last == day {
		return
	}
	var msgs []string
	if st.Overdue {
		msgs = append(msgs, "Letzte Kopie auf eine Wechselplatte: "+fmtTime(st.LastCopy)+".")
	}
	for _, d := range st.Disks {
		if d.Stale {
			msgs = append(msgs, d.Label+" war lange nicht angesteckt (letzte Kopie "+fmtTime(d.LastOK)+").")
		}
	}
	if len(msgs) == 0 {
		return
	}
	_ = c.St.SetSetting(key, day)
	c.Event("warning", "Wechselplatten", "Wechselplatten von „"+r.Name+"“ prüfen", strings.Join(msgs, "\n"))
}

// PrepareDisk wipes a disk and creates a removable datastore on it.
func (c *Core) PrepareDisk(ctx context.Context, serverID, disk, store, fs string, removable bool) error {
	pbs, _, err := c.PBS(serverID)
	if err != nil {
		return err
	}
	if fs != "xfs" {
		fs = "ext4"
	}
	disks, err := pbs.Disks(ctx)
	if err != nil {
		return err
	}
	var found *prox.Disk
	for i := range disks {
		if disks[i].Name == disk {
			found = &disks[i]
		}
	}
	if found == nil {
		return errors.New("Platte " + disk + " nicht gefunden")
	}
	for _, p := range found.Partitions {
		if p.Mounted {
			return fmt.Errorf("auf %s ist %s eingehängt – diese Platte wird benutzt", disk, p.Name)
		}
	}
	if found.Used != "unused" {
		if err := pbs.WipeDisk(ctx, disk); err != nil {
			return fmt.Errorf("Platte löschen: %w", err)
		}
	}
	if err := pbs.CreateDirectoryStore(ctx, disk, store, fs, removable); err != nil {
		return fmt.Errorf("Datastore anlegen: %w", err)
	}
	kind := "Datastore"
	if removable {
		kind = "Wechsel-Datastore"
	}
	c.Event("info", "Platten", "Platte vorbereitet", fmt.Sprintf("%s (%s %s) ist jetzt der %s %s.", disk, strings.TrimSpace(found.Vendor), strings.TrimSpace(found.Model), kind, store))
	return nil
}

var _ = http.MethodGet
var _ = log.Printf
