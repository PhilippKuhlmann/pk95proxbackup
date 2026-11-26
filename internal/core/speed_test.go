package core_test

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The demo logs of twelve nights: homeassistant (111) got much slower the last two nights.
func TestBackupSpeedFromLogs(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	ov := c.BuildOverview(ctx)
	c.RecordJobRunsForTest(ctx, &ov, time.Now())
	gs, _ := c.Coverage(ctx)
	if g := guestOf(t, gs, 111); g.Speed == nil || !g.Speed.Slow || g.Speed.Last > 45 || g.Speed.Usual < 120 {
		t.Fatalf("111: %+v", g.Speed)
	}
	if g := guestOf(t, gs, 100); g.Speed == nil || g.Speed.Slow || g.Speed.Last < 120 || len(g.Speed.Points) < 6 {
		t.Fatalf("100: %+v", g.Speed)
	}
	if g := guestOf(t, gs, 110); g.Speed == nil || g.Speed.Last < 100 {
		t.Fatalf("container 110 (had to backup …): %+v", g.Speed)
	}
	count := func() int {
		return strings.Count(eventTitles(t, c), "Sicherung von 111 homeassistant ist deutlich langsamer geworden")
	}
	if n := count(); n != 1 {
		t.Fatalf("warned %d times:\n%s", n, eventTitles(t, c))
	}
	// reading again neither doubles the backups nor warns again
	ov = c.BuildOverview(ctx)
	c.RecordJobRunsForTest(ctx, &ov, time.Now().Add(time.Hour))
	gs, _ = c.Coverage(ctx)
	if g := guestOf(t, gs, 100); len(g.Speed.Points) > 12 {
		t.Fatalf("recorded twice: %d points", len(g.Speed.Points))
	}
	if n := count(); n != 1 {
		t.Fatalf("warned again: %d", n)
	}
}
