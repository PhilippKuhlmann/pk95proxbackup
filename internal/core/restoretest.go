package core

import (
	"context"
	"errors"
	"fmt"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
	"pk95proxbackup/internal/store"
)

// Object kinds of restore tests.
const (
	KindRTPlan = "rtplan"
	KindRTRun  = "rtrun"
	rtTag      = "pk95-restoretest"
	rtPrefix   = "restoretest-"
)

// RestoreTestPlan says what is tested where and when.
type RestoreTestPlan struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Cluster  string `json:"cluster"`
	Node     string `json:"node"`    // where the test guests run
	Storage  string `json:"storage"` // where their disks go
	PBS      string `json:"pbs"`     // source: backup server …
	Store    string `json:"store"`   // … and datastore
	Select   string `json:"select"`  // all, include, exclude
	VMIDs    string `json:"vmids"`
	Weekdays []int  `json:"weekdays"` // 1 = Monday … 7 = Sunday
	Time     string `json:"time"`     // HH:MM
	MaxCores int    `json:"max_cores"`
	MaxMem   int    `json:"max_memory"` // MiB
	BootWait int    `json:"boot_wait"`  // seconds
	Limit    int    `json:"time_limit"` // minutes per guest for the restore
	IDBase   int    `json:"id_base"`    // first VMID for test guests
	KeepFail bool   `json:"keep_failed"`
}

// RestoreTestResult is the outcome for one guest.
type RestoreTestResult struct {
	Type       string `json:"type"` // vm, ct
	VMID       string `json:"vmid"`
	Name       string `json:"name"`
	BackupTime int64  `json:"backup_time"`
	TestID     int    `json:"test_id,omitempty"`
	Status     string `json:"status"` // pending, running, ok, warning, error, skipped
	Phase      string `json:"phase,omitempty"`
	Restore    int64  `json:"restore_secs,omitempty"`
	Boot       int64  `json:"boot_secs,omitempty"`
	DiskBytes  int64  `json:"disk_bytes,omitempty"`
	OS         string `json:"os,omitempty"`
	Hostname   string `json:"hostname,omitempty"`
	Screenshot string `json:"screenshot,omitempty"`
	Info       string `json:"info,omitempty"`
	Notes      string `json:"notes,omitempty"` // what was changed for isolation
}

// RestoreTestRun is one run of a plan.
type RestoreTestRun struct {
	ID       string              `json:"id"`
	PlanID   string              `json:"plan_id"`
	PlanName string              `json:"plan_name"`
	By       string              `json:"by"`
	Start    int64               `json:"start"`
	End      int64               `json:"end,omitempty"`
	Status   string              `json:"status"` // running, ok, warning, error, cancelled
	Error    string              `json:"error,omitempty"`
	Results  []RestoreTestResult `json:"results"`
}

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// Normalize fills defaults and validates.
func (p *RestoreTestPlan) Normalize() map[string]string {
	errs := map[string]string{}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		errs["name"] = "Bitte einen Namen angeben."
	}
	if p.Cluster == "" || p.Node == "" {
		errs["node"] = "Knoten wählen, auf dem getestet wird."
	}
	if p.Storage == "" {
		errs["storage"] = "Speicher für die Test-Disks wählen."
	}
	if p.PBS == "" || p.Store == "" {
		errs["store"] = "Quelle wählen."
	}
	if p.Select != "include" && p.Select != "exclude" {
		p.Select = "all"
	}
	p.VMIDs = strings.ReplaceAll(strings.TrimSpace(p.VMIDs), " ", "")
	if p.Select != "all" && !regexp.MustCompile(`^\d+(,\d+)*$`).MatchString(p.VMIDs) {
		errs["vmids"] = "Mindestens eine ID angeben, z. B. 100,101."
	}
	if p.Time != "" && !hhmm.MatchString(p.Time) {
		errs["time"] = "Uhrzeit als HH:MM."
	}
	if p.Enabled && (p.Time == "" || len(p.Weekdays) == 0) {
		errs["time"] = "Für den Zeitplan Wochentage und Uhrzeit angeben."
	}
	if p.BootWait <= 0 {
		p.BootWait = 180
	}
	if p.BootWait < 30 || p.BootWait > 1800 {
		errs["boot_wait"] = "30 bis 1800 Sekunden."
	}
	if p.Limit <= 0 {
		p.Limit = 240
	}
	if p.IDBase < 100 {
		p.IDBase = 990000
	}
	if p.MaxCores < 0 || p.MaxCores > 64 {
		errs["max_cores"] = "0 (wie Original) bis 64."
	}
	if p.MaxMem < 0 {
		errs["max_memory"] = "MiB, 0 = wie Original."
	}
	return errs
}

