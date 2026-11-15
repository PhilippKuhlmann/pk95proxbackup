package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/fake"
	"pk95proxbackup/internal/prox"
)

// As PBS01 answered on 13.11.2026: 4.2.6 installed and running, 4.2.10 available.
func TestServerHealthAvailableNotInstalled(t *testing.T) {
	versions := []prox.AptPackage{
		{Package: "proxmox-backup", Version: "4.2.0", OldVersion: "4.2.0", ExtraInfo: "running kernel: 7.0.14-17-pve"},
		{Package: "proxmox-backup-server", Version: "4.2.10-1", OldVersion: "4.2.6-1", ExtraInfo: "running version: 4.2.6"},
		{Package: "proxmox-kernel-7.0.14-17-pve-signed", Version: "7.0.14-17", OldVersion: "7.0.14-17"},
		{Package: "proxmox-kernel-7.0", Version: "7.0.14-26", OldVersion: "7.0.14-17"},
		{Package: "proxmox-kernel-6.14.11-9-pve-signed", Version: "6.14.11-9", OldVersion: "6.14.11-9"},
	}
	updates := []prox.AptPackage{{Package: "tzdata", OldVersion: "a", Version: "b"}, {Package: "proxmox-backup-server", OldVersion: "4.2.6-1", Version: "4.2.10-1"}}
	h := core.EvaluateServerHealth(versions, updates, "7.0.14-17-pve")
	if h.RestartNeeded || h.RebootNeeded {
		t.Fatalf("nothing to restart: %+v", h)
	}
	if h.Running != "4.2.6" || h.Installed != "4.2.6-1" || h.Available != "4.2.10-1" || h.Updates != 2 {
		t.Fatalf("%+v", h)
	}
	if !strings.HasPrefix(h.UpdateList[0], "proxmox-backup-server") {
		t.Fatalf("PBS update first: %v", h.UpdateList)
	}
}

func TestServerHealthRestartAndReboot(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	h, err := c.CheckServerHealth(ctx, "buero") // upgraded to 4.2.10, still running 4.2.6, new kernel
	if err != nil {
		t.Fatal(err)
	}
	if !h.RestartNeeded || !h.RebootNeeded || h.NewestKernel != "7.0.14-26-pve" {
		t.Fatalf("%+v", h)
	}
	titles := eventTitles(t, c)
	if !strings.Contains(titles, "Dienste laufen noch mit der alten Version") || !strings.Contains(titles, "Neustart nötig") {
		t.Fatalf("not reported:\n%s", titles)
	}
	h, err = c.ReloadServerServices(ctx, "buero")
	if err != nil {
		t.Fatal(err)
	}
	if h.RestartNeeded || h.Running != "4.2.10" || !h.RebootNeeded {
		t.Fatalf("after reload: %+v", h)
	}
	if l, _ := c.CheckServerHealth(ctx, "local"); l.RestartNeeded || l.RebootNeeded || l.Updates != 3 {
		t.Fatalf("local: %+v", l)
	}
}

// Updates are installed in the background; afterwards a reboot brings the new kernel.
func TestSystemUpgrade(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	oldD, oldP := fake.UpgradeDuration, core.UpgradePoll
	fake.UpgradeDuration, core.UpgradePoll = 400*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { fake.UpgradeDuration, core.UpgradePoll = oldD, oldP })
	h, err := c.RefreshPackages(ctx, "local")
	if err != nil || h.Updates != 3 || h.RebootNeeded {
		t.Fatalf("before: %+v %v", h, err)
	}
	if _, err := c.StartUpgrade(ctx, "buero", "admin"); !errors.Is(err, core.ErrNoShell) {
		t.Fatalf("server without shell: %v", err)
	}
	u, err := c.StartUpgrade(ctx, "local", "admin")
	if err != nil || u.State != "running" || u.By != "admin" {
		t.Fatalf("start: %+v %v", u, err)
	}
	if _, err := c.StartUpgrade(ctx, "local", "admin"); err == nil {
		t.Fatal("second upgrade started")
	}
	if _, err := c.RebootServer(ctx, "local", "admin", true); err == nil {
		t.Fatal("reboot while upgrading")
	}
	for end := time.Now().Add(10 * time.Second); u.State == "running" && time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		u, _ = c.UpgradeStatus(ctx, "local")
	}
	if u.State != "ok" || u.Upgraded != 3 || len(u.Log) == 0 {
		t.Fatalf("after: %+v", u)
	}
	h, _ = c.CheckServerHealth(ctx, "local")
	if h.Updates != 0 || !h.RebootNeeded || h.NewestKernel != "7.0.14-28-pve" {
		t.Fatalf("health after upgrade: %+v", h)
	}
	if n := strings.Count(eventTitles(t, c), "Updates eingespielt\n"); n != 1 {
		t.Fatalf("reported %d times:\n%s", n, eventTitles(t, c))
	}
	// reboot: refused while work runs (unless forced), then the new kernel runs
	pbs, _, _ := c.PBS("local")
	if _, err := pbs.Verify(ctx, "backup"); err != nil {
		t.Fatal(err)
	}
	if busy, err := c.RebootServer(ctx, "local", "admin", false); !errors.Is(err, core.ErrBusy) || len(busy) == 0 || !strings.Contains(busy[0], "Prüfung") {
		t.Fatalf("reboot while verifying: %v %v", busy, err)
	}
	if _, err := c.RebootServer(ctx, "local", "admin", true); err != nil {
		t.Fatal(err)
	}
	if fake.Current.Local.Reboots() != 1 {
		t.Fatal("not rebooted")
	}
	if h, _ = c.CheckServerHealth(ctx, "local"); h.RebootNeeded || h.Kernel != "7.0.14-28-pve" {
		t.Fatalf("after reboot: %+v", h)
	}
}

