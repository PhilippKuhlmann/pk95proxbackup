package core_test

import (
	"context"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/prox"
)

func TestMatchBackupJob(t *testing.T) {
	jobs := []prox.BackupJob{
		{ID: "nacht", Storage: "pbs", All: 1, Exclude: "112,120"},
		{ID: "db", Storage: "pbs", VMID: "101,102"},
		{ID: "usb", Storage: "usb"},
	}
	for _, c := range []struct {
		vj   prox.VzdumpJob
		want string
	}{
		{prox.VzdumpJob{Storage: "pbs", All: true, Exclude: []string{"120", "112"}}, "nacht"},
		{prox.VzdumpJob{Storage: "pbs", IDs: []string{"102", "101"}}, "db"},
		{prox.VzdumpJob{Storage: "pbs", IDs: []string{"105"}}, ""},           // by hand
		{prox.VzdumpJob{Storage: "usb", IDs: []string{"100", "101"}}, "usb"}, // "run now" of the only job there
		{prox.VzdumpJob{JobID: "db", Storage: "x"}, "db"},
	} {
		if got := core.MatchBackupJobForTest(jobs, c.vj); got != c.want {
			t.Errorf("%+v: %q, want %q", c.vj, got, c.want)
		}
	}
	vj, ok := prox.ParseVzdumpJob([]prox.TaskLine{{T: "INFO: starting new backup job: vzdump --all 1 --exclude 112,120 --storage pbs --mode snapshot --job-id backup-a1b2"}})
	if !ok || !vj.All || vj.JobID != "backup-a1b2" || vj.Storage != "pbs" || len(vj.Exclude) != 2 {
		t.Fatalf("parse: %+v", vj)
	}
	vj, _ = prox.ParseVzdumpJob([]prox.TaskLine{{T: "INFO: starting new backup job: vzdump 100 110 --storage pbs --mode snapshot"}})
	if vj.All || len(vj.IDs) != 2 || vj.IDs[1] != "110" {
		t.Fatalf("parse ids: %+v", vj)
	}
}

// The demo has twelve nights of the backup job (one with a failed guest), the copy to the
// office (once without connection) and the rotation disks.
func TestJobCards(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	ov := c.BuildOverview(ctx)
	c.RecordJobRunsForTest(ctx, &ov, time.Now())
	cards := c.JobCards(ctx)
	by := map[string]core.JobCard{}
	for _, card := range cards {
		by[card.Key] = card
	}
	count := func(runs []core.JobRun, st string) int {
		n := 0
		for _, r := range runs {
			if r.Status == st {
				n++
			}
		}
		return n
	}
	pve := by["pve:cluster:backup-nightly"]
	if len(pve.Runs) != 12 || count(pve.Runs, "warning") != 1 || pve.Status != "ok" || pve.What != "Alle VMs und Container außer 3" || pve.Target != "Diese Appliance (pk95-backup) / backup" {
		t.Fatalf("backup job: %+v", pve)
	}
	if r := pve.Runs[len(pve.Runs)-1]; r.OK != 5 || r.Failed != 0 || r.Bytes == 0 {
		t.Fatalf("last run: %+v", r)
	}
	sync := by["sync:local:pk95-sync-offsite"]
	if len(sync.Runs) != 12 || count(sync.Runs, "error") != 1 || sync.Kind != "sync" {
		t.Fatalf("copy job: %+v", sync)
	}
	usb := by["usb:demo"]
	if len(usb.Runs) < 12 || usb.Runs[len(usb.Runs)-1].Label == "" || usb.Kind != "usb" {
		t.Fatalf("rotation: %+v", usb)
	}
	if _, ok := by["sync:local:pk95r-demo-a1"]; ok {
		t.Fatal("rotation disk as own copy job")
	}
	// A second look adds nothing.
	ov = c.BuildOverview(ctx)
	c.RecordJobRunsForTest(ctx, &ov, time.Now().Add(5*time.Minute))
	if n := len(c.JobCards(ctx)); n != len(cards) {
		t.Fatalf("cards %d → %d", len(cards), n)
	}
	for _, card := range c.JobCards(ctx) {
		if card.Key == "pve:cluster:backup-nightly" && len(card.Runs) != 12 {
			t.Fatalf("counted twice: %d", len(card.Runs))
		}
	}
}
