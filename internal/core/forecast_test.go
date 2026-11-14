package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
)

const gib = int64(1) << 30

func series(now time.Time, days int, used0, perDay, total int64) []core.UsagePoint {
	var h []core.UsagePoint
	for i := days; i >= 0; i-- {
		t := now.AddDate(0, 0, -i)
		h = append(h, core.UsagePoint{time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).Unix(), used0 + perDay*int64(days-i), total})
	}
	return h
}

func TestMakeForecast(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.Local)
	// 1 TiB, 600 GiB used after 60 days of +5 GiB: 424 GiB left → about 85 days
	f := core.MakeForecast(series(now, 60, 300*gib, 5*gib, 1024*gib), false, now)
	if f.State != "soon" || f.DaysLeft < 83 || f.DaysLeft > 86 || (f.GrowthDay-5*gib)*20 > gib || (5*gib-f.GrowthDay)*20 > gib || f.BasisDays < 58 || f.BasisDays > 60 {
		t.Fatalf("growing: %+v", f)
	}
	if f.Pct90At == 0 || f.Pct90At >= f.FullAt {
		t.Fatalf("90 %%: %+v", f)
	}
	// 20 days left
	if f := core.MakeForecast(series(now, 30, 800*gib, 10*gib, 1300*gib), false, now); f.State != "urgent" || f.DaysLeft > 21 {
		t.Fatalf("urgent: %+v", f)
	}
	// shrinking
	if f := core.MakeForecast(series(now, 30, 500*gib, -gib, 1024*gib), false, now); f.State != "stable" || f.FullAt != 0 {
		t.Fatalf("stable: %+v", f)
	}
	// too little history
	if f := core.MakeForecast(series(now, 5, 500*gib, gib, 1024*gib), false, now); f.State != "unknown" || f.Used == 0 {
		t.Fatalf("unknown: %+v", f)
	}
	// a bigger disk: only the values since then count
	h := series(now, 60, 900*gib, 2*gib, 1024*gib)
	for i := len(h) - 20; i < len(h); i++ {
		h[i][2] = 4096 * gib
	}
	if f := core.MakeForecast(h, false, now); f.Total != 4096*gib || f.BasisDays < 18 || f.BasisDays > 19 || f.State != "ok" {
		t.Fatalf("bigger disk: %+v", f)
	}
	// full
	if f := core.MakeForecast(series(now, 30, 1000*gib, gib, 1024*gib), false, now); f.State != "critical" || f.DaysLeft != 0 {
		t.Fatalf("full: %+v", f)
	}
}

// The monitor fills the history from the year archive of the Backup Server and warns about the
// NAS datastore of the demo, which fills up within weeks.
func TestForecastFromMonitor(t *testing.T) {
	c := newCore(t)
	ov := c.BuildOverview(context.Background())
	c.RecordUsage(&ov)
	if h := c.UsageHistory("local", "nas-backup"); len(h) < 200 {
		t.Fatalf("history not filled: %d days", len(h))
	}
	f := c.Forecast("local", "nas-backup", false)
	if f.GrowthDay < 15*gib || f.GrowthDay > 35*gib {
		t.Fatalf("growth %d GiB a day: %+v", f.GrowthDay/gib, f)
	}
	urgent := f.State == "urgent" || f.State == "critical"
	if !urgent {
		t.Fatalf("nas forecast: %+v", f)
	}
	count := func() int {
		evs, _ := c.St.Events(context.Background(), "", 0, 50)
		n := 0
		for _, e := range evs {
			if strings.Contains(e.Title, "nas-backup ist voraussichtlich") || strings.Contains(e.Title, "nas-backup ist voll") {
				n++
			}
		}
		return n
	}
	if count() != 1 {
		t.Fatalf("warnings: %d", count())
	}
	c.RecordUsage(&ov)
	if count() != 1 {
		t.Fatal("warned twice")
	}
	if issues := c.SpaceIssues(ov); len(issues) == 0 || issues[0].Store != "nas-backup" {
		t.Fatalf("issues %+v", issues)
	}
	// a USB disk without enough values is not forecast
	if f := c.Forecast("local", "usb-b", true); f.State != "unknown" {
		t.Fatalf("usb: %+v", f)
	}
}
