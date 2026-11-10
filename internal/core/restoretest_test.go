package core_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/fake"
	"pk95proxbackup/internal/prox"
)

// Three guests are restored, isolated, started, judged and removed again. VM 101 carries a
// passed-through PCI card, a USB device, an ISO and a bridge the node does not have; the
// container a bind mount – the test only passes when all of that is taken away first.
func TestRestoreTestRun(t *testing.T) {
	fake.RestoreDuration = time.Second
	t.Cleanup(func() { fake.RestoreDuration = 20 * time.Second })
	c := newCore(t)
	c.EnsureStorage = func(ctx context.Context, pve *prox.PVE, cl core.PVECluster, srv core.PBSServer, store string) (string, error) {
		return "pbs-appliance", nil
	}
	plan := core.RestoreTestPlan{ID: "p1", Name: "Wöchentlich", Cluster: "cluster", Node: "pve1", Storage: "local-lvm",
		PBS: "local", Store: "backup", Select: "include", VMIDs: "100,101,110", BootWait: 6, Limit: 5, IDBase: 990000, MaxCores: 2, MaxMem: 4096}
	if err := c.St.Put(core.KindRTPlan, plan.ID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := c.StartRestoreTest(plan.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.StartRestoreTest(plan.ID, "test"); err == nil {
		t.Fatal("a second run must be refused while one is running")
	}
	deadline := time.Now().Add(90 * time.Second)
	for c.ActiveRestoreTest() != nil {
		if time.Now().After(deadline) {
			t.Fatal("run did not finish")
		}
		time.Sleep(500 * time.Millisecond)
	}
	run := c.RestoreTestRuns()[0]
	byID := map[string]core.RestoreTestResult{}
	for _, r := range run.Results {
		byID[r.VMID] = r
	}
	if run.Status != "ok" || len(run.Results) != 3 {
		t.Fatalf("run %s: %+v", run.Status, run.Results)
	}
	if r := byID["100"]; r.Status != "ok" || !strings.Contains(r.OS, "Debian") || r.TestID < 990000 {
		t.Fatalf("vm 100 (agent): %+v", r)
	}
	r101 := byID["101"]
	if r101.Status != "ok" {
		t.Fatalf("vm 101: %+v", r101)
	}
	for _, want := range []string{"Netzwerk getrennt", "hostpci0 entfernt", "usb0 entfernt", "ISO in ide2 entfernt", "bridge=vmbr9 fehlt"} {
		if !strings.Contains(r101.Notes, want) {
			t.Errorf("vm 101 notes miss %q: %s", want, r101.Notes)
		}
	}
	if r := byID["110"]; r.Status != "ok" || !strings.Contains(r.Notes, "mp0 (Bind-Mount) entfernt") {
		t.Fatalf("ct 110: %+v", r)
	}
	// nothing is left behind
	pve, _, _ := c.PVE("cluster")
	guests, _ := pve.Guests(context.Background())
	for _, g := range guests {
		if strings.HasPrefix(g.Name, "restoretest-") || g.VMID >= 990000 {
			t.Fatalf("test guest left: %+v", g)
		}
	}
	evs, _ := c.St.Events(context.Background(), "", 0, 30)
	found := false
	for _, e := range evs {
		if strings.HasPrefix(e.Title, "Wiederherstellungstest erfolgreich – alle 3 Server") {
			found = true
		}
	}
	if !found {
		t.Fatal("no success report")
	}
}

func TestRestoreTestSkipsWithoutSpace(t *testing.T) {
	fake.RestoreDuration = time.Second
	t.Cleanup(func() { fake.RestoreDuration = 20 * time.Second })
	c := newCore(t)
	c.EnsureStorage = func(ctx context.Context, pve *prox.PVE, cl core.PVECluster, srv core.PBSServer, store string) (string, error) {
		return "pbs-appliance", nil
	}
	// The target storage has 2 GiB free: VM 101 (64 GiB disk) must be skipped, not restored.
	plan := core.RestoreTestPlan{ID: "p2", Name: "x", Cluster: "cluster", Node: "pve1", Storage: "voll",
		PBS: "local", Store: "backup", Select: "include", VMIDs: "101", BootWait: 6, Limit: 5, IDBase: 990000}
	_ = c.St.Put(core.KindRTPlan, plan.ID, plan)
	if _, err := c.StartRestoreTest(plan.ID, "test"); err != nil {
		t.Fatal(err)
	}
	for c.ActiveRestoreTest() != nil {
		time.Sleep(300 * time.Millisecond)
	}
	run := c.RestoreTestRuns()[0]
	r := run.Results[0]
	if r.Status != "skipped" || !strings.Contains(r.Info, "auf voll sind 2,0 GiB frei") || r.TestID != 0 {
		t.Fatalf("vm 101 on a full storage: %+v", r)
	}
	if run.Status != "warning" {
		t.Fatalf("a skipped guest makes the run yellow: %s", run.Status)
	}
}

func TestRunCopyNeverNil(t *testing.T) {
	b, _ := json.Marshal(core.CopyRunForTest(&core.RestoreTestRun{ID: "x"}))
	if !strings.Contains(string(b), `"results":[]`) {
		t.Fatalf("results must be a list: %s", b)
	}
}
