package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"pk95proxbackup/internal/notify"
)

// System updates of a Backup Server: the Debian and Proxmox packages are upgraded with apt in a
// transient systemd unit, so the run goes on when the SSH connection drops or the appliance's
// own service restarts. The appliance follows the unit, keeps the end of the log and reports
// the result. A reboot (new kernel) is started on request.

const (
	upgradeUnit = "pk95-apt-upgrade"
	upgradeLog  = "/var/log/pk95-apt-upgrade.log"
	upgradeRC   = "/var/log/pk95-apt-upgrade.rc"
	// upgradeGrace is how long a started unit may not yet show up as active.
	upgradeGrace = 30 * time.Second
	upgradeMax   = 3 * time.Hour
)

// UpgradeScript is what the unit runs: refresh the lists, then upgrade everything without
// questions (keep changed configuration files), and write the exit code.
const UpgradeScript = `exec >` + upgradeLog + ` 2>&1
echo "== apt-get update"
apt-get -q -o DPkg::Lock::Timeout=600 update && {
echo "== apt-get dist-upgrade"
apt-get -q -y -o DPkg::Lock::Timeout=600 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold dist-upgrade
}
echo $? > ` + upgradeRC

// UpgradeStatusScript prints the unit state, the exit code (if done) and the end of the log.
const UpgradeStatusScript = `systemctl is-active ` + upgradeUnit + `; echo "--rc"; cat ` + upgradeRC + ` 2>/dev/null; echo "--log"; tail -n 60 ` + upgradeLog + ` 2>/dev/null; true`

// ErrBusy means work is running that a reboot would interrupt.
var ErrBusy = errors.New("es läuft gerade noch etwas")

// SysUpgrade is the last (or running) package upgrade of a Backup Server.
type SysUpgrade struct {
	State    string   `json:"state"` // running, ok, failed ("" = never)
	Started  int64    `json:"started"`
	Finished int64    `json:"finished,omitempty"`
	By       string   `json:"by"`
	Upgraded int      `json:"upgraded"` // packages upgraded or newly installed
	Log      []string `json:"log"`
	Error    string   `json:"error,omitempty"`
}

// SysUpdateView is one Backup Server on the update page.
type SysUpdateView struct {
	ServerID string        `json:"server_id"`
	Name     string        `json:"name"`
	Local    bool          `json:"local"`
	Shell    bool          `json:"shell"` // packages can be upgraded from here
	Health   *ServerHealth `json:"health"`
	Upgrade  SysUpgrade    `json:"upgrade"`
	Auto     AutoUpdate    `json:"auto"`
	Next     int64         `json:"next,omitempty"` // next automatic run
	// Postponed is set when the last automatic reboot could not be done.
	Postponed string `json:"postponed,omitempty"`
}

type upgradeRuntime struct {
	mu        sync.Mutex // one start or status read at a time (a run is finished once)
	following sync.Map   // server id → true while a goroutine follows the unit
	autoBusy  atomic.Bool
}

// scheduleAutoUpdates runs the automatic updates in the background, one pass at a time.
func (c *Core) scheduleAutoUpdates(now time.Time) {
	if !c.upgrades.autoBusy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer c.upgrades.autoBusy.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		c.RunAutoUpdates(ctx, now)
	}()
}

func upgradeKey(id string) string { return "sysupd:" + id }

func (c *Core) upgradeState(id string) SysUpgrade {
	var u SysUpgrade
	_ = c.St.GetSetting(upgradeKey(id), &u)
	if u.Log == nil {
		u.Log = []string{}
	}
	return u
}

// SystemUpdates lists the Backup Servers with their software state and last upgrade.
func (c *Core) SystemUpdates() []SysUpdateView {
	servers, _ := c.Servers()
	health := map[string]ServerHealth{}
	for _, h := range c.ServerHealths() {
		health[h.ServerID] = h
	}
	out := []SysUpdateView{}
	for _, s := range servers {
		v := SysUpdateView{ServerID: s.ID, Name: s.Name, Local: s.Local, Shell: s.Local || s.SSHEnabled, Upgrade: c.upgradeState(s.ID), Auto: c.AutoUpdate(s.ID)}
		if v.Auto.Enabled {
			if t, ok := v.Auto.next(time.Now()); ok {
				v.Next = t.Unix()
			}
		}
		v.Postponed = c.autoRun(s.ID).Postponed
		if h, ok := health[s.ID]; ok {
			v.Health = &h
		}
		out = append(out, v)
	}
	return out
}

