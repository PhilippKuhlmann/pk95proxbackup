package core_test

import (
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
)

func day(s string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04", s+" 12:00", time.Local)
	return t
}

func nextDays(p core.RestoreTestPlan, from string, n int) []string {
	var out []string
	for _, ts := range p.NextRuns(day(from), n) {
		out = append(out, time.Unix(ts, 0).Format("2006-01-02"))
	}
	return out
}

func TestScheduleMonthly(t *testing.T) {
	norm := func(p core.RestoreTestPlan) core.RestoreTestPlan {
		p.Name, p.Cluster, p.Node, p.Storage, p.PBS, p.Store, p.Time = "t", "c", "n", "s", "p", "d", "03:00"
		if errs := p.Normalize(); len(errs) > 0 {
			t.Fatal(errs)
		}
		return p
	}
	first := norm(core.RestoreTestPlan{Repeat: "monthly", MonthWeek: "1", MonthWeekday: 7, EveryMonths: 1, StartMonth: 1})
	if got := strings.Join(nextDays(first, "2026-11-13", 3), " "); got != "2026-12-06 2027-01-03 2027-02-07" {
		t.Errorf("1st Sunday: %s", got)
	}
	if first.ScheduleText() != "Am ersten Sonntag jedes Monats um 03:00" {
		t.Errorf("text: %q", first.ScheduleText())
	}
	last := norm(core.RestoreTestPlan{Repeat: "monthly", MonthWeek: "last", MonthWeekday: 7, EveryMonths: 3, StartMonth: 1})
	if got := strings.Join(nextDays(last, "2026-11-13", 3), " "); got != "2027-01-31 2027-04-25 2027-07-25" {
		t.Errorf("last Sunday every 3 months: %s", got)
	}
	if !strings.Contains(last.ScheduleText(), "alle 3 Monate (Jan, Apr, Jul, Okt)") {
		t.Errorf("text: %q", last.ScheduleText())
	}
	two := norm(core.RestoreTestPlan{Repeat: "monthly", MonthMode: "day", MonthDay: 31, EveryMonths: 2, StartMonth: 2})
	if got := strings.Join(nextDays(two, "2026-11-13", 3), " "); got != "2026-12-31 2027-02-28 2027-04-30" {
		t.Errorf("last day every 2 months from Feb: %s", got)
	}
	yearly := norm(core.RestoreTestPlan{Repeat: "monthly", MonthWeek: "2", MonthWeekday: 6, EveryMonths: 12, StartMonth: 3})
	if got := strings.Join(nextDays(yearly, "2026-11-13", 2), " "); got != "2027-03-13 2028-03-11" {
		t.Errorf("yearly: %s", got)
	}
}

func TestScheduleWeekly(t *testing.T) {
	// Old plans (no repeat set) keep working: every Saturday.
	old := core.RestoreTestPlan{Name: "t", Cluster: "c", Node: "n", Storage: "s", PBS: "p", Store: "d", Weekdays: []int{6}, Time: "02:00"}
	_ = old.Normalize()
	if got := strings.Join(nextDays(old, "2026-11-13", 2), " "); got != "2026-11-14 2026-11-21" {
		t.Errorf("weekly: %s", got)
	}
	// Every 2 weeks on Monday and Thursday, counted from Monday 16.11.2026.
	p := core.RestoreTestPlan{Name: "t", Cluster: "c", Node: "n", Storage: "s", PBS: "p", Store: "d", Weekdays: []int{4, 1}, Time: "21:00", EveryWeeks: 2, StartDate: "2026-11-16"}
	if errs := p.Normalize(); len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := strings.Join(nextDays(p, "2026-11-13", 4), " "); got != "2026-11-16 2026-11-19 2026-11-30 2026-12-03" {
		t.Errorf("every 2 weeks: %s", got)
	}
	if p.ScheduleText() != "Montag und Donnerstag alle 2 Wochen um 21:00" {
		t.Errorf("text: %q", p.ScheduleText())
	}
}
