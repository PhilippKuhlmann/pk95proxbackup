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

// The demo's copy set switched to backing up directly: the copied groups on the disk are handed
// to Proxmox VE's token first (else PBS refuses: owner check), and back when switched again.
func TestDirectUSBSwitchHandsOver(t *testing.T) {
	vd := fake.VzdumpDuration
	fake.VzdumpDuration = time.Second
	t.Cleanup(func() { fake.VzdumpDuration = vd })
	c := newCore(t)
	ctx := context.Background()
	rot, _ := c.Rotation("demo")
	for _, d := range rot.Disks {
		if err := c.RemoveRotationDisk(ctx, rot, d); err != nil {
			t.Fatal(err)
		}
	}
	pve, _, _ := c.PVE("cluster")
	if err := pve.AddPBSStorage(ctx, "pk95-usb-a", "10.0.0.1", 8007, "usb-a", "pk95-pve@pbs!usb", "secret", "", "", ""); err != nil {
		t.Fatal(err)
	}
	rot.Mode, rot.Cluster, rot.Guests, rot.Time = "direct", "cluster", "100", "21:00"
	rot.Disks[0].Storage = "pk95-usb-a"
	if errs := rot.Validate(); len(errs) > 0 {
		t.Fatal(errs)
	}
	if err := c.ApplyRotation(ctx, rot); err != nil {
		t.Fatal(err)
	}
	_ = c.St.Put(core.KindRotation, rot.ID, rot)
	pbs, _, _ := c.PBS("local")
	owners := func() map[string]bool {
		gs, err := pbs.Groups(ctx, "usb-a")
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, g := range gs {
			out[g.Owner] = true
		}
		return out
	}
	if o := owners(); o["pk95-pve@pbs!usb"] || len(o) == 0 {
		t.Fatalf("before: %v", o)
	}
	ov := c.BuildOverview(ctx)
	c.ScheduleDirectForTest(ctx, &ov, time.Now())
	if o := owners(); !o["pk95-pve@pbs!usb"] || len(o) != 1 {
		t.Fatalf("not handed over: %v", o)
	}
	if !strings.Contains(eventTitles(t, c), "Sicherungen auf „Platte A (gerade Tage)“ übergeben") {
		t.Fatalf("hand-over not reported:\n%s", eventTitles(t, c))
	}
	// Now Proxmox VE may add to the groups.
	start := time.Now().Unix()
	if err := c.RunRotationDisk(ctx, rot, rot.Disks[0]); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	ov = c.BuildOverview(ctx)
	c.RecordJobRunsForTest(ctx, &ov, time.Now().Add(time.Hour))
	if d := directDisk(t, c, rot.Disks[0].Key); d.LastOK < start || d.LastFail >= start {
		t.Fatalf("backup after the hand-over: %+v\n%s", d, eventTitles(t, c))
	}

	// And back to copying: the groups go back to the copy job.
	deadline := time.Now().Add(20 * time.Second)
	for directDisk(t, c, rot.Disks[0].Key).Phase != "" || directDisk(t, c, rot.Disks[0].Key).State == "running" {
		if time.Now().After(deadline) {
			t.Fatal("aftercare did not end")
		}
		time.Sleep(200 * time.Millisecond)
	}
	fake.Current.Local.SetMounted("usb-a", true)
	_ = c.RemoveDirect(ctx, rot)
	rot.Mode, rot.SourceServer, rot.SourceStore = "", "local", "backup"
	if errs := rot.Validate(); len(errs) > 0 {
		t.Fatal(errs)
	}
	_ = c.St.Put(core.KindRotation, rot.ID, rot)
	if err := c.ApplyRotation(ctx, rot); err != nil {
		t.Fatal(err)
	}
	c.ScheduleRotationsForTest(ctx, c.RotationStates(ctx), time.Now())
	if o := owners(); !o["root@pam"] || len(o) != 1 {
		t.Fatalf("not handed back: %v", o)
	}
}

// The demo's copy set: the logs of the backups tell data and change of every server.
func TestRotationSpace(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	ov := c.BuildOverview(ctx)
	c.RecordJobRunsForTest(ctx, &ov, time.Now())
	rot, _ := c.Rotation("demo")
	var st *core.RotationState
	for _, x := range c.RotationStates(ctx) {
		if x.ID == "demo" {
			st = &x
		}
	}
	e, err := c.RotationSpace(ctx, rot, st)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("demo set: %+v", e)
	// VM 100: 64 GiB disks, 46 % zeros; changes about 8 GiB a day
	if e.Guests < 4 || e.Base < 50<<30 || e.Base > 200<<30 || e.PerDay < 10<<30 || e.PerDay > 40<<30 {
		t.Fatalf("estimate: %+v", e)
	}
	if e.Keep < 7 || e.SpanDays < 14 || e.Smallest == 0 || e.Fits != "yes" || e.Need > e.Smallest {
		t.Fatalf("demo set should fit: %+v", e)
	}
	// Keeping everything: the disk only fills up.
	rot.KeepLast, rot.KeepWeekly = 0, 0
	e, _ = c.RotationSpace(ctx, rot, st)
	if !e.KeepAll || e.Fits != "no" || e.LastsDay <= 0 || e.LastsDay > 365 || e.Suggest == "" || e.MinDisk <= e.Smallest {
		t.Fatalf("keep all: %+v", e)
	}
	// Reported once a week, not every day.
	c.CheckSpaceForTest(ctx, rot, *st, time.Now().Add(12*time.Hour).Truncate(24*time.Hour).Add(10*time.Hour))
	c.CheckSpaceForTest(ctx, rot, *st, time.Now().Add(36*time.Hour).Truncate(24*time.Hour).Add(10*time.Hour))
	if n := strings.Count(eventTitles(t, c), "werden zu klein"); n != 1 {
		t.Fatalf("warned %d times:\n%s", n, eventTitles(t, c))
	}
}