// Automatic updates: install at the planned time, reboot only when nothing runs; a reboot
// that cannot be done in the window waits for the next planned time.
func TestAutoUpdate(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	oldD, oldP, oldV := fake.UpgradeDuration, core.UpgradePoll, fake.VerifyDuration
	fake.UpgradeDuration, core.UpgradePoll, fake.VerifyDuration = 300*time.Millisecond, 50*time.Millisecond, time.Second
	t.Cleanup(func() { fake.UpgradeDuration, core.UpgradePoll, fake.VerifyDuration = oldD, oldP, oldV })

	if errs, _ := c.SetAutoUpdate("local", core.AutoUpdate{Enabled: true, Time: "04:30"}); errs["weekdays"] == "" {
		t.Fatalf("no day: %v", errs)
	}
	if errs, _ := c.SetAutoUpdate("buero", core.AutoUpdate{Enabled: true, Weekdays: []int{6}, Time: "04:30"}); errs["enabled"] == "" {
		t.Fatalf("without shell: %v", errs)
	}
	sat := time.Date(2026, 11, 21, 4, 31, 0, 0, time.Local) // a Saturday
	if errs, err := c.SetAutoUpdate("local", core.AutoUpdate{Enabled: true, Weekdays: []int{6}, Time: "04:30", Reboot: true}); errs != nil || err != nil {
		t.Fatal(errs, err)
	}
	for _, v := range c.SystemUpdates() {
		if v.ServerID == "local" && (v.Next == 0 || !v.Auto.Enabled) {
			t.Fatalf("view: %+v", v)
		}
	}
	c.RunAutoUpdates(ctx, sat.Add(-2*time.Minute)) // before: nothing
	if u, _ := c.UpgradeStatus(ctx, "local"); u.State != "" {
		t.Fatalf("started too early: %+v", u)
	}
	c.RunAutoUpdates(ctx, sat)
	u, _ := c.UpgradeStatus(ctx, "local")
	if u.State != "running" || u.By != core.AutoUpdateBy {
		t.Fatalf("not started: %+v", u)
	}
	for end := time.Now().Add(10 * time.Second); u.State == "running" && time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		u, _ = c.UpgradeStatus(ctx, "local")
	}
	// a check runs: the reboot waits
	pbs, _, _ := c.PBS("local")
	if _, err := pbs.Verify(ctx, "backup"); err != nil {
		t.Fatal(err)
	}
	c.RunAutoUpdates(ctx, sat.Add(time.Minute)) // upgrade done → reboot due
	c.RunAutoUpdates(ctx, sat.Add(2*time.Minute))
	if fake.Current.Local.Reboots() != 0 {
		t.Fatal("rebooted during a check")
	}
	c.RunAutoUpdates(ctx, sat.Add(3*time.Hour+time.Minute)) // window over
	if !strings.Contains(eventTitles(t, c), "Automatischer Neustart verschoben") {
		t.Fatalf("not reported:\n%s", eventTitles(t, c))
	}
	// next Saturday: nothing to install, the pending reboot is done
	time.Sleep(1200 * time.Millisecond) // the check has ended
	next := sat.AddDate(0, 0, 7)
	c.RunAutoUpdates(ctx, next)
	c.RunAutoUpdates(ctx, next.Add(time.Minute))
	if fake.Current.Local.Reboots() != 1 {
		t.Fatalf("reboots %d", fake.Current.Local.Reboots())
	}
	for _, v := range c.SystemUpdates() {
		if v.ServerID == "local" && v.Postponed != "" {
			t.Fatalf("still postponed: %+v", v)
		}
	}
}
