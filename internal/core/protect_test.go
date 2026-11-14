package core_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/fake"
	"pk95proxbackup/internal/prox"
)

func mustSnaps(t *testing.T, c *core.Core, store string) []prox.Snapshot {
	t.Helper()
	pbs, _, _ := c.PBS("local")
	s, err := pbs.Snapshots(context.Background(), store, "")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func protectedOf(t *testing.T, c *core.Core, store string) map[string]prox.Snapshot {
	t.Helper()
	out := map[string]prox.Snapshot{}
	for _, s := range mustSnaps(t, c, store) {
		if s.Protected {
			out[core.SnapKey("local", store, s)] = s
		}
	}
	return out
}

func TestAutoProtect(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	if err := c.SetProtectSettings(core.ProtectSettings{Months: 3}); err != nil {
		t.Fatal(err)
	}
	if err := c.RunAutoProtect(ctx); err != nil {
		t.Fatal(err)
	}
	prot := protectedOf(t, c, "backup")
	// month-end backups of vm 100 and ct 110 for 3 months, plus vm 250 / ct 251 from last month
	if n := len(prot); n < 6 || n > 8 {
		t.Fatalf("protected %d: %v", n, prot)
	}
	if len(protectedOf(t, c, "usb-a")) != 0 {
		t.Fatal("removable datastore touched")
	}
	if info := c.AutoProtectInfo(); info.Protected != len(prot) || info.Error != "" {
		t.Fatalf("info %+v", info)
	}
	pbs, _, _ := c.PBS("local")
	var one prox.Snapshot
	for _, s := range prot {
		one = s
		break
	}
	// a protected snapshot cannot be deleted
	if err := pbs.DeleteSnapshot(ctx, "backup", "", one.Type, one.ID, one.Time); err == nil {
		t.Fatal("protected snapshot deleted")
	}
	// lifted by hand: stays lifted
	if err := c.ProtectSnapshot(ctx, "local", "backup", one.Type, one.ID, one.Time, false); err != nil {
		t.Fatal(err)
	}
	if err := c.RunAutoProtect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, again := protectedOf(t, c, "backup")[core.SnapKey("local", "backup", one)]; again {
		t.Fatal("protection set again after lifting it by hand")
	}
	// switched off: everything the appliance protected is released
	_ = c.SetProtectSettings(core.ProtectSettings{})
	if err := c.RunAutoProtect(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(protectedOf(t, c, "backup")); n != 0 {
		t.Fatalf("still protected: %d", n)
	}
}

func TestStorageGuard(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	fake.Current.PVE.AddJob(prox.BackupJob{ID: "backup-weekly", Schedule: "sat 02:00", Storage: "pbs-appliance", All: 1, Comment: "Wöchentlich", PruneBackups: "keep-last=4"})
	guards, errs := c.StorageGuards(ctx)
	if len(errs) != 0 || len(guards) != 1 {
		t.Fatalf("guards %+v %v", guards, errs)
	}
	g := guards[0]
	if g.State != "open" || g.Server != "local" || len(g.Retention) != 1 || !strings.Contains(g.Retention[0], "Wöchentlich") {
		t.Fatalf("guard %+v", g)
	}
	g, err := c.SetStorageGuard(ctx, "cluster", "pbs-appliance", true, "test")
	if err != nil || g.State != "protected" {
		t.Fatalf("on: %+v %v", g, err)
	}
	roles := fake.ACLRoles("/datastore/backup", "pk95-pve@pbs!pve-cluster")
	if slices.Contains(roles, "DatastorePowerUser") || !slices.Contains(roles, "DatastoreBackup") || !slices.Contains(roles, "DatastoreReader") {
		t.Fatalf("token roles %v", roles)
	}
	g, err = c.SetStorageGuard(ctx, "cluster", "pbs-appliance", false, "test")
	if err != nil || g.State != "open" {
		t.Fatalf("off: %+v %v", g, err)
	}
}

func alarms(t *testing.T, c *core.Core) int {
	t.Helper()
	evs, _ := c.St.Events(context.Background(), "", 0, 50)
	n := 0
	for _, e := range evs {
		if e.Severity == "critical" && strings.Contains(e.Title, "Sicherungen auf backup gelöscht") {
			if !strings.Contains(e.Body, "von 1 Servern alle") {
				t.Fatalf("body: %s", e.Body)
			}
			n++
		}
	}
	return n
}

func TestDeletionAlarm(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	c.WatchDeletions(ctx) // first look
	pbs, _, _ := c.PBS("local")
	// a deletion through the appliance: no alarm
	for _, s := range mustSnaps(t, c, "backup") {
		if s.ID == "102" {
			if err := pbs.DeleteSnapshot(ctx, "backup", "", s.Type, s.ID, s.Time); err != nil {
				t.Fatal(err)
			}
			c.ForgetSnapshot("local", "backup", s.Type, s.ID)
			break
		}
	}
	c.WatchDeletions(ctx)
	if alarms(t, c) != 0 {
		t.Fatal("alarm for a deletion of the appliance")
	}
	// all backups of vm 101 gone behind the appliance's back
	for _, s := range mustSnaps(t, c, "backup") {
		if s.ID == "101" {
			if err := pbs.DeleteSnapshot(ctx, "backup", "", s.Type, s.ID, s.Time); err != nil {
				t.Fatal(err)
			}
		}
	}
	c.WatchDeletions(ctx)
	if alarms(t, c) != 1 {
		t.Fatal("no alarm when a whole guest disappeared")
	}
	c.WatchDeletions(ctx)
	if alarms(t, c) != 1 {
		t.Fatal("alarm repeated")
	}
}

// A copy still running at the end of its time window is stopped; one started after it is not.
func TestCopyWindow(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	pbs, _, _ := c.PBS("local")
	if err := pbs.CreateJob(ctx, "sync", prox.Params{"id": "pk95-sync-nacht", "store": "nas-backup", "remote-store": "backup", "comment": "Nachtkopie"}); err != nil {
		t.Fatal(err)
	}
	if err := pbs.RunJob(ctx, "sync", "pk95-sync-nacht"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	end := now.Add(time.Hour).Format("15:04")
	if err := c.SetCopyWindow("local", "pk95-sync-nacht", end); err != nil {
		t.Fatal(err)
	}
	running := func() int {
		ts, _ := pbs.RunningTasks(ctx)
		n := 0
		for _, x := range ts {
			if x.Type == "syncjob" && strings.HasSuffix(x.WorkerID, ":pk95-sync-nacht") {
				n++
			}
		}
		return n
	}
	c.EnforceCopyWindows(ctx, now) // inside the window
	if running() != 1 {
		t.Fatal("copy stopped inside its window")
	}
	c.EnforceCopyWindows(ctx, now.Add(2*time.Hour)) // after the end
	if running() != 0 {
		t.Fatal("copy still running after the window")
	}
	if !strings.Contains(eventTitles(t, c), "Kopierjob „Nachtkopie“ um "+end+" angehalten") {
		t.Fatalf("event: %s", eventTitles(t, c))
	}
	if c.SetCopyWindow("local", "x", "25:00") == nil {
		t.Fatal("bad time accepted")
	}
}