// RefreshPackages lets the Backup Server read its package lists again and checks its state.
func (c *Core) RefreshPackages(ctx context.Context, id string) (ServerHealth, error) {
	pbs, _, err := c.PBS(id)
	if err != nil {
		return ServerHealth{}, err
	}
	if err := pbs.RefreshPackages(ctx); err != nil {
		return ServerHealth{}, fmt.Errorf("Paketlisten nicht aktualisiert: %w", err)
	}
	return c.CheckServerHealth(ctx, id)
}

// StartUpgrade starts the package upgrade of a Backup Server in the background.
func (c *Core) StartUpgrade(ctx context.Context, id, by string) (SysUpgrade, error) {
	c.upgrades.mu.Lock()
	defer c.upgrades.mu.Unlock()
	srv, err := c.Server(id)
	if err != nil {
		return SysUpgrade{}, err
	}
	run, err := c.Runner(srv)
	if err != nil {
		return SysUpgrade{}, err
	}
	if out, _ := run.Run(ctx, "systemctl", "is-active", upgradeUnit); isActive(out) {
		return SysUpgrade{}, errors.New("auf diesem Server werden gerade schon Updates eingespielt")
	}
	_, _ = run.Run(ctx, "systemctl", "reset-failed", upgradeUnit)
	if _, err := run.Run(ctx, "rm", "-f", upgradeRC); err != nil {
		return SysUpgrade{}, err
	}
	if _, err := run.Run(ctx, "systemd-run", "--unit="+upgradeUnit, "--description=PK95 ProxBackup: Updates einspielen",
		"--setenv=DEBIAN_FRONTEND=noninteractive", "--setenv=APT_LISTCHANGES_FRONTEND=none",
		"/bin/sh", "-c", UpgradeScript); err != nil {
		return SysUpgrade{}, fmt.Errorf("Updates nicht gestartet: %w", err)
	}
	u := SysUpgrade{State: "running", Started: time.Now().Unix(), By: by, Log: []string{}}
	_ = c.St.SetSetting(upgradeKey(id), u)
	c.Event("info", srv.Name, "Updates werden eingespielt", "Debian- und Proxmox-Pakete werden aktualisiert (von "+by+"). Laufende Sicherungen arbeiten weiter.")
	go c.followUpgrade(id)
	return u, nil
}

func isActive(out string) bool {
	s := strings.TrimSpace(out)
	return s == "active" || s == "activating" || s == "reloading"
}

// UpgradePoll is how often a running upgrade is looked at (tests shorten it).
var UpgradePoll = 5 * time.Second

// followUpgrade watches a running upgrade until it ends (one goroutine per server).
func (c *Core) followUpgrade(id string) {
	if _, busy := c.upgrades.following.LoadOrStore(id, true); busy {
		return
	}
	defer c.upgrades.following.Delete(id)
	for {
		time.Sleep(UpgradePoll)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		u, err := c.UpgradeStatus(ctx, id)
		cancel()
		if errors.Is(err, errServerGone) || (err == nil && u.State != "running") {
			return
		}
	}
}

var errServerGone = errors.New("Backup-Server nicht mehr vorhanden")

var upgradedRe = regexp.MustCompile(`(\d+) upgraded, (\d+) newly installed`)

