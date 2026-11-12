package core_test

import (
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
)

// The scheme of most customers: Monday to Thursday, and one disk for each Friday of the month.
func nineDisks() core.Rotation {
	r := core.Rotation{ID: "r9", Name: "USB täglich", Time: "22:30", CheckPlan: true}
	for i, l := range []string{"Montag", "Dienstag", "Mittwoch", "Donnerstag"} {
		r.Disks = append(r.Disks, core.RotDisk{Key: l[:2], Label: l, Days: []int{i + 1}})
	}
	for n := 1; n <= 5; n++ {
		w := string(rune('0' + n))
		r.Disks = append(r.Disks, core.RotDisk{Key: "f" + w, Label: "Freitag " + w, Days: []int{5}, Week: w})
	}
	return r
}

func TestPlanNineDisks(t *testing.T) {
	r := nineDisks()
	if msg := r.ValidatePlan(); msg != "" {
		t.Fatal(msg)
	}
	at := func(s string) time.Time { d, _ := time.ParseInLocation("2006-01-02 15:04", s, time.Local); return d }
	for day, want := range map[string]string{
		"2026-05-04 10:00": "Montag", "2026-05-07 10:00": "Donnerstag",
		"2026-05-01 10:00": "Freitag 1", "2026-05-08 10:00": "Freitag 2", "2026-05-29 10:00": "Freitag 5",
		"2026-05-02 10:00": "", // Saturday
	} {
		p := r.PlannedDisks(at(day))
		got := ""
		if len(p) > 0 {
			got = p[0].Label
		}
		if got != want || len(p) > 1 {
			t.Errorf("%s: planned %q (%d), want %q", day, got, len(p), want)
		}
	}
	if got := r.Disks[5].PlanText(); got != "2. Freitag im Monat" {
		t.Errorf("plan text %q", got)
	}
	// Two disks on the same day are refused.
	r.Disks[8].Week = "1"
	if msg := r.ValidatePlan(); !strings.Contains(msg, "Freitag 1") || !strings.Contains(msg, "Freitag 5") {
		t.Errorf("conflict not found: %q", msg)
	}
}

func TestPlanReportsWrongDisk(t *testing.T) {
	c := newCore(t)
	r := nineDisks()
	monday := time.Date(2026, 5, 4, 8, 0, 0, 0, time.Local)
	state := func(present ...string) core.RotationState {
		st := core.RotationState{Rotation: r}
		for _, d := range r.Disks {
			ds := core.DiskState{RotDisk: d}
			for _, p := range present {
				ds.Present = ds.Present || p == d.Label
			}
			st.Disks = append(st.Disks, ds)
		}
		return st
	}
	// Friday's disk still plugged in from last week when the appliance starts: no report yet.
	c.CheckPlanForTest(r, state("Freitag 5"), monday)
	// Swapped against Tuesday's disk by mistake.
	c.CheckPlanForTest(r, state("Dienstag"), monday.Add(time.Minute))
	titles := eventTitles(t, c)
	if !strings.Contains(titles, "Falsche Festplatte angesteckt: „Dienstag“") {
		t.Fatalf("wrong disk not reported:\n%s", titles)
	}
	// Plugged in again: reported only once a day.
	c.CheckPlanForTest(r, state(), monday.Add(2*time.Minute))
	c.CheckPlanForTest(r, state("Dienstag"), monday.Add(3*time.Minute))
	if n := strings.Count(eventTitles(t, c), "Falsche Festplatte"); n != 1 {
		t.Fatalf("reported %d times", n)
	}
	// At the copy time Monday's disk is still missing.
	c.CheckPlanForTest(r, state("Dienstag"), time.Date(2026, 5, 4, 22, 31, 0, 0, time.Local))
	if !strings.Contains(eventTitles(t, c), "Heute ist Festplatte „Montag“ dran") {
		t.Fatal("missing planned disk not reported")
	}
	// The right disk: nothing more.
	before := eventTitles(t, c)
	tuesday := time.Date(2026, 5, 5, 9, 0, 0, 0, time.Local)
	c.CheckPlanForTest(r, state(), tuesday)
	c.CheckPlanForTest(r, state("Dienstag"), tuesday.Add(time.Minute))
	c.CheckPlanForTest(r, state("Dienstag"), tuesday.Add(14*time.Hour))
	if eventTitles(t, c) != before {
		t.Fatalf("right disk reported:\n%s", eventTitles(t, c))
	}
	// Without the check nothing is reported.
	r.CheckPlan = false
	c.CheckPlanForTest(r, state("Freitag 3"), time.Date(2026, 5, 6, 9, 0, 0, 0, time.Local))
	if eventTitles(t, c) != before {
		t.Fatal("reported although the check is off")
	}
}
