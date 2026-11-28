package core_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/fake"
	"pk95proxbackup/internal/prox"
)

func runPlan(t *testing.T, c *core.Core, plan core.RestoreTestPlan) core.RestoreTestRun {
	t.Helper()
	if err := c.St.Put(core.KindRTPlan, plan.ID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := c.StartRestoreTest(plan.ID, "test"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(90 * time.Second)
	for c.ActiveRestoreTest() != nil {
		if time.Now().After(deadline) {
			t.Fatal("run did not finish")
		}
		time.Sleep(300 * time.Millisecond)
	}
	return c.RestoreTestRuns()[0]
}

// A plan that keeps its test guests leaves them on the node, on the bridge and VLAN it names,
// running; the next run replaces them, guests no longer tested go, and so do all of them when
// keeping is switched off. They never count as servers without a backup.
func TestRestoreTestKeepsGuests(t *testing.T) {
	fake.RestoreDuration = time.Second
	t.Cleanup(func() { fake.RestoreDuration = 20 * time.Second })
	c := newCore(t)
	c.EnsureStorage = func(ctx context.Context, pve *prox.PVE, cl core.PVECluster, srv core.PBSServer, store, keyFP string) (string, error) {
		return "pbs-appliance", nil
	}
	ctx := context.Background()
	pve, _, _ := c.PVE("cluster")
	plan := core.RestoreTestPlan{ID: "p1", Name: "Testsystem", Cluster: "cluster", Node: "pve1", Storage: "local-lvm",
		PBS: "local", Store: "backup", Select: "include", VMIDs: "100,110", BootWait: 6, Limit: 5, IDBase: 990000,
		Keep: true, KeepRunning: true, Network: "bridge", Bridge: "vmbr1", VLAN: 20}
	run := runPlan(t, c, plan)
	if run.Status != "ok" {
		t.Fatalf("run %s: %+v", run.Status, run.Results)
	}
	kept := c.KeptGuests()["p1"]
	if len(kept) != 2 {
		t.Fatalf("kept: %+v", kept)
	}
	for _, k := range kept {
		cfg, err := pve.GuestConfig(ctx, k.Node, k.Type, k.VMID)
		if err != nil {
			t.Fatal(err)
		}
		net := fmt.Sprint(cfg["net0"])
		if !strings.Contains(fmt.Sprint(cfg["tags"]), "pk95-testsystem") || !strings.Contains(net, "bridge=vmbr1") ||
			!strings.Contains(net, "tag=20") || strings.Contains(net, "link_down") || !k.Running || k.Network != "vmbr1, VLAN 20" {
			t.Fatalf("kept %d: %+v / %v", k.VMID, k, cfg)
		}
		st, _ := pve.GuestStatus(ctx, k.Node, k.Type, k.VMID)
		if st.Status != "running" {
			t.Fatalf("kept %d not running: %s", k.VMID, st.Status)
		}
	}
	if kept[0].Name != "test-dc01" || kept[1].Name != "pihole" {
		t.Fatalf("names: %s, %s", kept[0].Name, kept[1].Name)
	}
	for _, r := range run.Results {
		if !strings.Contains(r.Info, "bleibt als") || !strings.Contains(r.Notes, "Netzwerk an vmbr1, VLAN 20") {
			t.Fatalf("result %s: %s / %s", r.VMID, r.Info, r.Notes)
		}
	}
	// out of the job that saves all guests, the administrator's own excludes untouched
	exclude := func() string {
		jobs, _ := pve.BackupJobs(ctx)
		for _, j := range jobs {
			if j.ID == "backup-nightly" {
				return j.Exclude
			}
		}
		return "?"
	}
	if got := exclude(); got != "112,120,130,990000,990001" {
		t.Fatalf("exclude after keeping: %q", got)
	}
	if ours := c.TestExcludesOf("cluster")["backup-nightly"]; strings.Join(ours, ",") != "990000,990001" {
		t.Fatalf("recorded: %v", ours)
	}
	// an administrator saves the job with an exclude list of their own: the copies come back in
	if err := pve.UpdateBackupJob(ctx, "backup-nightly", prox.Params{"exclude": "112,113"}); err != nil {
		t.Fatal(err)
	}
	c.SyncTestExcludes(ctx)
	if got := exclude(); got != "112,113,990000,990001" {
		t.Fatalf("exclude after an edit: %q", got)
	}
	// not a gap in the backup chain
	guests, _ := c.Coverage(ctx)
	for _, g := range guests {
		if g.VMID >= 990000 {
			t.Fatalf("test guest listed as server: %+v", g.Guest)
		}
	}

	// Next run: only 100, switched off afterwards. The copy of 100 is replaced, the one of 110 goes.
	plan.VMIDs, plan.KeepRunning = "100", false
	run = runPlan(t, c, plan)
	kept = c.KeptGuests()["p1"]
	if run.Status != "ok" || len(kept) != 1 || kept[0].Orig != "vm/100" || kept[0].Running {
		t.Fatalf("second run %s: %+v", run.Status, kept)
	}
	if got := exclude(); got != "112,113,"+strconv.Itoa(kept[0].VMID) {
		t.Fatalf("exclude after the second run: %q", got)
	}
	list, _ := pve.Guests(ctx)
	test := 0
	for _, g := range list {
		if g.VMID >= 990000 {
			test++
			if g.Status != "stopped" {
				t.Fatalf("kept guest still running: %+v", g)
			}
		}
	}
	if test != 1 {
		t.Fatalf("%d test guests on the node", test)
	}

	// Keeping off: the next run removes the copy, its own test guests go as before.
	plan.Keep = false
	runPlan(t, c, plan)
	if k := c.KeptGuests()["p1"]; len(k) != 0 {
		t.Fatalf("still kept: %+v", k)
	}
	list, _ = pve.Guests(ctx)
	for _, g := range list {
		if g.VMID >= 990000 {
			t.Fatalf("guest left: %+v", g)
		}
	}
	if got := exclude(); got != "112,113" || len(c.TestExcludesOf("cluster")) != 0 {
		t.Fatalf("exclude after keeping stopped: %q / %v", got, c.TestExcludesOf("cluster"))
	}
}

// DeleteKept removes the copies of a plan, but never a guest that lost the tag.
func TestDeleteKept(t *testing.T) {
	fake.RestoreDuration = time.Second
	t.Cleanup(func() { fake.RestoreDuration = 20 * time.Second })
	c := newCore(t)
	c.EnsureStorage = func(ctx context.Context, pve *prox.PVE, cl core.PVECluster, srv core.PBSServer, store, keyFP string) (string, error) {
		return "pbs-appliance", nil
	}
	ctx := context.Background()
	pve, _, _ := c.PVE("cluster")
	plan := core.RestoreTestPlan{ID: "p2", Name: "Behalten", Cluster: "cluster", Node: "pve1", Storage: "local-lvm",
		PBS: "local", Store: "backup", Select: "include", VMIDs: "100,102", BootWait: 6, Limit: 5, IDBase: 990000, Keep: true}
	runPlan(t, c, plan)
	kept := c.KeptGuests()["p2"]
	if len(kept) != 2 {
		t.Fatalf("kept: %+v", kept)
	}
	// someone took one of them over
	if err := pve.SetGuestConfig(ctx, kept[1].Node, kept[1].Type, kept[1].VMID, prox.Params{"tags": "eigene"}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteKept("p2", "admin"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for len(c.KeptGuests()["p2"]) > 0 && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if k := c.KeptGuests()["p2"]; len(k) != 0 {
		t.Fatalf("still kept: %+v", k)
	}
	list, _ := pve.Guests(ctx)
	have := map[int]bool{}
	for _, g := range list {
		have[g.VMID] = true
	}
	if have[kept[0].VMID] || !have[kept[1].VMID] {
		t.Fatalf("deleted the wrong ones: %v", have)
	}
}
