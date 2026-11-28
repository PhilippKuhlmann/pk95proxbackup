package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/prox"
)

func rtoGuest(cluster string, vmid int, maxDisk int64, backup bool) core.GuestCoverage {
	g := core.GuestCoverage{Guest: prox.Guest{VMID: vmid, Name: "g" + string(rune('a'+vmid%26)), Type: "qemu", MaxDisk: maxDisk}, Cluster: cluster, ClusterName: "PVE " + cluster}
	if backup {
		g.Backups = []core.BackupRef{{Server: "local", Store: "backup", Kind: "fixed", Count: 3}}
	}
	return g
}

func rtoRun(plan string, t int64, res ...core.RestoreTestResult) core.RestoreTestRun {
	return core.RestoreTestRun{ID: plan + "-run", PlanID: plan, Start: t, Status: "ok", Results: res}
}

// Measured beats derived beats assumed; the speed of the restore tests carries over to guests
// without one of their own.
func TestRTOEstimates(t *testing.T) {
	c := newCore(t)
	now := time.Now()
	gb := int64(1e9)
	_ = c.St.SetSetting("guestsize", map[string]any{
		"c1/100": map[string]any{"data": 60 * gb},
		"c1/101": map[string]any{"data": 50 * gb},
	})
	plans := []core.RestoreTestPlan{{ID: "p1", Cluster: "c1"}}
	tested := now.Add(-48 * time.Hour).Unix()
	runs := []core.RestoreTestRun{rtoRun("p1", tested,
		core.RestoreTestResult{VMID: "100", Status: "ok", Restore: 600, Boot: 60, DiskBytes: 100 * gb, Started: tested, RestoreEnd: tested + 600})}
	guests := []core.GuestCoverage{
		rtoGuest("c1", 100, 100*gb, true), // own test
		rtoGuest("c1", 101, 80*gb, true),  // 50 GB data at the 100 MB/s of guest 100
		rtoGuest("c1", 102, 20*gb, true),  // no backup log: size of its disks
		rtoGuest("c2", 200, 30*gb, true),  // other Proxmox VE: the speed of all tests
		rtoGuest("c1", 103, 10*gb, false), // no backup: nothing to restore
	}
	c.RTOEstimatesForTest(guests, plans, runs, now)
	want := []struct {
		secs, boot int64
		basis      string
		rough      bool
	}{{600, 60, "measured", false}, {500, 0, "derived", false}, {200, 0, "derived", true}, {300, 0, "derived", true}}
	for i, w := range want {
		r := guests[i].RTO
		if r == nil || r.Secs != w.secs || r.Boot != w.boot || r.Basis != w.basis || r.Rough != w.rough {
			t.Fatalf("guest %d: %+v, want %+v", guests[i].VMID, r, w)
		}
	}
	if guests[0].RTO.Tested != tested || guests[1].RTO.Rate != 100 {
		t.Fatalf("details: %+v %+v", guests[0].RTO, guests[1].RTO)
	}
	if guests[4].RTO != nil {
		t.Fatalf("guest without backup estimated: %+v", guests[4].RTO)
	}

	// Disks grown since the test: scaled up. Without any test: assumed 100 MB/s.
	grown := []core.GuestCoverage{rtoGuest("c1", 100, 200*gb, true)}
	c.RTOEstimatesForTest(grown, plans, runs, now)
	if r := grown[0].RTO; r.Secs != 1200 || !r.Scaled {
		t.Fatalf("grown disks: %+v", r)
	}
	none := []core.GuestCoverage{rtoGuest("c1", 101, 80*gb, true)}
	c.RTOEstimatesForTest(none, nil, nil, now)
	if r := none[0].RTO; r.Basis != "assumed" || r.Secs != 500 {
		t.Fatalf("assumed: %+v", r)
	}
	// A failed restore says nothing; a test a year old neither.
	old := now.Add(-400 * 24 * time.Hour).Unix()
	bad := []core.RestoreTestRun{
		rtoRun("p1", tested, core.RestoreTestResult{VMID: "100", Status: "error", Restore: 30, Started: tested}),
		rtoRun("p1", old, core.RestoreTestResult{VMID: "100", Status: "ok", Restore: 60, Started: old, RestoreEnd: old + 60}),
	}
	g := []core.GuestCoverage{rtoGuest("c1", 100, 100*gb, true)}
	c.RTOEstimatesForTest(g, plans, bad, now)
	if g[0].RTO.Basis != "assumed" {
		t.Fatalf("failed or old test used: %+v", g[0].RTO)
	}
}

