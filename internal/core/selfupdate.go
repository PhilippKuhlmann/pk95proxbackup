package core

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Automatic updates of the appliance itself: at the planned time it looks for a new version
// and installs it – but not while it follows work a restart would interrupt (USB copies and
// their aftercare, archive copies, restore tests, system updates of a Backup Server). It
// waits for that up to the end of the window (AutoWindow) and otherwise tries at the next
// planned time. A new version that does not start is rolled back by UpdateGuard.

const (
	selfAutoKey = "selfupd"
	selfRunKey  = "selfupd-run"
	// selfRecheck: within the window GitHub is asked again at most this often.
	selfRecheck = 30 * time.Minute
)

// selfRunState is what the appliance did at the current planned time.
type selfRunState struct {
	Day       string   `json:"day"`
	Phase     string   `json:"phase"` // wait, done
	Checked   int64    `json:"checked,omitempty"`
	Busy      []string `json:"busy,omitempty"`
	Postponed string   `json:"postponed,omitempty"` // why the last planned update did not happen
	Installed string   `json:"installed,omitempty"` // version installed at the last planned time
}

// SelfAutoUpdateInfo is the plan and its state for the settings page.
type SelfAutoUpdateInfo struct {
	AutoUpdate
	Next      int64    `json:"next,omitempty"`
	Waiting   []string `json:"waiting,omitempty"` // work the planned update waits for
	Postponed string   `json:"postponed,omitempty"`
}

// SelfAutoUpdate returns the plan (off by default: every day at 05:00).
func (c *Core) SelfAutoUpdate() AutoUpdate {
	a := AutoUpdate{Weekdays: []int{1, 2, 3, 4, 5, 6, 7}, Time: "05:00"}
	_ = c.St.GetSetting(selfAutoKey, &a)
	if a.Weekdays == nil {
		a.Weekdays = []int{}
	}
	a.Reboot = false
	return a
}

// SelfAutoUpdateState returns plan, next planned time and what the update waits for.
func (c *Core) SelfAutoUpdateState(now time.Time) SelfAutoUpdateInfo {
	a := c.SelfAutoUpdate()
	info := SelfAutoUpdateInfo{AutoUpdate: a}
	if t, ok := a.next(now); ok {
		info.Next = t.Unix()
	}
	var r selfRunState
	_ = c.St.GetSetting(selfRunKey, &r)
	if r.Phase == "wait" {
		info.Waiting = r.Busy
	}
	info.Postponed = r.Postponed
	return info
}

// SetSelfAutoUpdate checks and stores the plan; field errors are keyed like the form.
func (c *Core) SetSelfAutoUpdate(a AutoUpdate) (map[string]string, error) {
	errs := map[string]string{}
	days := []int{}
	for _, d := range a.Weekdays {
		if d >= 1 && d <= 7 && !slices.Contains(days, d) {
			days = append(days, d)
		}
	}
	slices.Sort(days)
	a.Weekdays, a.Reboot = days, false
	if !hhmm.MatchString(a.Time) {
		errs["time"] = "Uhrzeit wie 05:00."
	}
	if a.Enabled && len(days) == 0 {
		errs["weekdays"] = "Mindestens einen Tag wählen."
	}
	if ok, why := c.canSelfUpdate(); a.Enabled && !ok {
		errs["enabled"] = why
	}
	if len(errs) > 0 {
		return errs, nil
	}
	return nil, c.St.SetSetting(selfAutoKey, a)
}

func (c *Core) scheduleSelfUpdate(now time.Time) {
	if !c.SelfAutoUpdate().Enabled || !c.update.auto.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer c.update.auto.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		c.RunSelfAutoUpdate(ctx, now)
	}()
}

// RunSelfAutoUpdate advances the planned self-update (every minute).
func (c *Core) RunSelfAutoUpdate(ctx context.Context, now time.Time) {
	a := c.SelfAutoUpdate()
	start, in := a.window(now)
	var r selfRunState
	_ = c.St.GetSetting(selfRunKey, &r)
	save := func() { _ = c.St.SetSetting(selfRunKey, r) }
	if day := start.Format("2006-01-02"); in && r.Day != day {
		r = selfRunState{Day: day, Phase: "wait", Postponed: r.Postponed}
	}
	if r.Phase != "wait" {
		return
	}
	if !in { // the window ended while the appliance waited for work to finish
		r.Phase = "done"
		r.Postponed = fmt.Sprintf("Am %s lief bis zum Ende des Zeitfensters noch: %s.", dayText(r.Day), strings.Join(r.Busy, ", "))
		save()
		c.Event("warning", "Appliance", "Update der Appliance verschoben",
			r.Postponed+" Das Update folgt zum nächsten geplanten Termin – oder jetzt per Klick unter Einstellungen → Aktualisierung.")
		return
	}
	info := c.Update()
	if r.Checked == 0 || now.Sub(time.Unix(r.Checked, 0)) >= selfRecheck {
		r.Checked = now.Unix()
		var err error
		if info, err = c.CheckUpdate(ctx); err != nil {
			save() // GitHub not reachable: ask again in half an hour, within the window
			return
		}
	}
	if !info.Available {
		r.Phase, r.Busy, r.Postponed = "done", nil, ""
		save()
		return
	}
	if !info.CanInstall {
		r.Phase, r.Postponed = "done", info.Reason
		save()
		return
	}
	if busy := c.BusyWork(); len(busy) > 0 {
		r.Busy = busy
		save()
		return
	}
	r.Phase, r.Busy, r.Postponed, r.Installed = "done", nil, "", info.Latest
	save()
	c.Event("info", "Appliance", "Update auf "+info.Latest+" wird automatisch installiert",
		"Geplantes Update (Einstellungen → Aktualisierung). Die Appliance startet dabei neu, Sicherungen in Proxmox VE laufen weiter.")
	if err := c.InstallUpdate(); err != nil {
		c.Event("warning", "Appliance", "Automatisches Update auf "+info.Latest+" nicht gestartet", err.Error())
	}
}

func dayText(day string) string {
	if t, err := time.Parse("2006-01-02", day); err == nil {
		return t.Format("02.01.2006")
	}
	return day
}
