package core

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/notify"
)

// Swap plan: each disk of a set can be assigned to weekdays, optionally only in certain weeks
// (the n-th of that weekday in the month, or odd/even calendar weeks). A common scheme is one
// disk each for Monday to Thursday and five Friday disks, one for each Friday of the month.
// With CheckPlan set, the appliance reports a wrong disk.

// PlanWeeks are the allowed values of RotDisk.Week.
var PlanWeeks = map[string]string{"": "jede Woche", "1": "1.", "2": "2.", "3": "3.", "4": "4.", "5": "5.", "odd": "ungerade KW", "even": "gerade KW"}

// HasPlan reports whether any disk of the set is assigned to days.
func (r Rotation) HasPlan() bool {
	for _, d := range r.Disks {
		if len(d.Days) > 0 {
			return true
		}
	}
	return false
}

func isoWeekday(t time.Time) int {
	wd := int(t.Weekday())
	if wd == 0 {
		return 7
	}
	return wd
}

// PlannedOn reports whether the disk is planned for the day of t.
func (d RotDisk) PlannedOn(t time.Time) bool {
	wd := isoWeekday(t)
	hit := false
	for _, x := range d.Days {
		if x == wd {
			hit = true
		}
	}
	if !hit {
		return false
	}
	switch d.Week {
	case "":
		return true
	case "odd", "even":
		_, w := t.ISOWeek()
		return (w%2 == 1) == (d.Week == "odd")
	}
	n, err := strconv.Atoi(d.Week)
	return err == nil && (t.Day()-1)/7+1 == n
}

// PlannedDisks returns the disks planned for the day of t.
func (r Rotation) PlannedDisks(t time.Time) []RotDisk {
	var out []RotDisk
	for _, d := range r.Disks {
		if d.PlannedOn(t) {
			out = append(out, d)
		}
	}
	return out
}

// PlanText describes when a disk is planned ("Montag", "1. Freitag im Monat").
func (d RotDisk) PlanText() string {
	if len(d.Days) == 0 {
		return ""
	}
	var names []string
	for _, x := range d.Days {
		if x >= 1 && x <= 7 {
			names = append(names, weekdays[x])
		}
	}
	days := strings.Join(names, ", ")
	if len(d.Days) == 5 && !contains(d.Days, 6) && !contains(d.Days, 7) {
		days = "Montag bis Freitag"
	}
	switch d.Week {
	case "":
		return days
	case "odd":
		return days + " in ungeraden Kalenderwochen"
	case "even":
		return days + " in geraden Kalenderwochen"
	}
	return d.Week + ". " + days + " im Monat"
}