// UpgradeStatus reads the state of the upgrade of a Backup Server; a finished run is
// recorded and reported once.
func (c *Core) UpgradeStatus(ctx context.Context, id string) (SysUpgrade, error) {
	c.upgrades.mu.Lock()
	defer c.upgrades.mu.Unlock()
	u := c.upgradeState(id)
	if u.State != "running" {
		return u, nil
	}
	srv, err := c.Server(id)
	if err != nil {
		return u, errServerGone
	}
	run, err := c.Runner(srv)
	if err != nil {
		return u, err
	}
	out, err := run.Run(ctx, "sh", "-c", UpgradeStatusScript)
	if err != nil {
		// not reachable (e.g. while it restarts): try again later
		if time.Since(time.Unix(u.Started, 0)) > upgradeMax {
			return c.finishUpgrade(ctx, srv, u, "", "Keine Rückmeldung mehr vom Server: "+err.Error())
		}
		return u, nil
	}
	state, rest, _ := strings.Cut(out, "--rc")
	rc, logText, _ := strings.Cut(rest, "--log")
	state, rc = strings.TrimSpace(state), strings.TrimSpace(rc)
	u.Log = tailLines(logText, 40)
	if isActive(state) || (rc == "" && time.Since(time.Unix(u.Started, 0)) < upgradeGrace) {
		_ = c.St.SetSetting(upgradeKey(id), u)
		return u, nil
	}
	if rc == "" {
		return c.finishUpgrade(ctx, srv, u, "", "Der Vorgang wurde abgebrochen (kein Ergebnis).")
	}
	return c.finishUpgrade(ctx, srv, u, rc, "")
}

