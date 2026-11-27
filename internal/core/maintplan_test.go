package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
)

func TestMaintPlanWindows(t *testing.T) {
	// The first Saturday of the month, 08:00 for four hours.
	p := core.MaintPlan{Reason: "Updates", Repeat: "monthly", MonthMode: "weekday", MonthWeek: "1", MonthWeekday: 6, Time: "08:00", Minutes: 240}
	if errs := p.Validate(); len(errs) > 0 {
		t.Fatal(errs)
	}
	from := time.Date(2026, 11, 27, 12, 0, 0, 0, time.Local)
	ws := p.Windows(from, 2)
	if len(ws) != 2 || time.Unix(ws[0][0], 0).Format("2006-01-02 15:04") != "2026-12-05 08:00" || ws[0][1]-ws[0][0] != 4*3600 ||
		time.Unix(ws[1][0], 0).Format("2006-01-02") != "2027-01-02" {
		t.Fatalf("windows: %v", ws)
	}
	// A window that has begun is still current.
	if ws := p.Windows(time.Date(2026, 12, 5, 10, 0, 0, 0, time.Local), 1); len(ws) != 1 || time.Unix(ws[0][0], 0).Day() != 5 {
		t.Fatalf("running window: %v", ws)
	}
	if !strings.Contains(p.ScheduleText(), "ersten Samstag") || !strings.Contains(p.ScheduleText(), "4 Std") {
		t.Fatal(p.ScheduleText())
	}
	once := core.MaintPlan{Reason: "Umbau", Repeat: "once", Date: "2026-11-28", Time: "22:00", Minutes: 180}
	if errs := once.Validate(); len(errs) > 0 {
		t.Fatal(errs)
	}
	if ws := once.Windows(from, 3); len(ws) != 1 {
		t.Fatalf("once: %v", ws)
	}
	if ws := once.Windows(time.Date(2026, 11, 29, 2, 0, 0, 0, time.Local), 3); len(ws) != 0 {
		t.Fatalf("past: %v", ws)
	}
	bad := core.MaintPlan{Repeat: "weekly", Time: "8 Uhr", Minutes: 5}
	if errs := bad.Validate(); errs["reason"] == "" || errs["time"] == "" || errs["minutes"] == "" || errs["weekdays"] == "" {
		t.Fatalf("validation: %v", errs)
	}
}

// Announced a day before, started at its time, ended at its end – once.
func TestMaintPlanRuns(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	start := time.Now().Add(30 * time.Hour).Truncate(time.Hour)
	p, errs := c.SaveMaintPlan(core.MaintPlan{Reason: "Umbau pve1", Enabled: true, Repeat: "once", Date: start.Format("2006-01-02"), Time: start.Format("15:04"),
		Minutes: 120, PauseJobs: true, Mute: true, AnnounceHours: 24})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	count := func(s string) int { return strings.Count(eventTitles(t, c), s) }
	c.ScheduleMaintPlansForTest(ctx, start.Add(-25*time.Hour))
	if count("Geplante Wartung") != 0 {
		t.Fatal("announced too early")
	}
	c.ScheduleMaintPlansForTest(ctx, start.Add(-23*time.Hour))
	c.ScheduleMaintPlansForTest(ctx, start.Add(-22*time.Hour))
	if count("Geplante Wartung: Umbau pve1") != 1 {
		t.Fatalf("announcement:\n%s", eventTitles(t, c))
	}
	// The window starts: the maintenance runs until its end.
	c.ScheduleMaintPlansForTest(ctx, start.Add(time.Minute))
	m := c.MaintenanceState()
	if !m.Active || m.By != "Zeitplan" || m.Until != start.Add(2*time.Hour).Unix() || !m.PauseJobs || len(m.Paused) == 0 {
		t.Fatalf("not started: %+v", m)
	}
	// Ended early by hand: not started again within the window.
	if _, err := c.EndMaintenance(ctx, "von Hand beendet"); err != nil {
		t.Fatal(err)
	}
	c.ScheduleMaintPlansForTest(ctx, start.Add(30*time.Minute))
	if c.MaintenanceState().Active {
		t.Fatal("started again after it was ended")
	}
	if views := c.MaintPlanViews(); len(views) != 1 || views[0].ID != p.ID || views[0].Text == "" {
		t.Fatalf("views: %+v", views)
	}
	if err := c.DeleteMaintPlan(p.ID); err != nil || len(c.MaintPlans()) != 0 {
		t.Fatal("delete", err)
	}
}
