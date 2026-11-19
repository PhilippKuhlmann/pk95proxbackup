package core_test

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The demo PBS has two weeks of backups; the newest backup of VM 102 brought 74 GiB of new
// data instead of about 4 GiB. The scan reports it once, protects the backup before it and
// lifts that protection when someone marks the report as checked.
func TestAnomalies(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	now := time.Now()
	c.ScanAnomalies(ctx, now)
	open := c.OpenAnomalies()
	if len(open) != 1 {
		t.Fatalf("want one unusual backup, got %+v", open)
	}
	a := open[0]
	if a.Type != "vm" || a.GuestID != "102" || a.Kind != "burst" || a.Upload != 74<<30 || a.NormalUp < 3<<30 || a.NormalUp > 5<<30 {
		t.Fatalf("anomaly: %+v", a)
	}
	if !a.KeptOwn || a.Kept == 0 || a.Kept >= a.Time {
		t.Fatalf("last normal backup not kept: %+v", a)
	}
	pbs, _, err := c.PBS("local")
	if err != nil {
		t.Fatal(err)
	}
	protected := func() bool {
		snaps, _ := pbs.Snapshots(ctx, "backup", "")
		for _, s := range snaps {
			if s.Type == "vm" && s.ID == "102" && s.Time == a.Kept {
				return s.Protected
			}
		}
		t.Fatal("kept backup missing")
		return false
	}
	if !protected() {
		t.Fatal("kept backup is not protected on the server")
	}
	if !strings.Contains(eventTitles(t, c), "Auffällige Sicherung: VM 102") {
		t.Fatalf("not reported:\n%s", eventTitles(t, c))
	}
	// a later scan reads nothing twice and reports nothing again
	c.ScanAnomalies(ctx, now.Add(11*time.Minute))
	if n := len(c.Anomalies()); n != 1 {
		t.Fatalf("reported again: %d", n)
	}
	if err := c.AckAnomaly(ctx, a.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if len(c.OpenAnomalies()) != 0 || protected() {
		t.Fatal("ack must close the report and lift the protection")
	}
}