// Targets: a default for all, one per guest, none for a guest; over only above the target.
func TestRTOTargets(t *testing.T) {
	c := newCore(t)
	now := time.Now()
	gb := int64(1e9)
	_ = c.St.SetSetting("guestsize", map[string]any{"c1/101": map[string]any{"data": 50 * gb}})
	est := func() []core.GuestCoverage {
		g := []core.GuestCoverage{rtoGuest("c1", 100, 66*gb, true), rtoGuest("c1", 101, 80*gb, true)} // 660 s, 500 s
		c.RTOEstimatesForTest(g, nil, nil, now)
		return g
	}
	if g := est(); g[0].RTO.Over || g[0].RTO.Target != 0 {
		t.Fatalf("no target yet: %+v", g[0].RTO)
	}
	if err := c.SetRTODefault(3); err == nil {
		t.Fatal("3 minutes accepted")
	}
	if err := c.SetRTODefault(10); err != nil {
		t.Fatal(err)
	}
	if g := est(); !g[0].RTO.Over || g[1].RTO.Over || g[0].RTO.Target != 10 {
		t.Fatalf("default 10 min: %+v %+v", g[0].RTO, g[1].RTO)
	}
	none, five := -1, 5
	if err := c.SetRTOGuest("c1", "100", &none); err != nil {
		t.Fatal(err)
	}
	if err := c.SetRTOGuest("c1", "101", &five); err != nil {
		t.Fatal(err)
	}
	g := est()
	if g[0].RTO.Over || g[0].RTO.Target != 0 || !g[0].RTO.Own || !g[1].RTO.Over || g[1].RTO.Target != 5 {
		t.Fatalf("own targets: %+v %+v", g[0].RTO, g[1].RTO)
	}
	if err := c.SetRTOGuest("c1", "100", nil); err != nil {
		t.Fatal(err)
	}
	if g := est(); g[0].RTO.Own || g[0].RTO.Target != 10 {
		t.Fatalf("back to default: %+v", g[0].RTO)
	}
	if err := c.SetRTOGuest("c1", "x", &five); err == nil {
		t.Fatal("bad id accepted")
	}
}

// The warning comes when a guest is new over its target, otherwise once a week.
func TestCheckRTO(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	now := time.Now()
	gb := int64(1e9)
	_ = c.SetRTODefault(5)
	guests := func(ids ...int) []core.GuestCoverage {
		var g []core.GuestCoverage
		for _, id := range ids {
			g = append(g, rtoGuest("c1", id, 60*gb, true)) // 600 s at the assumed speed
		}
		c.RTOEstimatesForTest(g, nil, nil, now)
		return g
	}
	count := func() int {
		evs, _ := c.St.Events(ctx, "", 0, 50)
		n := 0
		for _, e := range evs {
			if e.Source == "Wiederherstellungszeit" {
				n++
			}
		}
		return n
	}
	c.CheckRTOForTest(guests(100), now)
	if count() != 1 {
		t.Fatalf("no warning: %d", count())
	}
	evs, _ := c.St.Events(ctx, "", 0, 1)
	if !strings.Contains(evs[0].Title, "100") || !strings.Contains(evs[0].Body, "10 Min.") || core.EventCategory(evs[0].Source, evs[0].Title) != "checks" {
		t.Fatalf("event: %+v", evs[0])
	}
	c.CheckRTOForTest(guests(100), now.Add(time.Hour))
	if count() != 1 {
		t.Fatal("warned again within the week")
	}
	c.CheckRTOForTest(guests(100, 101), now.Add(2*time.Hour))
	if count() != 2 {
		t.Fatal("new guest over the target not reported")
	}
	c.CheckRTOForTest(guests(100), now.Add(3*time.Hour))
	if count() != 2 {
		t.Fatal("fewer guests reported")
	}
	c.CheckRTOForTest(guests(100), now.Add(8*24*time.Hour))
	if count() != 3 {
		t.Fatal("no weekly reminder")
	}
	// all within the target again: the next one over is new
	c.CheckRTOForTest(nil, now.Add(9*24*time.Hour))
	c.CheckRTOForTest(guests(100), now.Add(9*24*time.Hour+time.Hour))
	if count() != 4 {
		t.Fatal("over again not reported")
	}
}

func TestRTOText(t *testing.T) {
	for s, want := range map[int64]string{20: "unter 1 Min.", 89: "1 Min.", 91: "2 Min.", 3599: "60 Min.", 3600: "1 Std.", 8100: "2 Std. 15 Min.", 86400: "1 Tag"} {
		if got := core.RTOTextForTest(s); got != want {
			t.Errorf("%d s: %q, want %q", s, got, want)
		}
	}
}
