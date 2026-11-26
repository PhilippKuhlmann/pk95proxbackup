package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/fake"
)

// directSet replaces the demo's copy set by a set whose disks Proxmox VE backs up onto directly:
// usb-a (plugged in) and usb-b (away), each with its storage in Proxmox VE.
func directSet(t *testing.T, c *core.Core) core.Rotation {
	t.Helper()
	ctx := context.Background()
	_ = c.St.Delete(core.KindRotation, "demo")
	pve, _, err := c.PVE("cluster")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"usb-a", "usb-b"} {
		if err := pve.AddPBSStorage(ctx, "pk95-"+s, "10.0.0.1", 8007, s, "pk95-pve@pbs!t", "secret", "", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	day := time.Now().AddDate(0, 0, -1).Unix()
	rot := core.Rotation{ID: "usb", Name: "USB direkt", ServerID: "local", Mode: "direct", Cluster: "cluster", Guests: "100, 101",
		Rhythm: "daily", Time: "21:00", UnmountOnDone: true, KeepDaily: 7, SourceStore: "backup", TransferLast: 3, RunOnMount: true,
		Disks: []core.RotDisk{{Key: "a", Store: "usb-a", Label: "A", Storage: "pk95-usb-a", Added: day}, {Key: "b", Store: "usb-b", Label: "B", Storage: "pk95-usb-b", Added: day}}}
	if errs := rot.Validate(); len(errs) > 0 {
		t.Fatalf("valid direct set: %v", errs)
	}
	if rot.SourceStore != "" || rot.TransferLast != 0 || rot.RunOnMount || rot.Guests != "100,101" {
		t.Fatalf("copy fields kept: %+v", rot)
	}
	if err := c.ApplyRotation(ctx, rot); err != nil {
		t.Fatal(err)
	}
	if err := c.St.Put(core.KindRotation, rot.ID, rot); err != nil {
		t.Fatal(err)
	}
	return rot
}

func directDisk(t *testing.T, c *core.Core, key string) core.DiskState {
	t.Helper()
	for _, st := range c.RotationStates(context.Background()) {
		for _, d := range st.Disks {
			if d.Key == key {
				return d
			}
		}
	}
	t.Fatalf("disk %s missing", key)
	return core.DiskState{}
}

func TestDirectUSBBackup(t *testing.T) {
	vd := fake.VzdumpDuration
	fake.VzdumpDuration = time.Second
	t.Cleanup(func() { fake.VzdumpDuration = vd })
	c := newCore(t)
	ctx := context.Background()
	rot := directSet(t, c)
	pve, _, _ := c.PVE("cluster")

	job := func() (storage, vmid string, all int) {
		jobs, _ := pve.BackupJobs(ctx)
		for _, j := range jobs {
			if j.ID == rot.BackupJobID() {
				return j.Storage, j.VMID, j.All
			}
		}
		t.Fatal("no backup job in Proxmox VE")
		return
	}
	if st, vmid, all := job(); st != "pk95-usb-a" || vmid != "100,101" || all != 0 {
		t.Fatalf("job: %s %s %d", st, vmid, all)
	}
	disabled := func() map[string]bool {
		out := map[string]bool{}
		list, _ := pve.Storages(ctx)
		for _, s := range list {
			out[s.Storage] = s.Disable != 0
		}
		return out
	}
	// The storage of the disk that is away is switched off.
	ov := c.BuildOverview(ctx)
	c.ScheduleDirectForTest(ctx, &ov, time.Now())
	if d := disabled(); d["pk95-usb-a"] || !d["pk95-usb-b"] {
		t.Fatalf("storages: %v", d)
	}

	// Back up now: the guests land on the disk, then it is tidied up and unmounted.
	start := time.Now().Unix()
	if err := c.RunRotationDisk(ctx, rot, rot.Disks[0]); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	ov = c.BuildOverview(ctx)
	c.RecordJobRunsForTest(ctx, &ov, time.Now().Add(time.Hour))
	deadline := time.Now().Add(30 * time.Second)
	for d := directDisk(t, c, "a"); d.State != "ready"; d = directDisk(t, c, "a") {
		if time.Now().After(deadline) {
			t.Fatalf("disk not ready: %+v\n%s", d, eventTitles(t, c))
		}
		time.Sleep(200 * time.Millisecond)
	}
	if d := directDisk(t, c, "a"); d.LastOK < start || d.Mounted || d.LastFail != 0 {
		t.Fatalf("after the backup: %+v", d)
	}
	pbs, _, _ := c.PBS("local")
	fake.Current.Local.SetMounted("usb-a", true) // to look inside
	snaps, err := pbs.Snapshots(ctx, "usb-a", "")
	fake.Current.Local.SetMounted("usb-a", false)
	if err != nil {
		t.Fatal(err)
	}
	fresh := map[string]bool{}
	for _, s := range snaps {
		if s.Time >= start {
			fresh[s.ID] = true
		}
	}
	if !fresh["100"] || !fresh["101"] || len(fresh) != 2 {
		t.Fatalf("backups on the disk: %v", fresh)
	}
	evs, _ := c.St.Events(ctx, "", 0, 30)
	report := false
	for _, e := range evs {
		if e.Title == "USB-Sicherung fertig – Festplatte „A“ kann abgezogen werden" && strings.Contains(string(e.Details), "2 Server") {
			report = true
		}
	}
	if !report {
		t.Fatalf("no report of the backup:\n%s", eventTitles(t, c))
	}

	// Pulled: a few hours before the next backup the appliance warns – once.
	if _, ok := fake.Current.Local.PullDisk("sdc"); !ok {
		t.Fatal("pull")
	}
	cal, _ := core.ParseCalendar("21:00")
	next := cal.Between(time.Now(), time.Now().Add(48*time.Hour), 1)[0]
	ov = c.BuildOverview(ctx)
	c.ScheduleDirectForTest(ctx, &ov, next.Add(-2*time.Hour))
	c.ScheduleDirectForTest(ctx, &ov, next.Add(-time.Hour))
	if n := strings.Count(eventTitles(t, c), "Keine USB-Festplatte angeschlossen"); n != 1 {
		t.Fatalf("warned %d times:\n%s", n, eventTitles(t, c))
	}
	if d := disabled(); !d["pk95-usb-a"] || !d["pk95-usb-b"] {
		t.Fatalf("storages with no disk plugged in: %v", d)
	}
	// A backup without a disk fails and is reported as such.
	if _, _, err := c.RunBackupJob(ctx, "cluster", rot.BackupJobID()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	ov = c.BuildOverview(ctx)
	c.RecordJobRunsForTest(ctx, &ov, time.Now().Add(2*time.Hour))
	if d := directDisk(t, c, "a"); d.LastFail < d.LastOK || d.LastError == "" {
		t.Fatalf("failed backup not noted: %+v", d)
	}
	if !strings.Contains(eventTitles(t, c), "USB-Sicherung auf „A“ fehlgeschlagen") {
		t.Fatalf("failure not reported:\n%s", eventTitles(t, c))
	}
}

// Disk B plugged in instead: the job follows it.
func TestDirectUSBFollowsDisk(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	rot := directSet(t, c)
	pve, _, _ := c.PVE("cluster")
	// The disk of B goes where the one of A was.
	disk, _ := fake.Current.Local.PullDisk("sdc")
	for i := range disk.Partitions {
		disk.Partitions[i].UUID = "8b2e4c71-6a3d-4e2f-b0c9-5d7a1f3e9c22"
	}
	fake.Current.Local.PlugDisk(disk)
	fake.Current.Local.SetMounted("usb-a", false)
	fake.Current.Local.SetMounted("usb-b", true)
	ov := c.BuildOverview(ctx)
	c.ScheduleDirectForTest(ctx, &ov, time.Now())
	jobs, _ := pve.BackupJobs(ctx)
	for _, j := range jobs {
		if j.ID == rot.BackupJobID() && j.Storage != "pk95-usb-b" {
			t.Fatalf("job still on %s", j.Storage)
		}
	}
	// Removing the set removes its job; the storages stay for restoring.
	if err := c.RemoveDirect(ctx, rot); err != nil {
		t.Fatal(err)
	}
	jobs, _ = pve.BackupJobs(ctx)
	for _, j := range jobs {
		if j.ID == rot.BackupJobID() {
			t.Fatal("job left behind")
		}
	}
	// A direct set needs a Proxmox VE and a time.
	bad := core.Rotation{Name: "x", ServerID: "local", Mode: "direct", Rhythm: "daily", Guests: "100;x"}
	errs := bad.Validate()
	if errs["cluster"] == "" || errs["time"] == "" || errs["guests"] == "" || errs["source_store"] != "" {
		t.Fatalf("validation: %v", errs)
	}
}
