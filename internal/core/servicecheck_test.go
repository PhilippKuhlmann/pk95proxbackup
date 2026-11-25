package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/fake"
	"pk95proxbackup/internal/prox"
)

// The restore test looks into the test guests: VM 100 (guest agent) offers 22 and 389 but not
// 9999, VM 101 has no agent, container 110 offers 53 (pct exec on the node).
func TestRestoreTestServices(t *testing.T) {
	fake.RestoreDuration = time.Second
	core.ServiceWait = time.Second
	t.Cleanup(func() { fake.RestoreDuration = 20 * time.Second; core.ServiceWait = 90 * time.Second })
	c := newCore(t)
	c.EnsureStorage = func(context.Context, *prox.PVE, core.PVECluster, core.PBSServer, string, string) (string, error) {
		return "pbs-appliance", nil
	}
	plan := core.RestoreTestPlan{ID: "p1", Name: "Dienste", Cluster: "cluster", Node: "pve1", Storage: "local-lvm",
		PBS: "local", Store: "backup", Select: "include", VMIDs: "100,101,110", BootWait: 6, Limit: 5, IDBase: 990000,
		Services: "100: 22, 389, 9999\n101: 22\n110: 53"}
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
		time.Sleep(500 * time.Millisecond)
	}
	byID := map[string]core.RestoreTestResult{}
	for _, r := range c.RestoreTestRuns()[0].Results {
		byID[r.VMID] = r
	}
	r := byID["100"]
	if r.Status != "warning" || !strings.Contains(r.Info, "Port 9999 lauscht nicht") || len(r.Services) != 3 || !r.Services[0].OK || r.Services[2].OK {
		t.Fatalf("vm 100: %+v", r)
	}
	if r := byID["101"]; r.Status != "warning" || !strings.Contains(r.ServiceNote, "Gast-Agent") {
		t.Fatalf("vm 101 (no agent): %+v", r)
	}
	if r := byID["110"]; r.Status != "ok" || !strings.Contains(r.Info, "Dienst antwortet (Port 53)") {
		t.Fatalf("ct 110: %+v", r)
	}
	// a plan with a wrong line is refused
	bad := plan
	bad.Services = "100 443"
	if errs := bad.Normalize(); errs["services"] == "" {
		t.Fatalf("bad services accepted: %v", errs)
	}
}
