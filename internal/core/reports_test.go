package core_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/fake"
	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
)

// One job run on two nodes, one guest fails: one yellow event listing all five guests.
func TestBackupRunReport(t *testing.T) {
	fake.VzdumpDuration = 2 * time.Second
	t.Cleanup(func() { fake.VzdumpDuration = 25 * time.Second })
	c := newCore(t)
	ctx := context.Background()
	ov := c.BuildOverview(ctx)
	c.ReportBackupsForTest(ctx, &ov) // first look: only remembers the seeded past runs

	pve, _, err := c.PVE("cluster")
	if err != nil {
		t.Fatal(err)
	}
	for node, ids := range map[string]string{"pve1": "100,111", "pve2": "102,110,112"} {
		if _, err := pve.Vzdump(ctx, node, prox.Params{"vmid": ids, "storage": "pbs-appliance", "mode": "snapshot"}); err != nil {
			t.Fatal(err)
		}
	}
	// While running nothing is reported.
	ov = c.BuildOverview(ctx)
	c.ReportBackupsForTest(ctx, &ov)
	time.Sleep(3 * time.Second)
	ov = c.BuildOverview(ctx)
	c.ReportBackupsForTest(ctx, &ov)
	ov = c.BuildOverview(ctx)
	c.ReportBackupsForTest(ctx, &ov) // reported only once

	evs, _ := c.St.Events(ctx, "", 0, 50)
	var found []notify.Details
	var titles []string
	for _, e := range evs {
		if strings.HasPrefix(e.Title, "Datensicherung") {
			var d notify.Details
			if err := json.Unmarshal(e.Details, &d); err != nil {
				t.Fatal(err)
			}
			found = append(found, d)
			titles = append(titles, e.Severity+" "+e.Title)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one report, got %v", titles)
	}
	if titles[0] != "warning Datensicherung unvollständig – 4 von 5 Servern gesichert" {
		t.Fatalf("title %q", titles[0])
	}
	d := found[0]
	if d.Status != "warning" || len(d.Rows) != 5 {
		t.Fatalf("details %+v", d)
	}
	if r := d.Rows[0]; r.ID != "112" || r.Status != "error" || !strings.Contains(r.Info, "configuration file") {
		t.Fatalf("failed guest must come first: %+v", r)
	}
	names := map[string]string{}
	for _, r := range d.Rows {
		names[r.ID] = r.Kind + " " + r.Name
	}
	if names["110"] != "CT pihole" || names["100"] != "VM dc01" {
		t.Fatalf("names %v", names)
	}
	if len(d.Log) == 0 {
		t.Fatal("no log excerpt for the error")
	}
}
