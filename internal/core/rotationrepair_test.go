package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/fake"
)

// waitDisk polls the rotation state (which follows the copy jobs) until ok returns true.
func waitDisk(t *testing.T, c *core.Core, what string, ok func(core.DiskState) bool) core.DiskState {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		st := c.RotationStates(context.Background())[0].Disks[0]
		if ok(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %+v", what, st)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func TestDamagedDiskRebuild(t *testing.T) {
	sd, vd := fake.SyncDuration, fake.VerifyDuration
	fake.SyncDuration, fake.VerifyDuration = time.Second, time.Second
	t.Cleanup(func() { fake.SyncDuration, fake.VerifyDuration = sd, vd })
	c := newCore(t)
	ctx := context.Background()
	rot, _ := c.Rotation("demo")
	rot.TransferLast, rot.KeepLast, rot.KeepWeekly = 1, 1, 0
	_ = c.St.Put(core.KindRotation, rot.ID, rot)
	disk := rot.Disks[0] // usb-a on sdc, plugged in

	// The files on the disk read back as garbage: the copy fails and the disk is marked.
	fake.Current.Local.SetCorrupt("usb-a", true)
	start := time.Now().Unix()
	if err := c.RunRotationDisk(ctx, rot, disk); err != nil {
		t.Fatal(err)
	}
	st := waitDisk(t, c, "copy should fail", func(d core.DiskState) bool { return d.LastFail >= start && d.Damaged != "" })
	if st.State != "error" || !strings.Contains(st.Damaged, "valid UTF-8") || strings.Contains(st.Damaged, "2026-") {
		t.Fatalf("damage: %+v", st)
	}
	if !strings.Contains(eventTitles(t, c), "fehlgeschlagen: Festplatte beschädigt") {
		t.Fatalf("damage not reported:\n%s", eventTitles(t, c))
	}

	// "Neu aufsetzen": formatted, filled anew, read back from the disk.
	if err := c.RebuildDisk(ctx, rot, disk, false); err != nil {
		t.Fatal(err)
	}
	st = waitDisk(t, c, "rebuilt disk should be ready", func(d core.DiskState) bool { return d.State == "ready" })
	if st.Damaged != "" || st.RepairFailed || st.LastOK < start {
		t.Fatalf("after rebuild: %+v", st)
	}
	titles := eventTitles(t, c)
	if !strings.Contains(titles, "Festplatte „Platte A (gerade Tage)“ neu aufgesetzt") {
		t.Fatalf("rebuild not reported:\n%s", titles)
	}
	evs, _ := c.St.Events(ctx, "", 0, 20)
	checked := false
	for _, e := range evs {
		if strings.Contains(e.Title, "fertig") && strings.Contains(string(e.Details), "zurückgelesen und in Ordnung") {
			checked = true
		}
	}
	if !checked {
		t.Fatal("the copy after the rebuild must be read back")
	}

	// A broken enclosure: the rebuilt disk is bad again when read back → no endless repairs.
	fake.Current.Local.SetBadEnclosure("sdc", true)
	if err := c.RebuildDisk(ctx, rot, disk, false); err != nil {
		t.Fatal(err)
	}
	st = waitDisk(t, c, "check should fail", func(d core.DiskState) bool { return d.RepairFailed && d.Phase == "" })
	if st.Damaged == "" || !strings.Contains(eventTitles(t, c), "speichert nicht zuverlässig") {
		t.Fatalf("bad enclosure: %+v\n%s", st, eventTitles(t, c))
	}
	// Even with automatic repair it is not formatted again.
	rot.AutoRepair = true
	_ = c.St.Put(core.KindRotation, rot.ID, rot)
	fails := strings.Count(eventTitles(t, c), "neu aufgesetzt")
	start = time.Now().Unix()
	if err := c.RunRotationDisk(ctx, rot, disk); err != nil {
		t.Fatal(err)
	}
	waitDisk(t, c, "copy should fail again", func(d core.DiskState) bool { return d.LastFail >= start })
	time.Sleep(2 * time.Second)
	if n := strings.Count(eventTitles(t, c), "neu aufgesetzt"); n != fails {
		t.Fatalf("formatted again although the enclosure is broken (%d → %d)", fails, n)
	}
}

// A failure from before the damage check (already seen) is looked at once, silently.
func TestDamageFoundInOldFailure(t *testing.T) {
	sd := fake.SyncDuration
	fake.SyncDuration = time.Second
	t.Cleanup(func() { fake.SyncDuration = sd })
	c := newCore(t)
	ctx := context.Background()
	rot, _ := c.Rotation("demo")
	fake.Current.Local.SetCorrupt("usb-a", true)
	start := time.Now().Unix()
	_ = c.RunRotationDisk(ctx, rot, rot.Disks[0])
	waitDisk(t, c, "copy should fail", func(d core.DiskState) bool { return d.LastFail >= start })
	// Forget the damage as an older version would not have noted it.
	key := "rt:demo/" + rot.Disks[0].Key
	var rt map[string]any
	_ = c.St.GetSetting(key, &rt)
	delete(rt, "damaged")
	_ = c.St.SetSetting(key, rt)
	c2 := core.New(c.St, c.DataDir, "test", c.Signer)
	c2.LocalRunner = c.LocalRunner
	before := strings.Count(eventTitles(t, c2), "beschädigt")
	st := c2.RotationStates(ctx)[0].Disks[0]
	if st.Damaged == "" {
		t.Fatalf("old failure not looked at: %+v", st)
	}
	if strings.Count(eventTitles(t, c2), "beschädigt") != before {
		t.Fatal("the old failure must not be reported again")
	}
}

func TestAutoRepair(t *testing.T) {
	sd, vd := fake.SyncDuration, fake.VerifyDuration
	fake.SyncDuration, fake.VerifyDuration = time.Second, time.Second
	t.Cleanup(func() { fake.SyncDuration, fake.VerifyDuration = sd, vd })
	c := newCore(t)
	ctx := context.Background()
	rot, _ := c.Rotation("demo")
	// Only allowed when the disk holds just the newest backup.
	rot.AutoRepair = true
	if errs := rot.Validate(); errs["auto_repair"] == "" {
		t.Fatal("auto repair must need a single copy")
	}
	rot.TransferLast, rot.KeepLast, rot.KeepWeekly = 1, 1, 0
	if errs := rot.Validate(); len(errs) > 0 {
		t.Fatal(errs)
	}
	_ = c.St.Put(core.KindRotation, rot.ID, rot)
	fake.Current.Local.SetCorrupt("usb-a", true)
	start := time.Now().Unix()
	if err := c.RunRotationDisk(ctx, rot, rot.Disks[0]); err != nil {
		t.Fatal(err)
	}
	// It fails, repairs itself, copies again and checks.
	st := waitDisk(t, c, "should repair itself", func(d core.DiskState) bool { return d.State == "ready" && d.LastOK > start })
	if st.Damaged != "" || st.RepairAt < start {
		t.Fatalf("auto repair: %+v", st)
	}
	if !strings.Contains(eventTitles(t, c), "Automatisch") && !strings.Contains(strings.Join(eventBodies(t, c), "\n"), "Automatisch wurde die Platte") {
		t.Fatalf("auto repair not reported:\n%s", eventTitles(t, c))
	}
}

func eventBodies(t *testing.T, c *core.Core) []string {
	t.Helper()
	evs, _ := c.St.Events(context.Background(), "", 0, 100)
	var out []string
	for _, e := range evs {
		out = append(out, e.Body)
	}
	return out
}