func (p RestoreTestPlan) wants(id string) bool {
	set := map[string]bool{}
	for _, x := range strings.Split(p.VMIDs, ",") {
		set[x] = true
	}
	switch p.Select {
	case "include":
		return set[id]
	case "exclude":
		return !set[id]
	}
	return true
}

// ---- state of the running test ----

type rtActive struct {
	run    *RestoreTestRun
	cancel context.CancelFunc
}

var (
	rtMu  sync.Mutex
	rtCur *rtActive
)

// RestoreTestPlans lists the plans.
func (c *Core) RestoreTestPlans() ([]RestoreTestPlan, error) {
	return store.List[RestoreTestPlan](c.St, KindRTPlan)
}

// RestoreTestRuns lists the runs, newest first.
func (c *Core) RestoreTestRuns() []RestoreTestRun {
	runs, _ := store.List[RestoreTestRun](c.St, KindRTRun)
	sort.Slice(runs, func(i, j int) bool { return runs[i].Start > runs[j].Start })
	rtMu.Lock()
	if rtCur != nil {
		for i := range runs {
			if runs[i].ID == rtCur.run.ID {
				runs[i] = copyRun(rtCur.run)
			}
		}
	}
	rtMu.Unlock()
	return runs
}

func copyRun(r *RestoreTestRun) RestoreTestRun {
	cp := *r
	cp.Results = append([]RestoreTestResult{}, r.Results...) // never nil: the UI expects a list
	return cp
}

// ActiveRestoreTest returns the running test, if any.
func (c *Core) ActiveRestoreTest() *RestoreTestRun {
	rtMu.Lock()
	defer rtMu.Unlock()
	if rtCur == nil {
		return nil
	}
	cp := copyRun(rtCur.run)
	return &cp
}

// CancelRestoreTest stops the running test (the current guest is cleaned up).
func (c *Core) CancelRestoreTest() bool {
	rtMu.Lock()
	defer rtMu.Unlock()
	if rtCur == nil {
		return false
	}
	rtCur.cancel()
	return true
}

// ScreenshotPath is where the picture of a result is stored.
func (c *Core) ScreenshotPath(runID, file string) string {
	return filepath.Join(c.DataDir, "restoretests", filepath.Base(runID), filepath.Base(file))
}

