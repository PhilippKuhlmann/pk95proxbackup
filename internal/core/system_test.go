package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/fake"
	"pk95proxbackup/internal/prox"
	"pk95proxbackup/internal/store"
)

func findHealth(t *testing.T, c *core.Core, disk string) core.DiskHealthState {
	t.Helper()
	for _, h := range c.DiskHealths() {
		if h.Disk == disk || strings.HasSuffix(h.ID, "/"+disk) {
			return h
		}
	}
	t.Fatalf("disk %s not found", disk)
	return core.DiskHealthState{}
}

func eventTitles(t *testing.T, c *core.Core) string {
	t.Helper()
	evs, err := c.St.Events(context.Background(), "", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range evs {
		b.WriteString(e.Title + "\n")
	}
	return b.String()
}

func TestDiskHealth(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	c.CheckDisksNow(ctx)

	a := findHealth(t, c, "sdc")
	if a.Status != "ok" || !a.Present || a.Smart.PowerOnHours != 8760 || a.Smart.Temperature != 34 {
		t.Fatalf("disk A: %+v", a)
	}
	if a.Rotation != "demo/a1" || !strings.Contains(a.Use, "Platte A") {
		t.Fatalf("disk A must be recognised as rotation disk: %q %q", a.Rotation, a.Use)
	}
	spare := findHealth(t, c, "sdd")
	if spare.Status != "warning" || spare.Smart.Reallocated != 8 || len(spare.Problems) != 1 {
		t.Fatalf("spare disk: %+v", spare)
	}
	if v := findHealth(t, c, "sdb"); v.Status != "unknown" || !v.Virtual {
		t.Fatalf("virtual disk: %+v", v)
	}
	if u := findHealth(t, c, "sde"); u.Status != "unknown" || !strings.Contains(u.Note, "USB") {
		t.Fatalf("usb disk without SMART: %+v", u)
	}
	ssd := findHealth(t, c, "2117E5A1B2C3")
	if ssd.Status != "warning" || ssd.Smart.Wearout != 8 || ssd.Host != "pve2" {
		t.Fatalf("worn SSD: %+v", ssd)
	}
	nvme := findHealth(t, c, "S5GXNF0R123456")
	if nvme.Status != "ok" || nvme.Smart.Wearout != 97 || nvme.Smart.PowerOnHours != 9120 || nvme.Smart.MediaErrors != 0 {
		t.Fatalf("nvme: %+v", nvme)
	}
	// Which disks matter for the backup: the appliance runs as VM 120 on pve1 with its system on
	// local-lvm (NVMe) and its datastore disk on the ZFS pool tank (WD Red). pve2 holds nothing of it.
	if !nvme.Relevant || !strings.Contains(nvme.Use, "scsi0") {
		t.Fatalf("nvme under the appliance: %+v", nvme)
	}
	if wd := findHealth(t, c, "WD-WCC7K1234567"); !wd.Relevant || !strings.Contains(wd.Use, "scsi1") || !strings.Contains(wd.Use, "tank") {
		t.Fatalf("WD Red under the datastore disk: relevant %v, use %q", wd.Relevant, wd.Use)
	}
	if ssd.Relevant || findHealth(t, c, "ZGY8ABCD").Relevant {
		t.Fatal("disks of pve2 have nothing to do with the backup")
	}
	if findHealth(t, c, "sdb").Relevant || !a.Relevant || !spare.Relevant {
		t.Fatal("relevance on the backup server")
	}
	titles := eventTitles(t, c)
	if !strings.Contains(titles, "Festplatte zeigt erste Fehler: Seagate Expansion HDD") {
		t.Fatalf("warnings must be reported:\n%s", titles)
	}
	if strings.Contains(titles, "CT500MX500SSD1") {
		t.Fatalf("a disk without relation to the backup must stay quiet:\n%s", titles)
	}

	// The rotation page shows the health of disk A.
	for _, r := range c.RotationStates(ctx) {
		for _, d := range r.Disks {
			if d.Key == "a1" && (d.Health == nil || d.Health.Status != "ok") {
				t.Fatalf("rotation disk health: %+v", d.Health)
			}
		}
	}

	// The spare disk gets worse: unreadable sectors → replace it.
	fake.Current.Local.SetSmart("sdd", fake.SmartATA("passed", 8, 3, 21510, 38))
	c.CheckDisksNow(ctx)
	if spare = findHealth(t, c, "sdd"); spare.Status != "error" || spare.Smart.Pending != 3 {
		t.Fatalf("spare disk after more errors: %+v", spare)
	}
	if !strings.Contains(eventTitles(t, c), "Festplatte bald tauschen: Seagate Expansion HDD") {
		t.Fatal("error must be reported")
	}
	// No change: no further event.
	before := strings.Count(eventTitles(t, c), "Seagate")
	c.CheckDisksNow(ctx)
	if n := strings.Count(eventTitles(t, c), "Seagate"); n != before {
		t.Fatalf("unchanged disk reported again (%d → %d)", before, n)
	}
}

func TestEvaluateSmart(t *testing.T) {
	r := prox.NewSmartReport()
	if s, p := core.EvaluateSmart(r); s != "ok" || len(p) != 0 {
		t.Fatalf("unknown values: %s %v", s, p)
	}
	r.Health = "failed"
	if s, _ := core.EvaluateSmart(r); s != "error" {
		t.Fatalf("failed self test must be an error, got %s", s)
	}
	r = prox.NewSmartReport()
	r.Reallocated, r.Temperature = 1, 65
	s, p := core.EvaluateSmart(r)
	if s != "warning" || len(p) != 2 || !strings.Contains(p[0], "1 defekter Sektor wurde") {
		t.Fatalf("got %s %v", s, p)
	}
}

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"1.5.0", "1.4.0", true}, {"1.4.0", "1.4.0", false}, {"1.10.0", "1.9.3", true}, {"v2.0", "1.99.9", true},
		{"1.4.0", "1.5.0", false}, {"1.5.0", "dev", false}, {"1.5.0-rc1", "1.4.9", true}} {
		if got := core.NewerVersion(c.a, c.b); got != c.want {
			t.Errorf("NewerVersion(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestUpdateInstall(t *testing.T) {
	c := newCore(t)
	gh := fake.NewGitHub("1.5.0")
	defer gh.Close()
	oldAPI, oldWeb := core.GitHubAPI, core.GitHubWeb
	core.GitHubAPI, core.GitHubWeb = gh.URL, gh.URL
	defer func() { core.GitHubAPI, core.GitHubWeb = oldAPI, oldWeb }()
	c.Version = "1.4.0"
	got := make(chan []byte, 1)
	c.InstallBinary = func(_ context.Context, bin []byte, version string) error {
		if version != "1.5.0" {
			t.Errorf("version %q", version)
		}
		got <- bin
		return nil
	}
	info, err := c.CheckUpdate(context.Background())
	if err != nil || !info.Available || info.Latest != "1.5.0" || !strings.Contains(info.Notes, "SMART") {
		t.Fatalf("check: %+v %v", info, err)
	}
	if !strings.Contains(eventTitles(t, c), "Version 1.5.0 verfügbar") {
		t.Fatal("a new version must be announced")
	}
	if err := c.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	select {
	case bin := <-got:
		if !bytes.Equal(bin, gh.Binary) {
			t.Fatal("installed binary differs from the release")
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("not installed: %+v", c.Update())
	}

	// A checksum that does not match: nothing is installed.
	gh.BadSum = true
	deadline := time.Now().Add(10 * time.Second)
	for c.InstallUpdate() != nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond) // the first install is still finishing
	}
	for c.Update().Phase != "failed" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if u := c.Update(); u.Phase != "failed" || !strings.Contains(u.PhaseError, "Prüfsumme") {
		t.Fatalf("bad checksum: %+v", u)
	}
	select {
	case <-got:
		t.Fatal("binary with a wrong checksum was installed")
	default:
	}
}

func TestUpdateGuardRollsBack(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pk95proxbackup")
	_ = os.WriteFile(exe, []byte("new"), 0o755)
	_ = os.WriteFile(exe+".old", []byte("old"), 0o755)
	m, _ := json.Marshal(map[string]any{"from": "1.4.0", "to": "1.5.0", "exe": exe})
	_ = os.WriteFile(filepath.Join(dir, "update-pending.json"), m, 0o600)
	var started string
	defer core.SetExecForTest(func(p string, _ []string, _ []string) error { started = p; return nil })()
	for i := 0; i < 3; i++ {
		core.UpdateGuard(dir, "1.5.0")
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" || started != "" {
		t.Fatal("three starts are allowed")
	}
	core.UpdateGuard(dir, "1.5.0") // fourth start: back to the old program
	if b, _ := os.ReadFile(exe); string(b) != "old" || started != exe {
		t.Fatalf("not rolled back: %q started %q", b, started)
	}
	if _, err := os.Stat(filepath.Join(dir, "update-rolledback.json")); err != nil {
		t.Fatal("the rollback must be reported after the start")
	}
	if _, err := os.Stat(filepath.Join(dir, "update-pending.json")); err == nil {
		t.Fatal("marker must be gone")
	}
}

func TestSelfBackupRoundTrip(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "etc/proxmox-backup"), 0o700)
	_ = os.WriteFile(filepath.Join(root, "etc/proxmox-backup/datastore.cfg"), []byte("datastore: backup\n\tpath /mnt/datastore/backup\n"), 0o600)
	_ = os.WriteFile(filepath.Join(root, "etc/proxmox-backup/.datastore.lck"), nil, 0o600)
	oldRoot := core.SystemRoot
	core.SystemRoot = root
	defer func() { core.SystemRoot = oldRoot }()

	if err := c.SetSelfBackup(true, ""); err == nil {
		t.Fatal("enabling without a password must fail")
	}
	if err := c.SetSelfBackup(true, "zu-kurz"); err == nil {
		t.Fatal("short password must be refused")
	}
	const pw = "Pferd-Batterie-Heftklammer"
	if err := c.SetSelfBackup(true, pw); err != nil {
		t.Fatal(err)
	}
	// Older backups beyond the 14 kept ones are removed.
	for i := 1; i <= 15; i++ {
		fake.Current.Local.AddBlob("backup", "host", "pk95proxbackup", time.Now().Add(-time.Duration(i)*24*time.Hour).Unix(), "pk95proxbackup.conf.blob", []byte("alt"))
	}
	_ = c.St.SetSetting("marker", "vorher")
	if err := c.RunSelfBackup(ctx); err != nil {
		t.Fatal(err)
	}
	info := c.SelfBackup()
	if info.LastOK == 0 || len(info.Targets) != 2 || info.LastError != "" {
		t.Fatalf("backup: %+v", info)
	}
	found := c.FindConfigBackups(ctx)
	inBackup := 0
	for _, f := range found {
		if f.Store == "backup" {
			inBackup++
		}
	}
	if inBackup != 14 {
		t.Fatalf("kept %d backups in datastore backup, want 14", inBackup)
	}
	newest := found[0]
	_ = c.St.SetSetting("marker", "nachher")

	data, err := c.LoadConfigBackup(ctx, newest.ServerID, newest.Store, newest.Time)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("PK95CFG1")) || bytes.Contains(data, []byte("vorher")) {
		t.Fatal("backup must be encrypted")
	}
	if _, err := c.RestoreConfig(ctx, data, "falsches-passwort-123"); err == nil || !strings.Contains(err.Error(), "Passwort") {
		t.Fatalf("wrong password: %v", err)
	}
	restarted := make(chan bool, 1)
	c.Restart = func() { restarted <- true }
	man, err := c.RestoreConfig(ctx, data, pw)
	if err != nil {
		t.Fatal(err)
	}
	if !man.PBS || man.Files < 2 {
		t.Fatalf("manifest: %+v", man)
	}
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Fatal("restore must restart the program")
	}
	// At the next start the restored database replaces the current one.
	if err := core.ApplyStagedRestore(c.DataDir); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(c.DataDir, "pk95proxbackup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var marker string
	_ = st.GetSetting("marker", &marker)
	if marker != "vorher" {
		t.Fatalf("restored marker %q", marker)
	}
	if _, err := os.Stat(filepath.Join(c.DataDir, "restore-staging")); err == nil {
		t.Fatal("staging must be cleaned up")
	}
}

func TestDiskLabel(t *testing.T) {
	for _, c := range []struct{ vendor, model, want string }{
		{"INTEL SS", "INTEL_SSDSC2KB960G8", "INTEL SSDSC2KB960G8"},
		{"unknown", "SanDisk Extreme Pro 500GB", "SanDisk Extreme Pro 500GB"},
		{"ATA", "WDC_WD30EZRX-00D8PB0", "WDC WD30EZRX-00D8PB0"},
		{"Seagate", "Expansion HDD", "Seagate Expansion HDD"},
		{"QEMU", "QEMU HARDDISK", "QEMU HARDDISK"},
		{"", "", "sdz"},
	} {
		if got := core.DiskLabel(core.DiskHealth{Vendor: c.vendor, Model: c.model, Disk: "/dev/sdz"}); got != c.want {
			t.Errorf("DiskLabel(%q, %q) = %q, want %q", c.vendor, c.model, got, c.want)
		}
	}
}
