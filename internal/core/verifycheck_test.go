package core_test

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The demo has two failed verifications: on "backup" only a snapshot the retention removed
// while the check ran, on "offsite" a damaged backup of VM 101.
func TestFailedVerifications(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	// not the first look: failures of the last 24 hours are reported
	if err := c.St.SetSetting("seen-failed", map[string]bool{"-": true}); err != nil {
		t.Fatal(err)
	}
	ov := c.BuildOverview(ctx)
	c.CheckFailedForTest(ctx, &ov)
	titles := eventTitles(t, c)
	if !strings.Contains(titles, "Prüfung von Diese Appliance (pk95-backup) / backup ohne Befund") {
		t.Fatalf("harmless failure not explained:\n%s", titles)
	}
	if !strings.Contains(titles, "Beschädigte Sicherungen auf PBS Büro (extern) / offsite") {
		t.Fatalf("damage not reported:\n%s", titles)
	}
	if strings.Contains(titles, "Prüfjob fehlgeschlagen") || strings.Contains(titles, "Prüfung fehlgeschlagen") {
		t.Fatalf("generic failure report on top:\n%s", titles)
	}
	dmg := c.RecentDamage(24 * time.Hour)
	if len(dmg) != 1 || dmg[0].Store != "offsite" || len(dmg[0].Damaged) != 1 {
		t.Fatalf("damage: %+v", dmg)
	}
	if d := dmg[0].Damaged[0]; d.Type != "vm" || d.ID != "101" || d.Time == 0 || d.Name != "fileserver" || !strings.Contains(d.Error, "can't verify chunk") {
		t.Fatalf("damaged backup: %+v", d)
	}
	// the next overview marks the harmless task
	ov = c.BuildOverview(ctx)
	harmless, noted := 0, 0
	for _, x := range ov.Tasks {
		if x.Harmless {
			harmless++
		}
		if x.Note != "" {
			noted++
		}
	}
	if harmless != 1 || noted != 2 {
		t.Fatalf("annotated: %d harmless, %d with a note", harmless, noted)
	}
}

// On the first look (or for old tasks) nothing is reported, but the tasks are still read.
func TestFailedVerificationsFirstLook(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	ov := c.BuildOverview(ctx)
	c.CheckFailedForTest(ctx, &ov)
	if titles := eventTitles(t, c); strings.Contains(titles, "Beschädigte") || strings.Contains(titles, "ohne Befund") {
		t.Fatalf("reported on the first look:\n%s", titles)
	}
	if len(c.VerifyResults()) != 2 {
		t.Fatalf("not read: %+v", c.VerifyResults())
	}
}
