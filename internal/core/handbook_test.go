package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
)

func TestHandbook(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	h := c.BuildHandbook(ctx)
	if len(h.Problems) != 0 {
		t.Fatalf("problems: %v", h.Problems)
	}
	if len(h.Servers) != 2 || len(h.Clusters) != 1 || len(h.Guests) == 0 || len(h.Rotations) != 1 || len(h.Accounts) != 0 {
		t.Fatalf("content: %d servers, %d clusters, %d guests, %d rotations, %d accounts", len(h.Servers), len(h.Clusters), len(h.Guests), len(h.Rotations), len(h.Accounts))
	}
	if n := h.Clusters[0].Nodes; len(n) != 2 || n[0].IP != "192.168.10.11" || n[0].HostBackup == 0 {
		t.Fatalf("nodes: %+v", n)
	}
	var backup *core.HBStore
	for i, st := range h.Servers {
		for j := range st.Stores {
			if st.Local && st.Stores[j].Name == "backup" {
				backup = &h.Servers[i].Stores[j]
			}
		}
	}
	if backup == nil || backup.Kind != "Platte" {
		t.Fatalf("datastore backup: %+v", backup)
	}
	g := h.Guests[0]
	if len(g.Fixed) == 0 || !strings.Contains(g.Fixed[0], "/ backup") {
		t.Fatalf("guest %+v", g)
	}
	if h.Status.State != "never" || len(h.Hashes) != 9 {
		t.Fatalf("status %+v, %d hashes", h.Status, len(h.Hashes))
	}

	st, err := c.MarkHandbookPrinted(ctx, "admin")
	if err != nil || st.State != "current" || st.PrintedBy != "admin" {
		t.Fatalf("printed: %+v %v", st, err)
	}
	// Sizes and times change every day – the handbook stays current.
	c.BuildHandbook(ctx)
	if st := c.HandbookStatusInfo(); st.State != "current" {
		t.Fatalf("after rebuild: %+v", st)
	}
	// New contacts and a renamed disk are changes.
	if err := c.SetHandbookTexts("IT Muster, 0123", "", ""); err != nil {
		t.Fatal(err)
	}
	rots, _ := c.Rotations()
	r := rots[0]
	r.Disks[0].Label = "Platte A (Tresor)"
	if err := c.St.Put(core.KindRotation, r.ID, r); err != nil {
		t.Fatal(err)
	}
	c.BuildHandbook(ctx)
	st = c.HandbookStatusInfo()
	if st.State != "changed" || strings.Join(st.Changed, ",") != "Ansprechpartner und Hinweise,Wo liegt welcher Server,Wechsel- und Archiv-Platten" { // the guests name their disks
		t.Fatalf("changed: %+v", st)
	}

	// Printed again, but more than half a year ago.
	if _, err := c.MarkHandbookPrinted(ctx, "admin"); err != nil {
		t.Fatal(err)
	}
	s := c.HandbookSettingsOf()
	s.PrintedAt = time.Now().AddDate(0, -7, 0).Unix()
	_ = c.St.SetSetting("handbook", s)
	if st := c.HandbookStatusInfo(); st.State != "old" {
		t.Fatalf("old: %+v", st)
	}
	if err := c.SetHandbookTexts(strings.Repeat("x", 4001), "", ""); err == nil {
		t.Fatal("long text accepted")
	}
}

// The periodic check reports once when the printed handbook no longer matches.
func TestHandbookCheck(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	if _, err := c.MarkHandbookPrinted(ctx, "admin"); err != nil {
		t.Fatal(err)
	}
	_ = c.SetHandbookTexts("Neu: 0123", "", "")
	c.ScheduleHandbookForTest(time.Now().Add(7 * time.Hour))
	for i := 0; i < 300 && !strings.Contains(eventTitles(t, c), "Notfallhandbuch veraltet"); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(eventTitles(t, c), "Notfallhandbuch veraltet") {
		t.Fatalf("no message:\n%s", eventTitles(t, c))
	}
	c.ScheduleHandbookForTest(time.Now().Add(8 * time.Hour)) // checked recently: nothing
	time.Sleep(100 * time.Millisecond)
	if n := strings.Count(eventTitles(t, c), "Notfallhandbuch veraltet"); n != 1 {
		t.Fatalf("%d messages", n)
	}
}
