package core_test

import (
	"context"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/notify"
)

// Two sets with a disk of the same name: each disk gets its own copies.
func TestMonthlyUSBBySet(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	rot, _ := c.Rotation("demo")
	other := core.Rotation{ID: "zwei", Name: "Büro", ServerID: "local", SourceServer: "local", SourceStore: "backup", Rhythm: "daily", Time: "22:00",
		Disks: []core.RotDisk{{Key: "x1", Store: "usb-x", Label: rot.Disks[0].Label}}}
	_ = c.St.Put(core.KindRotation, other.ID, other)
	done := func(ref, label string) {
		c.EventDetails("info", "USB-Sicherung", "USB-Sicherung fertig", "", &notify.Details{Kind: "usb-done", Ref: ref,
			Facts: []notify.Fact{{Label: "Festplatte", Value: "„" + label + "“"}}})
	}
	done("demo/"+rot.Disks[0].Key, rot.Disks[0].Label)
	done("demo/"+rot.Disks[0].Key, rot.Disks[0].Label)
	done("zwei/x1", rot.Disks[0].Label)
	rep, err := c.BuildMonthlyReport(ctx, time.Now().Format("2006-01"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, u := range rep.USB {
		got[u.Set+"|"+u.Label] = u.Copies
	}
	if got["USB täglich|"+rot.Disks[0].Label] != 2 || got["Büro|"+rot.Disks[0].Label] != 1 {
		t.Fatalf("copies per disk: %v", got)
	}
}

// "Run now" of a job with one guest is counted for the job.
func TestRunNowCountsForJob(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	pve, _, _ := c.PVE("cluster")
	_ = pve.CreateBackupJob(ctx, map[string]any{"id": "backup-dc", "schedule": "23:00", "storage": "pbs-appliance", "vmid": "100", "enabled": 1})
	upids, _, err := c.RunBackupJob(ctx, "cluster", "backup-dc")
	if err != nil || len(upids) != 1 {
		t.Fatal(upids, err)
	}
	if got := c.StartedJobForTest(upids[0]); got != "backup-dc" {
		t.Fatalf("task not remembered for its job: %q", got)
	}
}

// A deleted set leaves nothing behind in the settings.
func TestForgetRotation(t *testing.T) {
	c := newCore(t)
	rot, _ := c.Rotation("demo")
	keys := []string{"directwarn:demo", "usbspace-day:demo", "remind:demo", "plan:demo:wrong", "directproblem:demo:Sicherungsjob fehlt", "rt:demo/" + rot.Disks[0].Key}
	for _, k := range keys {
		_ = c.St.SetSetting(k, 1)
	}
	_ = c.St.SetSetting("plan:demo2:wrong", 1) // another set whose id starts the same
	c.ForgetRotation(rot)
	for _, k := range keys {
		var v any
		if err := c.St.GetSetting(k, &v); err == nil && v != nil {
			t.Errorf("%s left", k)
		}
	}
	var v any
	if err := c.St.GetSetting("plan:demo2:wrong", &v); err != nil || v == nil {
		t.Error("another set's setting removed")
	}
}
