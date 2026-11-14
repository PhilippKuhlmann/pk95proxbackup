package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
)

func guestOf(t *testing.T, gs []core.GuestCoverage, vmid int) core.GuestCoverage {
	t.Helper()
	for _, g := range gs {
		if g.VMID == vmid {
			return g
		}
	}
	t.Fatalf("guest %d missing", vmid)
	return core.GuestCoverage{}
}

func TestCoverage(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	gs, errs := c.Coverage(ctx)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	g := guestOf(t, gs, 100)
	ch := g.Chain
	if ch.State != "ok" || ch.Backup.State != "ok" || !strings.Contains(ch.Backup.Where, "Diese Appliance") {
		t.Fatalf("backup of 100: %+v", ch)
	}
	if ch.Copy.State != "ok" || !strings.Contains(ch.Copy.Where, "PBS Büro") {
		t.Fatalf("copy of 100 (the office PBS has the same times): %+v", ch.Copy)
	}
	if ch.USB.State != "ok" || !strings.Contains(ch.USB.Where, "Platte A") {
		t.Fatalf("usb of 100: %+v", ch.USB)
	}
	if ch.Test.State != "none" {
		t.Fatalf("no test plan: %+v", ch.Test)
	}
	if g.LastBackup == 0 || len(g.Backups) == 0 {
		t.Fatalf("backups of 100: %+v", g.Backups)
	}
	if x := guestOf(t, gs, 112); x.Chain.State != "bad" || x.Chain.Backup.Note != "in keinem Sicherungsjob" {
		t.Fatalf("112 not in a job: %+v", x.Chain)
	}

	// The USB disk is pulled: what was on it is still known.
	pbs, _, _ := c.PBS("local")
	upid, err := pbs.Unmount(ctx, "usb-a")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = pbs.WaitTask(ctx, upid, time.Minute)
	gs, _ = c.Coverage(ctx)
	g = guestOf(t, gs, 100)
	if g.Chain.USB.State != "ok" || len(g.Seen) == 0 || g.Seen[0].Store != "usb-a" || g.Seen[0].Seen == 0 {
		t.Fatalf("pulled disk forgotten: %+v %+v", g.Chain.USB, g.Seen)
	}
	for _, b := range g.Backups {
		if b.Store == "usb-a" {
			t.Fatal("a pulled disk is no source for restoring")
		}
	}

	// A failed restore test breaks the chain.
	plan := core.RestoreTestPlan{ID: "p1", Name: "Wöchentlich", Enabled: true, Cluster: "cluster", Select: "include", VMIDs: "100"}
	_ = c.St.Put(core.KindRTPlan, plan.ID, plan)
	now := time.Now().Unix()
	run := core.RestoreTestRun{ID: "r1", PlanID: "p1", PlanName: "Wöchentlich", Start: now - 600, End: now, Status: "error",
		Results: []core.RestoreTestResult{{Type: "vm", VMID: "100", Status: "error"}}}
	_ = c.St.Put(core.KindRTRun, run.ID, run)
	gs, _ = c.Coverage(ctx)
	if g = guestOf(t, gs, 100); g.Chain.Test.State != "bad" || g.Chain.State != "bad" {
		t.Fatalf("failed restore test: %+v", g.Chain)
	}
	if x := guestOf(t, gs, 101); x.Chain.Test.State != "none" {
		t.Fatalf("101 is not in the plan: %+v", x.Chain.Test)
	}
	sum := c.RefreshCoverage(ctx)
	if sum.Bad != 1 || len(sum.Names) != 1 || !strings.HasPrefix(sum.Names[0], "100 ") || sum.Total < 5 {
		t.Fatalf("summary (guests outside a job do not count): %+v", sum)
	}
}
