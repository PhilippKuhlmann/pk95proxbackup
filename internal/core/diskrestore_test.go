package core_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/fake"
	"pk95proxbackup/internal/prox"
)

func newestSnap(t *testing.T, c *core.Core, id int) prox.Snapshot {
	t.Helper()
	pbs, _, _ := c.PBS("local")
	snaps, err := pbs.Snapshots(context.Background(), "backup", "")
	if err != nil {
		t.Fatal(err)
	}
	var best prox.Snapshot
	for _, s := range snaps {
		if s.Type == "vm" && s.ID == strconv.Itoa(id) && s.Time > best.Time {
			best = s
		}
	}
	if best.Time == 0 {
		t.Fatalf("no backup of %d", id)
	}
	return best
}

func waitDiskJob(t *testing.T, c *core.Core, id string) core.DiskRestoreJob {
	t.Helper()
	for i := 0; i < 600; i++ {
		j, _ := c.DiskRestore(id)
		if j.Phase == "done" || j.Phase == "error" {
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	j, _ := c.DiskRestore(id)
	t.Fatalf("job hangs: %+v", j)
	return j
}

// The data disk of VM 101 comes back from a backup and is attached next to the current one;
// the helper VM with the other disks is gone afterwards.
func TestDiskRestore(t *testing.T) {
	old := fake.RestoreDuration
	fake.RestoreDuration = 200 * time.Millisecond
	t.Cleanup(func() { fake.RestoreDuration = old })
	c := newCore(t)
	c.EnsureStorage = func(context.Context, *prox.PVE, core.PVECluster, core.PBSServer, string, string) (string, error) {
		return "pbs-appliance", nil
	}
	ctx := context.Background()
	snap := newestSnap(t, c, 101)
	if d := core.BackupDisks(snap.Files); len(d) != 2 || d["scsi1"] != 500<<30 {
		t.Fatalf("disks: %v", d)
	}
	pve, _, _ := c.PVE("cluster")
	before, _ := pve.Guests(ctx)

	req := core.DiskRestoreRequest{Cluster: "cluster", Server: "local", Store: "backup", Source: 101, Time: snap.Time, Drive: "scsi1", Target: 101, Storage: "local-lvm", By: "admin"}
	job, errs, err := c.StartDiskRestore(ctx, req)
	if err != nil || errs != nil {
		t.Fatal(errs, err)
	}
	if job.Node != "pve1" || job.TempID < 100 || job.Size != 500<<30 {
		t.Fatalf("job: %+v", job)
	}
	j := waitDiskJob(t, c, job.ID)
	if j.Phase != "done" || j.NewDisk != "scsi2" || j.Note != "" {
		t.Fatalf("done: %+v", j)
	}
	cfg, err := pve.GuestConfig(ctx, "pve1", "qemu", 101)
	if err != nil || !strings.Contains(cfg["scsi2"].(string), "local-lvm:vm-101-disk-") || !strings.Contains(cfg["scsi1"].(string), "tank:vm-101-disk-1") {
		t.Fatalf("target config: %v %v", cfg, err)
	}
	after, _ := pve.Guests(ctx)
	if len(after) != len(before) {
		t.Fatalf("helper VM left: %d guests before, %d after", len(before), len(after))
	}
	if !strings.Contains(eventTitles(t, c), "Disk scsi1 aus der Sicherung von 101 an VM 101 angehängt") {
		t.Fatalf("no message:\n%s", eventTitles(t, c))
	}

	// Into another VM: the system disk of 101 goes to 100 as its next free scsi slot.
	req.Drive, req.Target = "scsi0", 100
	job, errs, err = c.StartDiskRestore(ctx, req)
	if err != nil || errs != nil {
		t.Fatal(errs, err)
	}
	if j := waitDiskJob(t, c, job.ID); j.Phase != "done" || j.NewDisk != "scsi1" {
		t.Fatalf("other VM: %+v", j)
	}

	// Wrong input comes back per field.
	_, errs, err = c.StartDiskRestore(ctx, core.DiskRestoreRequest{Cluster: "cluster", Server: "local", Store: "backup", Source: 101, Time: snap.Time, Drive: "scsi7", Target: 110, Storage: ""})
	if err != nil || errs["drive"] == "" || errs["target"] == "" || errs["storage"] == "" {
		t.Fatalf("errors: %v %v", errs, err)
	}
	_, errs, err = c.StartDiskRestore(ctx, core.DiskRestoreRequest{Cluster: "cluster", Server: "local", Store: "backup", Source: 101, Time: snap.Time, Drive: "scsi0", Target: 101, Storage: "nas"})
	if err != nil || errs["storage"] == "" {
		t.Fatalf("storage: %v %v", errs, err)
	}
	if _, _, err := c.StartDiskRestore(ctx, core.DiskRestoreRequest{Cluster: "cluster", Server: "local", Store: "backup", Source: 101, Time: 1, Drive: "scsi0", Target: 101, Storage: "local-lvm"}); err == nil {
		t.Fatal("missing backup accepted")
	}
}

func TestFreeDisk(t *testing.T) {
	cfg := map[string]any{"scsi0": "x", "scsi1": "x", "virtio0": "x", "ide2": "cdrom"}
	for drive, want := range map[string]string{"scsi1": "scsi2", "virtio0": "virtio1", "sata3": "sata0", "ide0": "ide0", "efidisk0": "scsi2"} {
		if got := core.FreeDiskForTest(cfg, drive); got != want {
			t.Errorf("%s: %s, want %s", drive, got, want)
		}
	}
	full := map[string]any{}
	for i := 0; i < 4; i++ {
		full["ide"+strconv.Itoa(i)] = "x"
	}
	if got := core.FreeDiskForTest(full, "ide1"); got != "scsi0" {
		t.Errorf("full ide: %s", got)
	}
}
