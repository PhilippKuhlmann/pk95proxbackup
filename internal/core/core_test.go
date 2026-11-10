package core_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/fake"
	"pk95proxbackup/internal/hostexec"
	"pk95proxbackup/internal/prox"
	"pk95proxbackup/internal/store"
)

func newCore(t *testing.T) *core.Core {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	signer, err := hostexec.LoadOrCreateKey(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	c := core.New(st, dir, "test", signer)
	stop, err := fake.Setup(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return c
}

func TestPinnedFingerprint(t *testing.T) {
	c := newCore(t)
	srv, err := c.Server("local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.PBSClient(srv).Version(context.Background()); err != nil {
		t.Fatalf("pinned connection failed: %v", err)
	}
	srv.Fingerprint = strings.Repeat("ab:", 31) + "ab"
	_, err = c.PBSClient(srv).Version(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Fingerabdruck") {
		t.Fatalf("wrong fingerprint must be rejected, got %v", err)
	}
}

func TestFetchFingerprint(t *testing.T) {
	c := newCore(t)
	srv, _ := c.Server("local")
	fp, err := prox.FetchFingerprint(context.Background(), srv.Host, srv.Port)
	if err != nil {
		t.Fatal(err)
	}
	if prox.NormalizeFingerprint(fp) != prox.NormalizeFingerprint(srv.Fingerprint) {
		t.Fatalf("fingerprint %s != %s", fp, srv.Fingerprint)
	}
}

// A copy onto a plugged-in rotation disk runs the sync job, prunes and unmounts the disk,
// after which the disk is reported as ready to be swapped.
func TestRotationCopyCycle(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	rot, err := c.Rotation("demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyRotation(ctx, rot); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Only the newest backup of each guest goes onto the disk, and only it stays there.
	rot.TransferLast, rot.KeepLast, rot.KeepWeekly = 1, 1, 0
	if err := c.St.Put(core.KindRotation, rot.ID, rot); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyRotation(ctx, rot); err != nil {
		t.Fatalf("apply: %v", err)
	}
	disk := rot.Disks[0] // usb-a: plugged in and mounted in the demo
	before := c.RotationStates(ctx)[0].Disks[0]
	if !before.Present || !before.Mounted {
		t.Fatalf("demo disk A should be present and mounted: %+v", before)
	}
	if err := c.RunRotationDisk(ctx, rot, disk); err != nil {
		t.Fatalf("run: %v", err)
	}
	// While it copies, the running tasks show the copy with its progress.
	time.Sleep(3 * time.Second)
	var copying *core.RunningTask
	for _, rt := range c.RunningTasks(ctx) {
		if strings.Contains(rt.Label, "Platte A") {
			copying = &rt
		}
	}
	if copying == nil || copying.Progress.Percent < 0 || copying.UPID == "" {
		t.Fatalf("running copy not reported with progress: %+v", c.RunningTasks(ctx))
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		st := c.RotationStates(ctx)[0].Disks[0]
		if st.State == "ready" {
			if st.Mounted {
				t.Fatal("ready disk must be unmounted")
			}
			if time.Since(time.Unix(st.LastOK, 0)) > time.Minute {
				t.Fatalf("last copy not updated: %v", st.LastOK)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("disk never became ready: %+v", st)
		}
		time.Sleep(500 * time.Millisecond)
	}
	srv, _ := c.Server("local")
	pbs := c.PBSClient(srv)
	if _, err := pbs.Mount(ctx, "usb-a"); err != nil {
		t.Fatal(err)
	}
	src, _ := pbs.Snapshots(ctx, "backup", "")
	dst, err := pbs.Snapshots(ctx, "usb-a", "")
	if err != nil {
		t.Fatal(err)
	}
	newest := map[string]int64{}
	for _, sn := range src {
		if sn.Time > newest[sn.Type+"/"+sn.ID] {
			newest[sn.Type+"/"+sn.ID] = sn.Time
		}
	}
	if len(dst) != len(newest) {
		t.Fatalf("disk holds %d snapshots, want one per guest (%d)", len(dst), len(newest))
	}
	for _, sn := range dst {
		if newest[sn.Type+"/"+sn.ID] != sn.Time {
			t.Fatalf("disk holds %s/%s from %d, newest is %d", sn.Type, sn.ID, sn.Time, newest[sn.Type+"/"+sn.ID])
		}
	}
	jobs, _ := pbs.Jobs(ctx, "sync")
	for _, j := range jobs {
		if j.ID == rot.SyncJobID(disk) && j.TransferLast != 1 {
			t.Fatalf("sync job without transfer-last: %+v", j)
		}
	}
	evs, _ := c.St.Events(ctx, "", 0, 20)
	found := false
	for _, e := range evs {
		if strings.Contains(e.Title, "kann abgezogen werden") && strings.Contains(e.Body, "Beim nächsten Wechsel bitte die Festplatte „Platte B (ungerade Tage)“ anschließen") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no swap event: %+v", evs)
	}
	// Disk B is not plugged in: running it must fail with a clear message.
	if err := c.RunRotationDisk(ctx, rot, rot.Disks[1]); err == nil || !strings.Contains(err.Error(), "steckt sie") {
		t.Fatalf("absent disk: %v", err)
	}
}

func TestRotationValidate(t *testing.T) {
	r := core.Rotation{Name: "x", ServerID: "a", SourceServer: "a", SourceStore: "s", Rhythm: "daily", Time: "25:00"}
	if errs := r.Validate(); errs["time"] == "" {
		t.Error("bad time accepted")
	}
	r.Time, r.RunOnMount = "", false
	if errs := r.Validate(); errs["time"] == "" {
		t.Error("neither time nor run-on-mount accepted")
	}
	r.RunOnMount = true
	if errs := r.Validate(); len(errs) != 0 {
		t.Errorf("valid rotation rejected: %v", errs)
	}
}

func TestJobID(t *testing.T) {
	if id := core.JobID("pk95-prune", "short"); id != "pk95-prune-short" {
		t.Errorf("got %s", id)
	}
	if id := core.JobID("pk95-prune", strings.Repeat("x", 40)); len(id) > 32 {
		t.Errorf("too long: %s", id)
	}
}

func TestUnescapeWorkerID(t *testing.T) {
	if got := core.UnescapeWorkerIDForTest(`\x2d:backup:usb\x2da::pk95r\x2dab\x2dcd`); got != "-:backup:usb-a::pk95r-ab-cd" {
		t.Fatalf("got %q", got)
	}
}