func contains(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// ValidatePlan checks the assignment: valid days and weeks, and no day with two disks.
func (r *Rotation) ValidatePlan() string {
	for i := range r.Disks {
		d := &r.Disks[i]
		seen := map[int]bool{}
		var days []int
		for _, x := range d.Days {
			if x < 1 || x > 7 {
				return "Ungültiger Wochentag bei „" + d.Label + "“."
			}
			if !seen[x] {
				seen[x] = true
				days = append(days, x)
			}
		}
		d.Days = days
		if _, ok := PlanWeeks[d.Week]; !ok {
			return "Ungültige Woche bei „" + d.Label + "“."
		}
		if len(d.Days) == 0 {
			d.Week = ""
		}
	}
	// Two disks on one day? Try a whole year from a Monday on.
	day := time.Date(2026, 1, 5, 12, 0, 0, 0, time.Local)
	for i := 0; i < 371; i++ {
		t := day.AddDate(0, 0, i)
		if p := r.PlannedDisks(t); len(p) > 1 {
			return fmt.Sprintf("„%s“ und „%s“ sind beide für %s eingeplant (z. B. am %s).", p[0].Label, p[1].Label, weekdays[isoWeekday(t)], t.Format("02.01.2006"))
		}
	}
	return ""
}

// planState tells which disk is due today and whether it is plugged in.
type planState struct {
	Today    string // label of the planned disk ("" = nothing planned today)
	TodayKey string
	Next     string // label and day of the next planned disk after today
	State    string // "", ok, swap (another disk is plugged in), missing (none)
	Plugged  []string
	PastTime bool // the daily copy time has passed
}

func (r Rotation) planState(st RotationState, now time.Time) planState {
	var ps planState
	if !r.HasPlan() {
		return ps
	}
	if p := r.PlannedDisks(now); len(p) > 0 {
		ps.Today, ps.TodayKey = p[0].Label, p[0].Key
	}
	for i := 1; i <= 35; i++ {
		t := now.AddDate(0, 0, i)
		if p := r.PlannedDisks(t); len(p) > 0 {
			when := weekdays[isoWeekday(t)] + ", " + t.Format("02.01.")
			if i == 1 {
				when = "morgen"
			}
			ps.Next = "„" + p[0].Label + "“ " + when
			break
		}
	}
	todayOK := false
	for _, d := range st.Disks {
		if d.Present {
			ps.Plugged = append(ps.Plugged, d.Label)
			if d.Key == ps.TodayKey {
				todayOK = true
			}
		}
	}
	ps.PastTime = r.Time != "" && now.Format("15:04") >= r.Time
	switch {
	case ps.Today == "":
	case todayOK:
		ps.State = "ok"
	case len(ps.Plugged) > 0:
		ps.State = "swap"
	default:
		ps.State = "missing"
	}
	return ps
}

type planRuntime struct {
	mu      sync.Mutex
	present map[string]bool // rotation/disk → plugged in at the last look
	started map[string]bool // rotations looked at since the start
}

// checkPlan reports a wrong disk: when one is plugged in on a day another is planned, and
// at the copy time when the planned disk is not there. Each only once a day.
func (c *Core) checkPlan(r Rotation, st RotationState, now time.Time) {
	c.plan.mu.Lock()
	if c.plan.present == nil {
		c.plan.present, c.plan.started = map[string]bool{}, map[string]bool{}
	}
	first := !c.plan.started[r.ID]
	c.plan.started[r.ID] = true
	var plugged []DiskState
	for _, d := range st.Disks {
		key := rtKey(r, d.RotDisk)
		if d.Present && !c.plan.present[key] && !first {
			plugged = append(plugged, d)
		}
		c.plan.present[key] = d.Present
	}
	c.plan.mu.Unlock()
	if !r.CheckPlan || !r.HasPlan() || st.Error != "" {
		return
	}
	ps := r.planState(st, now)
	if ps.Today == "" {
		return
	}
	day := now.Format("2006-01-02")
	once := func(what string) bool {
		key := "plan:" + r.ID + ":" + what
		var last string
		_ = c.St.GetSetting(key, &last)
		if last == day {
			return false
		}
		_ = c.St.SetSetting(key, day)
		return true
	}
	det := func(status string) *notify.Details {
		var rows []notify.Row
		for _, d := range r.Disks {
			if t := d.PlanText(); t != "" {
				row := notify.Row{Kind: "USB", ID: d.Label, Status: "ok", Info: t}
				if d.Key == ps.TodayKey {
					row.Info += " – heute dran"
				}
				rows = append(rows, row)
			}
		}
		return &notify.Details{Status: status, Link: "/wechselplatten", RowsTitle: "Wechselplan", Rows: rows,
			Facts: []notify.Fact{{Label: "Satz", Value: r.Name}, {Label: "Heute dran", Value: "„" + ps.Today + "“"}}}
	}
	for _, d := range plugged {
		if d.Key != ps.TodayKey && once("wrong-"+d.Key) {
			c.EventDetails("warning", rotationSource, "Falsche Festplatte angesteckt: „"+d.Label+"“",
				"Laut Wechselplan ist heute die Festplatte „"+ps.Today+"“ dran. Bitte „"+d.Label+"“ wieder abziehen und „"+ps.Today+"“ anschließen.", det("warning"))
		}
	}
	if ps.PastTime && ps.State != "ok" && once("due") {
		body := "Laut Wechselplan sollte heute die Festplatte „" + ps.Today + "“ angeschlossen sein, "
		if ps.State == "swap" {
			body += "angeschlossen ist aber „" + strings.Join(ps.Plugged, "“, „") + "“. Bitte die Festplatten tauschen."
		} else {
			body += "es ist aber keine Festplatte des Satzes angeschlossen. Bitte „" + ps.Today + "“ anschließen."
		}
		c.EventDetails("warning", rotationSource, "Heute ist Festplatte „"+ps.Today+"“ dran", body, det("warning"))
	}
}

// SetPlan stores the swap plan of a set: days and weeks per disk and whether to check it.
func (c *Core) SetPlan(id string, check bool, plan map[string]RotDisk) (Rotation, string, error) {
	r, err := c.Rotation(id)
	if err != nil {
		return r, "", err
	}
	for i := range r.Disks {
		if p, ok := plan[r.Disks[i].Key]; ok {
			r.Disks[i].Days, r.Disks[i].Week = p.Days, p.Week
			if l := strings.TrimSpace(p.Label); l != "" {
				r.Disks[i].Label = l
			}
		}
	}
	r.CheckPlan = check
	if msg := r.ValidatePlan(); msg != "" {
		return r, msg, nil
	}
	if r.CheckPlan && !r.HasPlan() {
		return r, "Zum Prüfen mindestens einer Platte Wochentage zuordnen.", nil
	}
	if err := c.St.Put(KindRotation, r.ID, r); err != nil {
		return r, "", err
	}
	c.RequestRefresh()
	return r, "", nil
}