func tailLines(s string, n int) []string {
	out := []string{}
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r", "\n"), "\n") {
		if l = strings.TrimRight(l, " \t"); l != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

func (c *Core) finishUpgrade(ctx context.Context, srv PBSServer, u SysUpgrade, rc, failure string) (SysUpgrade, error) {
	u.Finished = time.Now().Unix()
	for _, l := range u.Log {
		if m := upgradedRe.FindStringSubmatch(l); m != nil {
			a, _ := strconv.Atoi(m[1])
			b, _ := strconv.Atoi(m[2])
			u.Upgraded = a + b
		}
	}
	if failure == "" && rc != "0" {
		failure = "apt beendete sich mit Fehler " + rc + "."
		for i := len(u.Log) - 1; i >= 0; i-- {
			if strings.HasPrefix(u.Log[i], "E: ") {
				failure = strings.TrimPrefix(u.Log[i], "E: ")
				break
			}
		}
	}
	if failure != "" {
		u.State, u.Error = "failed", failure
	} else {
		u.State = "ok"
	}
	_ = c.St.SetSetting(upgradeKey(srv.ID), u)
	// the package state has changed: read it again (this also reports a needed reboot)
	h, _ := c.RefreshPackages(ctx, srv.ID)
	if u.State == "failed" {
		c.EventDetails("error", srv.Name, "Updates nicht vollständig eingespielt", failure,
			&notify.Details{Status: "error", Link: "/einstellungen/aktualisierung", Log: lastN(u.Log, 15)})
		return u, nil
	}
	body := fmt.Sprintf("%d Pakete aktualisiert.", u.Upgraded)
	if u.Upgraded == 0 {
		body = "Es gab nichts zu aktualisieren."
	}
	if h.RebootNeeded {
		body += " Ein Neustart ist nötig, damit der neue Kernel läuft."
	} else if h.RestartNeeded {
		body += " Die Dienste des Backup-Servers sollten neu geladen werden."
	}
	c.EventDetails("info", srv.Name, "Updates eingespielt", body, &notify.Details{Status: "ok", Link: "/einstellungen/aktualisierung"})
	return u, nil
}

func lastN(l []string, n int) []string {
	if len(l) > n {
		return l[len(l)-n:]
	}
	return l
}

// resumeUpgrades follows upgrades that were still running when the appliance started.
func (c *Core) resumeUpgrades() {
	servers, _ := c.Servers()
	for _, s := range servers {
		if c.upgradeState(s.ID).State == "running" {
			go c.followUpgrade(s.ID)
		}
	}
}

// RebootServer restarts the machine of a Backup Server. Unless forced, it refuses while
// tasks run on it (or, for this appliance, its own work) and lists them.
func (c *Core) RebootServer(ctx context.Context, id, by string, force bool) ([]string, error) {
	pbs, srv, err := c.PBS(id)
	if err != nil {
		return nil, err
	}
	if c.upgradeState(id).State == "running" {
		return nil, errors.New("es werden gerade Updates eingespielt – erst danach neu starten")
	}
	busy := []string{}
	if ts, err := pbs.RunningTasks(ctx); err == nil {
		for _, t := range ts {
			busy = append(busy, strings.TrimSpace(TaskLabel(t.Type)+" "+unescapeWorkerID(t.WorkerID)))
		}
	}
	if srv.Local {
		busy = append(busy, c.BusyWork()...)
	}
	if len(busy) > 0 && !force {
		return busy, ErrBusy
	}
	what := "Der Backup-Server startet neu"
	if srv.Local {
		what = "Die Appliance startet neu"
	}
	c.Event("warning", srv.Name, "Neustart", what+" (von "+by+"). Er ist in ein bis zwei Minuten wieder erreichbar.")
	if err := pbs.Reboot(ctx); err != nil {
		return nil, fmt.Errorf("Neustart nicht ausgelöst: %w", err)
	}
	return nil, nil
}

// ---- automatic updates ----

// AutoWindow is how long after the planned time the appliance may still start the run or
// the reboot (it waits for running backups to end).
const AutoWindow = 3 * time.Hour

// AutoUpdate is the plan for automatic updates of a Backup Server.
type AutoUpdate struct {
	Enabled  bool   `json:"enabled"`
	Weekdays []int  `json:"weekdays"` // 1 = Monday … 7 = Sunday
	Time     string `json:"time"`     // "04:30"
	Reboot   bool   `json:"reboot"`   // reboot afterwards when a new kernel needs it
}

// autoRunState is what the appliance did at the current planned time.
type autoRunState struct {
	Day       string   `json:"day"`   // date of the run, 2026-11-15
	Phase     string   `json:"phase"` // check, upgrading, reboot, done
	Busy      []string `json:"busy,omitempty"`
	Postponed string   `json:"postponed,omitempty"` // why the last reboot did not happen
}

func autoKey(id string) string    { return "autoupd:" + id }
func autoRunKey(id string) string { return "autorun:" + id }

// AutoUpdate returns the plan of a Backup Server (off by default: Saturday 04:30 with reboot).
func (c *Core) AutoUpdate(id string) AutoUpdate {
	a := AutoUpdate{Weekdays: []int{6}, Time: "04:30", Reboot: true}
	_ = c.St.GetSetting(autoKey(id), &a)
	if a.Weekdays == nil {
		a.Weekdays = []int{}
	}
	return a
}

func (c *Core) autoRun(id string) autoRunState {
	var r autoRunState
	_ = c.St.GetSetting(autoRunKey(id), &r)
	return r
}

// SetAutoUpdate checks and stores the plan; field errors are keyed like the form.
func (c *Core) SetAutoUpdate(id string, a AutoUpdate) (map[string]string, error) {
	srv, err := c.Server(id)
	if err != nil {
		return nil, err
	}
	errs := map[string]string{}
	days := []int{}
	for _, d := range a.Weekdays {
		if d >= 1 && d <= 7 && !slices.Contains(days, d) {
			days = append(days, d)
		}
	}
	a.Weekdays = days
	if !hhmm.MatchString(a.Time) {
		errs["time"] = "Uhrzeit wie 04:30."
	}
	if a.Enabled && len(days) == 0 {
		errs["weekdays"] = "Mindestens einen Tag wählen."
	}
	if a.Enabled && !srv.Local && !srv.SSHEnabled {
		errs["enabled"] = "Dafür braucht die Appliance den SSH-Zugang zu diesem Server (unter Verbindungen)."
	}
	if len(errs) > 0 {
		return errs, nil
	}
	return nil, c.St.SetSetting(autoKey(id), a)
}

// at is the planned time on the day of t.
func (a AutoUpdate) at(t time.Time) time.Time {
	h, _ := strconv.Atoi(a.Time[:2])
	m, _ := strconv.Atoi(a.Time[3:])
	return time.Date(t.Year(), t.Month(), t.Day(), h, m, 0, 0, t.Location())
}

// window reports whether now lies in the window of a planned day, and that day's start.
func (a AutoUpdate) window(now time.Time) (time.Time, bool) {
	if !a.Enabled || !hhmm.MatchString(a.Time) {
		return time.Time{}, false
	}
	for _, back := range []int{0, -1} { // the window may reach past midnight
		day := now.AddDate(0, 0, back)
		start := a.at(day)
		if slices.Contains(a.Weekdays, isoWeekday(day)) && !now.Before(start) && now.Before(start.Add(AutoWindow)) {
			return start, true
		}
	}
	return time.Time{}, false
}

// next is the next planned time after now.
func (a AutoUpdate) next(now time.Time) (time.Time, bool) {
	if !a.Enabled || !hhmm.MatchString(a.Time) || len(a.Weekdays) == 0 {
		return time.Time{}, false
	}
	for i := 0; i <= 7; i++ {
		day := now.AddDate(0, 0, i)
		if t := a.at(day); slices.Contains(a.Weekdays, isoWeekday(day)) && t.After(now) {
			return t, true
		}
	}
	return time.Time{}, false
}

// AutoUpdateBy is the name automatic runs carry in events and the update history.
const AutoUpdateBy = "automatisch"

// RunAutoUpdates advances the automatic updates of all Backup Servers (every minute):
// at the planned time look for updates and install them, then reboot if a new kernel needs
// it – but not while work runs; that is tried again until the window ends.
func (c *Core) RunAutoUpdates(ctx context.Context, now time.Time) {
	servers, _ := c.Servers()
	for _, s := range servers {
		a := c.AutoUpdate(s.ID)
		start, in := a.window(now)
		r := c.autoRun(s.ID)
		day := start.Format("2006-01-02")
		if in && r.Day != day {
			r = autoRunState{Day: day, Phase: "check", Postponed: r.Postponed}
		}
		if r.Phase == "" || r.Phase == "done" {
			continue
		}
		c.stepAutoUpdate(ctx, s, a, &r, in)
		_ = c.St.SetSetting(autoRunKey(s.ID), r)
	}
}

func (c *Core) stepAutoUpdate(ctx context.Context, s PBSServer, a AutoUpdate, r *autoRunState, in bool) {
	switch r.Phase {
	case "check":
		h, err := c.RefreshPackages(ctx, s.ID)
		switch {
		case err != nil:
			c.Event("error", s.Name, "Automatische Updates ausgefallen", "Paketlisten nicht geladen: "+err.Error())
			r.Phase = "done"
		case h.Updates > 0:
			_ = c.St.SetSetting(autoRunKey(s.ID), autoRunState{Day: r.Day, Phase: "upgrading", Postponed: r.Postponed})
			if _, err := c.StartUpgrade(ctx, s.ID, AutoUpdateBy); err != nil {
				c.Event("error", s.Name, "Automatische Updates nicht gestartet", err.Error())
				r.Phase = "done"
				return
			}
			r.Phase = "upgrading"
		case a.Reboot && h.RebootNeeded:
			r.Phase = "reboot"
		default:
			r.Phase = "done"
		}
	case "upgrading":
		u := c.upgradeState(s.ID)
		if u.State == "running" {
			return
		}
		r.Phase = "done"
		if u.State != "ok" {
			return
		}
		var h ServerHealth
		_ = c.St.GetSetting("srvhealth:"+s.ID, &h)
		if h.RestartNeeded && !(a.Reboot && h.RebootNeeded) {
			_, _ = c.ReloadServerServices(ctx, s.ID)
		}
		if a.Reboot && h.RebootNeeded {
			r.Phase = "reboot"
		}
	case "reboot":
		if !in {
			why := "Das Einspielen dauerte bis zum Ende des Zeitfensters."
			if len(r.Busy) > 0 {
				why = "Bis zum Ende des Zeitfensters lief noch: " + strings.Join(r.Busy, ", ") + "."
			}
			r.Phase, r.Postponed = "done", why
			c.EventDetails("warning", s.Name, "Automatischer Neustart verschoben",
				why+" Neuer Versuch beim nächsten Termin; unter Einstellungen → Aktualisierung lässt er sich auch von Hand auslösen.",
				&notify.Details{Status: "warning", Link: "/einstellungen/aktualisierung"})
			return
		}
		// stored as done first: a reboot of the appliance itself ends this program
		_ = c.St.SetSetting(autoRunKey(s.ID), autoRunState{Day: r.Day, Phase: "done"})
		busy, err := c.RebootServer(ctx, s.ID, AutoUpdateBy, false)
		if errors.Is(err, ErrBusy) {
			r.Busy = busy
			return
		}
		r.Phase, r.Busy, r.Postponed = "done", nil, ""
		if err != nil {
			c.Event("error", s.Name, "Automatischer Neustart fehlgeschlagen", err.Error())
		}
	}
}