// StartRestoreTest begins a run of a plan in the background.
func (c *Core) StartRestoreTest(planID, by string) (string, error) {
	var plan RestoreTestPlan
	if err := c.St.Get(KindRTPlan, planID, &plan); err != nil {
		return "", err
	}
	rtMu.Lock()
	if rtCur != nil {
		rtMu.Unlock()
		return "", errors.New("es läuft schon ein Wiederherstellungstest")
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &RestoreTestRun{ID: time.Now().Format("20060102-150405"), PlanID: plan.ID, PlanName: plan.Name, By: by, Start: time.Now().Unix(), Status: "running", Results: []RestoreTestResult{}}
	rtCur = &rtActive{run: run, cancel: cancel}
	rtMu.Unlock()
	_ = c.St.Put(KindRTRun, run.ID, run)
	go func() {
		defer cancel()
		c.runRestoreTest(ctx, plan, run)
		rtMu.Lock()
		rtCur = nil
		rtMu.Unlock()
		c.pruneRuns(40)
		c.RequestRefresh()
	}()
	return run.ID, nil
}

func (c *Core) pruneRuns(keep int) {
	runs := c.RestoreTestRuns()
	for i := keep; i < len(runs); i++ {
		_ = c.St.Delete(KindRTRun, runs[i].ID)
		_ = os.RemoveAll(filepath.Join(c.DataDir, "restoretests", filepath.Base(runs[i].ID)))
	}
}

// update changes the run under the lock and saves it.
func (c *Core) rtUpdate(run *RestoreTestRun, fn func()) {
	rtMu.Lock()
	fn()
	cp := copyRun(run)
	rtMu.Unlock()
	_ = c.St.Put(KindRTRun, cp.ID, cp)
}

func (c *Core) runRestoreTest(ctx context.Context, plan RestoreTestPlan, run *RestoreTestRun) {
	fail := func(err error) {
		c.rtUpdate(run, func() { run.Status, run.Error, run.End = "error", err.Error(), time.Now().Unix() })
		c.EventDetails("critical", "Wiederherstellungstest", "Wiederherstellungstest konnte nicht starten",
			"Der geplante Test der Sicherungen konnte nicht durchgeführt werden. Bitte leiten Sie diese Nachricht an Ihren IT-Betreuer weiter.",
			&notify.Details{Status: "error", Link: "/restoretests", Log: []string{err.Error()}})
	}
	pve, cl, err := c.PVE(plan.Cluster)
	if err != nil {
		fail(fmt.Errorf("Proxmox VE: %w", err))
		return
	}
	srv, err := c.Server(plan.PBS)
	if err != nil {
		fail(fmt.Errorf("Backup-Server: %w", err))
		return
	}
	pbs := c.PBSClient(srv)
	if c.EnsureStorage == nil {
		fail(errors.New("intern: keine Speicherzuordnung"))
		return
	}
	storage, err := c.EnsureStorage(ctx, pve, cl, srv, plan.Store)
	if err != nil {
		fail(fmt.Errorf("Datastore %s in Proxmox VE: %w", plan.Store, err))
		return
	}
	snaps, err := pbs.Snapshots(ctx, plan.Store, "")
	if err != nil {
		fail(fmt.Errorf("Sicherungen von %s: %w", plan.Store, err))
		return
	}
	newest := map[string]prox.Snapshot{}
	for _, s := range snaps {
		if s.Type != "vm" && s.Type != "ct" {
			continue
		}
		k := s.Type + "/" + s.ID
		if s.Time > newest[k].Time {
			newest[k] = s
		}
	}
	names := c.guestNames(ctx)
	var todo []prox.Snapshot
	for _, s := range newest {
		if plan.wants(s.ID) {
			todo = append(todo, s)
		}
	}
	sort.Slice(todo, func(i, j int) bool {
		a, _ := strconv.Atoi(todo[i].ID)
		b, _ := strconv.Atoi(todo[j].ID)
		return a < b
	})
	c.rtUpdate(run, func() {
		for _, s := range todo {
			name := names[s.Type+"/"+s.ID]
			if name == "" {
				name = strings.TrimSpace(strings.SplitN(s.Comment, "\n", 2)[0])
			}
			run.Results = append(run.Results, RestoreTestResult{Type: s.Type, VMID: s.ID, Name: name, BackupTime: s.Time, Status: "pending"})
		}
	})
	if len(todo) == 0 {
		fail(errors.New("in " + plan.Store + " gibt es keine passende Sicherung"))
		return
	}
	c.sweepRestoreTests(ctx, pve)
	bridges, _ := pve.Bridges(ctx, plan.Node)
	for i, s := range todo {
		if ctx.Err() != nil {
			c.rtUpdate(run, func() {
				for j := i; j < len(run.Results); j++ {
					run.Results[j].Status, run.Results[j].Info = "skipped", "Test abgebrochen"
				}
			})
			break
		}
		c.testOne(ctx, plan, pve, pbs, storage, bridges, s, run, i)
	}
	c.finishRun(plan, cl, run, ctx.Err() != nil)
}

// testOne restores one backup under a free test ID, isolates and starts it, judges it and
// removes it again.
func (c *Core) testOne(ctx context.Context, plan RestoreTestPlan, pve *prox.PVE, pbs *prox.PBS, storage string, bridges []string,
	snap prox.Snapshot, run *RestoreTestRun, idx int) {
	set := func(fn func(r *RestoreTestResult)) { c.rtUpdate(run, func() { fn(&run.Results[idx]) }) }
	typ := "qemu"
	file := "qemu-server.conf.blob"
	if snap.Type == "ct" {
		typ, file = "lxc", "pct.conf.blob"
	}
	set(func(r *RestoreTestResult) { r.Status, r.Phase = "running", "prüft Platz und Arbeitsspeicher" })

	// What will the guest need?
	var disk, mem int64
	if conf, err := pbs.SnapshotFile(ctx, plan.Store, "", snap.Type, snap.ID, snap.Time, file); err == nil {
		disk, mem = guestNeeds(string(conf), typ)
		if n := prox.GuestNameFromConfig(conf); n != "" {
			set(func(r *RestoreTestResult) {
				if r.Name == "" {
					r.Name = n
				}
			})
		}
	}
	if plan.MaxMem > 0 && mem > int64(plan.MaxMem)<<20 {
		mem = int64(plan.MaxMem) << 20
	}
	set(func(r *RestoreTestResult) { r.DiskBytes = disk })
	skip := func(msg string) {
		set(func(r *RestoreTestResult) { r.Status, r.Phase, r.Info = "skipped", "", msg })
	}
	if st, err := pve.StorageStatus(ctx, plan.Node, plan.Storage); err == nil && st.Total > 0 {
		need := disk + disk/10 + 1<<30
		if st.Avail < need || st.Used+need > st.Total*95/100 {
			skip(fmt.Sprintf("Übersprungen: auf %s sind %s frei, gebraucht werden %s (mit Reserve)", plan.Storage, fmtBytes(st.Avail), fmtBytes(need)))
			return
		}
	}
	if ns, err := pve.NodeStatus(ctx, plan.Node); err == nil && ns.Memory.Total > 0 {
		free := ns.Memory.Total - ns.Memory.Used
		if mem > 0 && free < mem+2<<30 {
			skip(fmt.Sprintf("Übersprungen: auf %s sind %s Arbeitsspeicher frei, gebraucht werden %s", plan.Node, fmtBytes(free), fmtBytes(mem+2<<30)))
			return
		}
	}

	testID, err := c.freeTestID(ctx, pve, plan.IDBase)
	if err != nil {
		set(func(r *RestoreTestResult) { r.Status, r.Phase, r.Info = "error", "", err.Error() })
		return
	}
	set(func(r *RestoreTestResult) { r.TestID, r.Phase = testID, "stellt wieder her" })

	created := false
	failed := false
	defer func() {
		if !created {
			return
		}
		if failed && plan.KeepFail {
			set(func(r *RestoreTestResult) {
				r.Info += fmt.Sprintf(" – Test-%s %d bleibt zur Untersuchung erhalten", map[string]string{"qemu": "VM", "lxc": "Container"}[typ], testID)
			})
			return
		}
		set(func(r *RestoreTestResult) { r.Phase = "räumt auf" })
		if err := c.destroyTestGuest(context.WithoutCancel(ctx), pve, plan.Node, typ, testID, true); err != nil {
			set(func(r *RestoreTestResult) { r.Info += " – Test-Gast konnte nicht gelöscht werden: " + err.Error() })
		}
		set(func(r *RestoreTestResult) { r.Phase = "" })
	}()

	volume := fmt.Sprintf("%s:backup/%s/%s/%s", storage, snap.Type, snap.ID, time.Unix(snap.Time, 0).UTC().Format("2006-01-02T15:04:05Z"))
	start := time.Now()
	upid, err := pve.Restore(ctx, plan.Node, typ, testID, volume, plan.Storage, prox.RestoreOptions{Unique: true})
	if err != nil {
		failed = true
		set(func(r *RestoreTestResult) {
			r.Status, r.Phase, r.Info = "error", "", "Wiederherstellung nicht gestartet: "+err.Error()
		})
		return
	}
	created = true
	rctx, rcancel := context.WithTimeout(ctx, time.Duration(plan.Limit)*time.Minute)
	_, err = pve.WaitTaskCtx(rctx, upid)
	rcancel()
	if err != nil {
		failed = true
		if rctx.Err() != nil {
			_ = pve.StopTask(context.WithoutCancel(ctx), upid)
			time.Sleep(3 * time.Second)
		}
		msg := err.Error()
		if ctx.Err() != nil {
			msg = "Test abgebrochen"
		} else if rctx.Err() != nil {
			msg = fmt.Sprintf("Wiederherstellung dauerte länger als %d Minuten", plan.Limit)
		}
		set(func(r *RestoreTestResult) {
			r.Status, r.Phase, r.Info, r.Restore = "error", "", "Wiederherstellung fehlgeschlagen: "+msg, int64(time.Since(start).Seconds())
		})
		return
	}
	set(func(r *RestoreTestResult) {
		r.Restore, r.Phase = int64(time.Since(start).Seconds()), "trennt Netzwerk und Geräte"
	})
	// Mark it first, so a later sweep recognises it even if the rest fails.
	mark := prox.Params{"tags": rtTag, "onboot": false}
	if typ == "lxc" {
		mark["hostname"] = rtPrefix + snap.ID
	} else {
		mark["name"] = rtPrefix + snap.ID
	}
	_ = pve.SetGuestConfig(ctx, plan.Node, typ, testID, mark)

	cfg, err := pve.GuestConfig(ctx, plan.Node, typ, testID)
	if err != nil {
		failed = true
		set(func(r *RestoreTestResult) {
			r.Status, r.Phase, r.Info = "error", "", "Konfiguration nicht lesbar: "+err.Error()
		})
		return
	}
	params, notes := isolate(cfg, typ, snap.ID, plan, bridges)
	if err := pve.SetGuestConfig(ctx, plan.Node, typ, testID, params); err != nil {
		failed = true
		set(func(r *RestoreTestResult) {
			r.Status, r.Phase, r.Info = "error", "", "Isolieren fehlgeschlagen (Test nicht gestartet): "+err.Error()
		})
		return
	}
	set(func(r *RestoreTestResult) { r.Notes, r.Phase = strings.Join(notes, "; "), "startet" })

	boot := time.Now()
	if upid, err := pve.GuestAction(ctx, plan.Node, typ, testID, "start", nil); err != nil {
		failed = true
		set(func(r *RestoreTestResult) {
			r.Status, r.Phase, r.Info = "error", "", "Start fehlgeschlagen: "+err.Error()
		})
		return
	} else if _, err := pve.WaitTask(ctx, upid, 5*time.Minute); err != nil {
		failed = true
		set(func(r *RestoreTestResult) {
			r.Status, r.Phase, r.Info = "error", "", "Start fehlgeschlagen: "+err.Error()
		})
		return
	}
	agentOn := typ == "qemu" && agentEnabled(cfg)
	agentOK := false
	deadline := boot.Add(time.Duration(plan.BootWait) * time.Second)
	set(func(r *RestoreTestResult) { r.Phase = "wartet auf das Betriebssystem" })
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			failed = true
			set(func(r *RestoreTestResult) { r.Status, r.Phase, r.Info = "skipped", "", "Test abgebrochen" })
			return
		case <-time.After(5 * time.Second):
		}
		st, err := pve.GuestStatus(ctx, plan.Node, typ, testID)
		if err == nil && st.Status != "running" {
			failed = true
			set(func(r *RestoreTestResult) {
				r.Status, r.Phase, r.Boot = "error", "", int64(time.Since(boot).Seconds())
				r.Info = "Nach dem Start wieder ausgegangen (" + st.Status + ")"
			})
			return
		}
		if agentOn && !agentOK && pve.AgentPing(ctx, plan.Node, testID) == nil {
			agentOK = true
			osName, host := pve.AgentOSInfo(ctx, plan.Node, testID)
			set(func(r *RestoreTestResult) { r.OS, r.Hostname, r.Boot = osName, host, int64(time.Since(boot).Seconds()) })
			// give the desktop a moment for the screenshot, then stop waiting
			if d := time.Now().Add(20 * time.Second); d.Before(deadline) {
				deadline = d
			}
		}
	}
	var shot, blank = "", false
	if typ == "qemu" {
		set(func(r *RestoreTestResult) { r.Phase = "macht einen Screenshot" })
		sctx, scancel := context.WithTimeout(ctx, 45*time.Second)
		img, err := pve.Screenshot(sctx, plan.Node, testID)
		scancel()
		if err == nil {
			blank = prox.LooksBlank(img)
			name := snap.Type + "-" + snap.ID + ".png"
			path := c.ScreenshotPath(run.ID, name)
			if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
				if f, err := os.Create(path); err == nil {
					_ = png.Encode(f, img)
					f.Close()
					shot = name
				}
			}
		} else {
			log.Printf("restore test screenshot %d: %v", testID, err)
		}
	}
	set(func(r *RestoreTestResult) {
		r.Screenshot, r.Phase = shot, ""
		if r.Boot == 0 {
			r.Boot = int64(time.Since(boot).Seconds())
		}
		switch {
		case typ == "lxc":
			r.Status, r.Info = "ok", "Container läuft"
		case agentOK:
			r.Status = "ok"
			r.Info = "Betriebssystem antwortet"
			if r.OS != "" {
				r.Info += ": " + r.OS
			}
		case agentOn:
			r.Status, r.Info = "warning", "Gestartet, aber der Gast-Agent antwortet nicht – Screenshot ansehen"
			failed = true
		case shot != "" && blank:
			r.Status, r.Info = "warning", "Gestartet, der Bildschirm bleibt aber schwarz"
			failed = true
		case shot != "":
			r.Status, r.Info = "ok", "Gestartet, Bildschirm zeigt das System (ohne Gast-Agent)"
		default:
			r.Status, r.Info = "ok", "Gestartet und stabil gelaufen (ohne Gast-Agent, kein Screenshot)"
		}
	})
}

