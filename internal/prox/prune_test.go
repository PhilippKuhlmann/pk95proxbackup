package prox

import (
	"sort"
	"strings"
	"testing"
	"time"
)

func snapsAt(times ...time.Time) []Snapshot {
	var out []Snapshot
	for _, t := range times {
		out = append(out, Snapshot{Type: "vm", ID: "100", Time: t.Unix()})
	}
	return out
}

func kept(m map[int64]bool) string {
	var out []string
	for t, k := range m {
		if k {
			out = append(out, time.Unix(t, 0).UTC().Format("01-02 15h"))
		}
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// The rules of the Backup Server's cleanup.
func TestPruneSelect(t *testing.T) {
	loc := time.UTC
	day := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, loc) }
	daily := func(from, to int) []Snapshot {
		var ts []time.Time
		for d := from; d <= to; d++ {
			ts = append(ts, day(d, 21))
		}
		return snapsAt(ts...)
	}
	for _, c := range []struct {
		name  string
		snaps []Snapshot
		keep  Keep
		want  string
	}{
		{"several a day: the newest", snapsAt(day(20, 1), day(20, 2), day(20, 3)), Keep{Daily: 1}, "11-20 03h"},
		{"daily", daily(1, 10), Keep{Daily: 3}, "11-08 21h, 11-09 21h, 11-10 21h"},
		{"last, then other days", daily(1, 10), Keep{Last: 1, Daily: 2}, "11-08 21h, 11-09 21h, 11-10 21h"},
		// Monday 2 … Sunday 15 November: the newest of the last two ISO weeks
		{"weekly", daily(2, 15), Keep{Weekly: 2}, "11-08 21h, 11-15 21h"},
		{"no rule keeps everything", daily(1, 3), Keep{}, "11-01 21h, 11-02 21h, 11-03 21h"},
	} {
		if got := kept(PruneSelect(c.snaps, c.keep, loc)); got != c.want {
			t.Errorf("%s: kept %s, want %s", c.name, got, c.want)
		}
	}
	// a protected backup stays and counts for no rule
	s := daily(1, 5)
	s[4].Protected = true // 5 November
	if got := kept(PruneSelect(s, Keep{Last: 2}, loc)); got != "11-03 21h, 11-04 21h, 11-05 21h" {
		t.Errorf("protected: %s", got)
	}
}
