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

// A one-off archive prepares the empty disk, copies only the newest backup of the chosen
// guests, checks it, records what is on it and unmounts it again.
func TestArchiveCopy(t *testing.T) {
	sd, vd := fake.SyncDuration, fake.VerifyDuration
	fake.SyncDuration, fake.VerifyDuration = 2*time.Second, time.Second
	t.Cleanup(func() { fake.SyncDuration, fake.VerifyDuration = sd, vd })
	c := newCore(t)
	ctx := context.Background()

	a := core.Archive{Name: "Jahresarchiv 2026", ServerID: "local", Disk: "sdd", Store: "archiv-2026",
		SourceServer: "local", SourceStore: "backup", Groups: []string{"vm/100", "ct/110", "vm/250"}, Latest: true, Verify: true, Location: "Schließfach"}
	if errs := c.ValidateArchive(&a); len(errs) > 0 {
		t.Fatalf("validate: %v", errs)
	}
	// A disk of a rotation set must not become an archive.
	bad := core.Archive{Name: "x", ServerID: "local", Store: "usb-a", SourceServer: "local", SourceStore: "backup"}
	if errs := c.ValidateArchive(&bad); errs["store"] == "" {
		t.Fatal("rotation disk accepted as archive")
	}
	a, err := c.StartArchive(a)
	if err != nil {
		t.Fatal(err)
	}
	var st core.ArchiveState
	deadline := time.Now().Add(60 * time.Second)
	sawTask := false
	for {
		for _, rt := range c.RunningTasks(ctx) {
			if strings.Contains(rt.Label, "Jahresarchiv 2026") {
				sawTask = true
			}
		}
		for _, x := range c.ArchiveStates(ctx) {
			if x.ID == a.ID {
				st = x
			}
		}
		if !st.Busy && st.Status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("archive never finished: %+v", st)
		}
		time.Sleep(300 * time.Millisecond)
	}
	if st.Status != "ok" {
		t.Fatalf("status %s: %s %v", st.Status, st.Error, st.Problems)
	}
	if !sawTask {
		t.Fatal("archive not shown in the running tasks")
	}
	if st.Mounted || !st.Present || st.Missing {
		t.Fatalf("disk should be unmounted, present and configured: %+v", st)
	}
	if len(st.Contents) != 3 || st.Contents[0].ID != "100" || st.Contents[1].ID != "110" || st.Contents[2].ID != "250" {
		t.Fatalf("contents: %+v", st.Contents)
	}
	for _, it := range st.Contents {
		if it.Count != 1 || it.Name == "" {
			t.Fatalf("want only the newest backup with name: %+v", it)
		}
	}
	if st.CopySecs < 1 || st.Disk != "" {
		t.Fatalf("copy time / disk not recorded: %+v", st.Archive)
	}
	// No sync job is left behind.
	srv, _ := c.Server("local")
	jobs, _ := c.PBSClient(srv).Jobs(ctx, "sync")
	for _, j := range jobs {
		if j.Store == "archiv-2026" {
			t.Fatalf("leftover sync job %s", j.ID)
		}
	}
	// Years later: the check mounts, reads back and unmounts.
	if err := c.CheckArchive(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckArchive(a.ID); err == nil {
		t.Fatal("second check while the first runs must be refused")
	}
	for c.ArchiveBusy(a.ID) {
		time.Sleep(200 * time.Millisecond)
	}
	got, _ := c.GetArchive(a.ID)
	if len(got.Checks) != 1 || !got.Checks[0].OK {
		t.Fatalf("check: %+v", got.Checks)
	}
	if err := c.ForgetArchive(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	cfgs, _ := c.PBSClient(srv).Datastores(ctx)
	for _, d := range cfgs {
		if d.Name == "archiv-2026" {
			t.Fatal("datastore still configured after forget")
		}
	}
}

// A disk from another Backup Server is found, scanned and read in; its backups can then be
// tested automatically: the test mounts the unplugged-but-present disk and unmounts it again.
func TestImportForeignDisk(t *testing.T) {
	fake.RestoreDuration = time.Second
	t.Cleanup(func() { fake.RestoreDuration = 20 * time.Second })
	c := newCore(t)
	ctx := context.Background()
	parts, err := c.ForeignPartitions(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	uuid := ""
	for _, p := range parts {
		if p.Disk == "sdc" {
			t.Fatal("the rotation disk must not be offered as foreign")
		}
		if p.Disk == "sde" {
			uuid = p.UUID
		}
	}
	if uuid == "" {
		t.Fatalf("foreign disk not offered: %+v", parts)
	}
	found, err := c.ScanPartition(ctx, "local", uuid)
	if err != nil || len(found) != 1 || found[0].Path != "usb-montag" || found[0].VMs != 2 {
		t.Fatalf("scan: %+v %v", found, err)
	}
	// Wrong folder: nothing is left configured.
	req := core.ImportRequest{ServerID: "local", UUID: uuid, Path: "falsch", Store: "kunde-mueller", Name: "Platte Müller GmbH"}
	if _, err := c.ImportDisk(ctx, req); err == nil {
		t.Fatal("import of a wrong folder must fail")
	}
	srv, _ := c.Server("local")
	pbs := c.PBSClient(srv)
	cfgs, _ := pbs.Datastores(ctx)
	for _, d := range cfgs {
		if d.Name == "kunde-mueller" {
			t.Fatal("failed import left a datastore behind")
		}
	}
	req.Path = "usb-montag"
	if errs := req.Validate(); len(errs) > 0 {
		t.Fatal(errs)
	}
	a, err := c.ImportDisk(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Imported || len(a.Contents) != 3 || a.Contents[0].Name != "erp" || a.Contents[0].Count != 3 {
		t.Fatalf("imported contents: %+v", a.Contents)
	}
	if err := c.EjectArchive(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	// Automatic test of the read-in disk.
	c.EnsureStorage = func(ctx context.Context, pve *prox.PVE, cl core.PVECluster, srv core.PBSServer, store, keyFP string) (string, error) {
		return "pbs-appliance", nil
	}
	plan := core.RestoreTestPlan{ID: "pf", Name: "Fremde Platte", Cluster: "cluster", Node: "pve1", Storage: "local-lvm",
		PBS: "local", Store: "kunde-mueller", Select: "include", VMIDs: "210", BootWait: 6, Limit: 5, IDBase: 990000}
	_ = c.St.Put(core.KindRTPlan, plan.ID, plan)
	if _, err := c.StartRestoreTest(plan.ID, "test"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for c.ActiveRestoreTest() != nil {
		if time.Now().After(deadline) {
			t.Fatal("restore test did not finish")
		}
		time.Sleep(300 * time.Millisecond)
	}
	run := c.RestoreTestRuns()[0]
	if run.Status != "ok" || len(run.Results) != 1 || run.Results[0].Name != "webshop" {
		t.Fatalf("restore test of the foreign disk: %s %s %+v", run.Status, run.Error, run.Results)
	}
	if st := c.ArchiveStates(ctx); len(st) != 1 || st[0].Mounted || !st[0].Present {
		t.Fatalf("disk must be unmounted again after the test: %+v", st)
	}
}
