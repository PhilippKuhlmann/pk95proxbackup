package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/prox"
)

// A maintenance switches off what is on, holds notifications back, ends by itself and switches
// on exactly what it switched off.
func TestMaintenance(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	pve, _, _ := c.PVE("cluster")
	pbs, _, _ := c.PBS("local")
	// A job that was off before stays off.
	if err := pve.CreateBackupJob(ctx, prox.Params{"id": "backup-off", "schedule": "02:00", "storage": "pbs-appliance", "all": 1, "enabled": 0}); err != nil {
		t.Fatal(err)
	}
	enabled := func() map[string]bool {
		out := map[string]bool{}
		jobs, _ := pve.BackupJobs(ctx)
		for _, j := range jobs {
			out["pve:"+j.ID] = j.IsEnabled()
		}
		for _, kind := range []string{"sync", "verify", "prune"} {
			js, _ := pbs.Jobs(ctx, kind)
			for _, j := range js {
				out[kind+":"+j.ID] = !j.Disable
			}
		}
		return out
	}
	before := enabled()
	if !before["pve:backup-nightly"] || before["pve:backup-off"] || !before["sync:pk95-sync-offsite"] {
		t.Fatalf("setup: %v", before)
	}
	m, err := c.StartMaintenance(ctx, time.Now().Add(2*time.Hour), "Host-Umbau", "admin", true, true)
	if err != nil {
		t.Fatal(err)
	}
	during := enabled()
	for k, on := range during {
		if on {
			t.Errorf("still on during the maintenance: %s", k)
		}
	}
	if len(m.Paused) == 0 || !c.InMaintenance() || !strings.Contains(eventTitles(t, c), "Wartung begonnen") {
		t.Fatalf("maintenance: %+v", m)
	}
	// Events are written, notifications held back.
	c.EventDetails("warning", "Cluster Zuhause", "Knoten pve1 nicht erreichbar", "", nil)
	if c.MaintenanceState().Muted != 1 {
		t.Fatalf("muted: %+v", c.MaintenanceState())
	}
	// Changing the end does not touch the jobs again.
	if _, err := c.StartMaintenance(ctx, time.Now().Add(3*time.Hour), "", "admin", true, true); err != nil {
		t.Fatal(err)
	}
	if n := len(c.MaintenanceState().Paused); n != len(m.Paused) {
		t.Fatalf("paused again: %d", n)
	}
	// Ends at its time.
	c.CheckMaintenanceForTest(ctx, time.Now().Add(4*time.Hour))
	if c.InMaintenance() {
		t.Fatal("not ended")
	}
	after := enabled()
	for k, on := range before {
		if after[k] != on {
			t.Errorf("%s: before %v, after %v", k, on, after[k])
		}
	}
	evs, _ := c.St.Events(ctx, "", 0, 5)
	ok := false
	for _, e := range evs {
		if e.Title == "Wartung beendet" && strings.Contains(string(e.Details), "Knoten pve1 nicht erreichbar") && strings.Contains(e.Body, "1 Benachrichtigungen") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("no summary:\n%s", eventTitles(t, c))
	}
	if _, err := c.StartMaintenance(ctx, time.Now().Add(-time.Minute), "", "admin", true, true); err == nil {
		t.Fatal("end in the past accepted")
	}
	if _, err := c.StartMaintenance(ctx, time.Now().Add(15*24*time.Hour), "", "admin", true, true); err == nil {
		t.Fatal("more than 14 days accepted")
	}
}