// isolate builds the config changes that keep a test guest away from production: network
// links down, passthrough devices and bind mounts removed, no autostart, limited resources.
func isolate(cfg map[string]any, typ, origID string, plan RestoreTestPlan, bridges []string) (prox.Params, []string) {
	p := prox.Params{}
	var del, notes []string
	bridgeOK := map[string]bool{}
	for _, b := range bridges {
		bridgeOK[b] = true
	}
	str := func(v any) string { return fmt.Sprint(v) }
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	devRe := regexp.MustCompile(`^(hostpci|usb|parallel|dev)\d+$`)
	netRe := regexp.MustCompile(`^net\d+$`)
	mpRe := regexp.MustCompile(`^mp\d+$`)
	diskRe := regexp.MustCompile(`^(ide|sata|scsi)\d+$`)
	for _, k := range keys {
		v := str(cfg[k])
		switch {
		case devRe.MatchString(k):
			del = append(del, k)
			notes = append(notes, k+" entfernt")
		case strings.HasPrefix(k, "serial") && v != "socket":
			del = append(del, k)
			notes = append(notes, k+" entfernt")
		case typ == "lxc" && mpRe.MatchString(k) && strings.HasPrefix(v, "/"):
			del = append(del, k)
			notes = append(notes, k+" (Bind-Mount) entfernt")
		case netRe.MatchString(k):
			parts := []string{}
			for _, x := range strings.Split(v, ",") {
				if !strings.HasPrefix(x, "link_down=") {
					if strings.HasPrefix(x, "bridge=") && len(bridges) > 0 && !bridgeOK[strings.TrimPrefix(x, "bridge=")] {
						notes = append(notes, k+": "+x+" fehlt, "+bridges[0]+" genutzt")
						x = "bridge=" + bridges[0]
					}
					parts = append(parts, x)
				}
			}
			p[k] = strings.Join(append(parts, "link_down=1"), ",")
		case typ == "qemu" && diskRe.MatchString(k) && strings.Contains(v, "media=cdrom") && !strings.HasPrefix(v, "none") && !strings.HasPrefix(v, "cdrom"):
			p[k] = "none,media=cdrom"
			notes = append(notes, "ISO in "+k+" entfernt")
		}
	}
	for _, k := range []string{"startup", "hookscript"} {
		if _, ok := cfg[k]; ok {
			del = append(del, k)
		}
	}
	p["onboot"] = false
	p["tags"] = rtTag
	p["description"] = "PK95 ProxBackup: Wiederherstellungstest von " + origID + ". Wird nach dem Test automatisch gelöscht."
	if typ == "lxc" {
		p["hostname"] = rtPrefix + origID
	} else {
		p["name"] = rtPrefix + origID
		p["sockets"] = 1
	}
	if plan.MaxCores > 0 {
		if cores, _ := strconv.Atoi(str(cfg["cores"])); cores == 0 || cores > plan.MaxCores {
			p["cores"] = plan.MaxCores
		}
		if v, ok := cfg["vcpus"]; ok {
			if n, _ := strconv.Atoi(str(v)); n > plan.MaxCores {
				p["vcpus"] = plan.MaxCores
			}
		}
	}
	if plan.MaxMem > 0 {
		if m, _ := strconv.Atoi(str(cfg["memory"])); m == 0 || m > plan.MaxMem {
			p["memory"] = plan.MaxMem
		}
		if b, ok := cfg["balloon"]; ok {
			if n, _ := strconv.Atoi(str(b)); n > plan.MaxMem {
				p["balloon"] = plan.MaxMem
			}
		}
	}
	if len(del) > 0 {
		p["delete"] = strings.Join(del, ",")
	}
	notes = append([]string{"Netzwerk getrennt"}, notes...)
	return p, notes
}

