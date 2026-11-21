package core_test

import (
	"context"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/fake"
)

// The demo has 90 recorded nights: VM 102 failed four nights ago, one night nothing worked,
// VM 111 joined 60 days ago.
func TestProof(t *testing.T) {
	fake.SeedHistory = true
	t.Cleanup(func() { fake.SeedHistory = false })
	c := newCore(t)
	ctx := context.Background()
	now := time.Now()
	from := time.Date(now.Year(), now.Month(), now.Day()-30, 0, 0, 0, 0, time.Local)
	rep, err := c.BuildProof(ctx, core.ProofRequest{From: from.Unix(), To: now.Unix(), For: "Muster GmbH"})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]core.ProofGuest{}
	for _, g := range rep.Guests {
		by[g.Kind+g.ID] = g
	}
	if rep.Runs != 30 || rep.RunsOK != 29 || rep.Partial != 0 || rep.For != "Muster GmbH" {
		t.Fatalf("report: runs %d/%d partial %d", rep.Runs, rep.RunsOK, rep.Partial)
	}
	if g := by["VM102"]; g.OK != 29 || g.Failed != 1 || g.Status != "warning" || g.Days != 29 || len(g.Copies) == 0 || g.Name != "nextcloud" {
		t.Fatalf("nextcloud: %+v", g)
	}
	if g := by["VM100"]; g.OK != 30 || g.Status != "ok" || g.MaxGap > 1 {
		t.Fatalf("dc01: %+v", g)
	}
	if !rep.Offsite || rep.Status != "warning" {
		t.Fatalf("summary: offsite %v status %s %q", rep.Offsite, rep.Status, rep.Headline)
	}
	// One guest only.
	rep, _ = c.BuildProof(ctx, core.ProofRequest{From: from.Unix(), To: now.Unix(), Guests: []string{"vm/101"}})
	if len(rep.Guests) != 1 || rep.Guests[0].ID != "101" || rep.Runs != 30 {
		t.Fatalf("one guest: %d guests, %d runs", len(rep.Guests), rep.Runs)
	}
	// The night without any backup is a gap of two days; a year back has no complete run data.
	rep, _ = c.BuildProof(ctx, core.ProofRequest{From: now.AddDate(-1, 0, 0).Unix(), To: now.Unix(), Guests: []string{"vm/100"}})
	if rep.Partial == 0 || rep.Guests[0].MaxGap < 200 {
		t.Fatalf("year: partial %d gap %d", rep.Partial, rep.Guests[0].MaxGap)
	}
	// October has 31 days, also with the change of the clock.
	oct := time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)
	if r, _ := c.BuildProof(ctx, core.ProofRequest{From: oct.Unix(), To: oct.AddDate(0, 1, 0).Unix()}); r.Days != 31 {
		t.Fatalf("october: %d days", r.Days)
	}
	if _, err := c.BuildProof(ctx, core.ProofRequest{From: now.Unix(), To: from.Unix()}); err == nil {
		t.Fatal("reversed period accepted")
	}
}
