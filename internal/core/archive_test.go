package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/fake"
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