func agentEnabled(cfg map[string]any) bool {
	a := fmt.Sprint(cfg["agent"])
	return a == "1" || strings.HasPrefix(a, "1,") || strings.Contains(a, "enabled=1")
}

var sizeRe = regexp.MustCompile(`size=(\d+(?:\.\d+)?)([KMGT]?)`)

// guestNeeds sums the disk sizes and reads the memory of a stored guest config.
func guestNeeds(conf, typ string) (disk, mem int64) {
	diskKey := regexp.MustCompile(`^(ide|sata|scsi|virtio|efidisk|tpmstate)\d+$|^rootfs$|^mp\d+$`)
	for _, line := range strings.Split(conf, "\n") {
		if strings.HasPrefix(line, "[") {
			break
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "memory" {
			n, _ := strconv.ParseInt(v, 10, 64)
			mem = n << 20
		}
		if !diskKey.MatchString(k) || strings.Contains(v, "media=cdrom") || strings.HasPrefix(v, "/") {
			continue
		}
		if m := sizeRe.FindStringSubmatch(v); m != nil {
			f, _ := strconv.ParseFloat(m[1], 64)
			mult := map[string]float64{"": 1, "K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}[m[2]]
			disk += int64(f * mult)
		}
	}
	if mem == 0 && typ == "lxc" {
		mem = 512 << 20
	}
	return
}

// freeTestID finds the lowest unused VMID from base on.
func (c *Core) freeTestID(ctx context.Context, pve *prox.PVE, base int) (int, error) {
	guests, err := pve.Guests(ctx)
	if err != nil {
		return 0, err
	}
	used := map[int]bool{}
	for _, g := range guests {
		used[g.VMID] = true
	}
	for id := base; id < base+100000; id++ {
		if !used[id] {
			return id, nil
		}
	}
	return 0, errors.New("keine freie Test-ID gefunden")
}

// destroyTestGuest stops and deletes a test guest. owned: this run created it (it may not be
// tagged yet when isolating failed); otherwise it must carry our tag and name prefix.
func (c *Core) destroyTestGuest(ctx context.Context, pve *prox.PVE, node, typ string, vmid int, owned bool) error {
	cfg, err := pve.GuestConfig(ctx, node, typ, vmid)
	if err != nil {
		return err
	}
	name := fmt.Sprint(cfg["name"])
	if typ == "lxc" {
		name = fmt.Sprint(cfg["hostname"])
	}
	if !owned && (!strings.Contains(fmt.Sprint(cfg["tags"]), rtTag) || !strings.HasPrefix(name, rtPrefix)) {
		return fmt.Errorf("Gast %d ist nicht als Test markiert, wird nicht gelöscht", vmid)
	}
	if st, err := pve.GuestStatus(ctx, node, typ, vmid); err == nil && st.Status == "running" {
		if upid, err := pve.GuestAction(ctx, node, typ, vmid, "stop", nil); err == nil {
			_, _ = pve.WaitTask(ctx, upid, 3*time.Minute)
		}
	}
	upid, err := pve.DestroyGuest(ctx, node, typ, vmid)
	if err != nil {
		return err
	}
	_, err = pve.WaitTask(ctx, upid, 10*time.Minute)
	return err
}

// sweepRestoreTests removes test guests left over by a crash or power loss.
func (c *Core) sweepRestoreTests(ctx context.Context, pve *prox.PVE) {
	guests, err := pve.Guests(ctx)
	if err != nil {
		return
	}
	for _, g := range guests {
		if strings.Contains(g.Tags, rtTag) && strings.HasPrefix(g.Name, rtPrefix) {
			if err := c.destroyTestGuest(ctx, pve, g.Node, g.Type, g.VMID, false); err != nil {
				log.Printf("restore test sweep %d: %v", g.VMID, err)
			} else {
				c.Event("info", "Wiederherstellungstest", fmt.Sprintf("Übrig gebliebenen Test-Gast %d entfernt", g.VMID), g.Name)
			}
		}
	}
}

func (c *Core) finishRun(plan RestoreTestPlan, cl PVECluster, run *RestoreTestRun, cancelled bool) {
	var rows []notify.Row
	okN, warnN, errN, skipN := 0, 0, 0, 0
	c.rtUpdate(run, func() {
		run.End = time.Now().Unix()
		for _, r := range run.Results {
			switch r.Status {
			case "ok":
				okN++
			case "warning":
				warnN++
			case "error":
				errN++
			default:
				skipN++
			}
		}
		switch {
		case cancelled:
			run.Status = "cancelled"
		case errN > 0:
			run.Status = "error"
		case warnN > 0 || skipN > 0:
			run.Status = "warning"
		default:
			run.Status = "ok"
		}
	})
	for _, r := range run.Results {
		st := r.Status
		if st == "skipped" {
			st = "warning"
		}
		kind := "VM"
		if r.Type == "ct" {
			kind = "CT"
		}
		info := r.Info
		rows = append(rows, notify.Row{Kind: kind, ID: r.VMID, Name: r.Name, Status: st, Time: r.BackupTime,
			Duration: humanDuration(r.Restore + r.Boot), Size: fmtBytes(r.DiskBytes), Info: info})
	}
	total := len(run.Results)
	d := &notify.Details{RowsTitle: "Getestete Server", Rows: rows, Link: "/restoretests",
		Facts: []notify.Fact{{Label: "System", Value: cl.Name}, {Label: "Getestet auf", Value: plan.Node + " / " + plan.Storage},
			{Label: "Sicherungen aus", Value: plan.Store}, {Label: "Beginn", Value: fmtWhen(run.Start)}, {Label: "Dauer", Value: humanDuration(run.End - run.Start)}}}
	sev, title, body := "info", "", ""
	forward := " Bitte leiten Sie diese Nachricht an Ihren IT-Betreuer weiter."
	switch {
	case cancelled:
		sev, d.Status = "warning", "warning"
		title = "Wiederherstellungstest abgebrochen"
		body = fmt.Sprintf("Der Test wurde abgebrochen. %d von %d Servern wurden bis dahin erfolgreich getestet.", okN, total)
	case errN == 0 && warnN == 0 && skipN == 0:
		d.Status = "ok"
		title = fmt.Sprintf("Wiederherstellungstest erfolgreich – %s", allStart(okN))
		body = fmt.Sprintf("Alle %d Server wurden testweise aus der Sicherung wiederhergestellt und gestartet. Ihre Sicherungen sind einsatzbereit. Sie müssen nichts tun.", okN)
	case errN > 0:
		sev, d.Status = "critical", "error"
		title = fmt.Sprintf("Wiederherstellungstest: %d von %d Servern ohne Probleme", okN, total)
		body = fmt.Sprintf("Bei %d von %d Servern ist die testweise Wiederherstellung fehlgeschlagen.", errN, total) + forward
	default:
		sev, d.Status = "warning", "warning"
		title = fmt.Sprintf("Wiederherstellungstest: %d von %d Servern ohne Probleme", okN, total)
		body = fmt.Sprintf("%d von %d Servern ließen sich einwandfrei wiederherstellen und starten, bei den übrigen gab es Hinweise.", okN, total) + forward
	}
	c.EventDetails(sev, "Wiederherstellungstest", title, body, d)
}

func allStart(n int) string {
	if n == 1 {
		return "der Server lässt sich aus der Sicherung starten"
	}
	return fmt.Sprintf("alle %d Server lassen sich aus der Sicherung starten", n)
}

// scheduleRestoreTests starts plans whose weekday and time have come (called every minute).
func (c *Core) scheduleRestoreTests(now time.Time) {
	plans, _ := c.RestoreTestPlans()
	wd := int(now.Weekday())
	if wd == 0 {
		wd = 7
	}
	for _, p := range plans {
		if !p.Enabled || p.Time == "" || now.Format("15:04") < p.Time {
			continue
		}
		today := false
		for _, d := range p.Weekdays {
			if d == wd {
				today = true
			}
		}
		if !today {
			continue
		}
		key := "rtlast:" + p.ID
		var last string
		_ = c.St.GetSetting(key, &last)
		if last == now.Format("2006-01-02") {
			continue
		}
		if _, err := c.StartRestoreTest(p.ID, "Zeitplan"); err != nil {
			continue // another test is running: try again next minute
		}
		_ = c.St.SetSetting(key, now.Format("2006-01-02"))
	}
}
