package core

import (
	"testing"
	"time"

	"pk95proxbackup/internal/prox"
)

// How many backups stay on one disk and over how long, played through for two years.
func TestSimulateKeep(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.Local)
	cases := []struct {
		r          Rotation
		disks      int
		k          prox.Keep
		count, low int // span at least low days
		high       int // and at most high
	}{
		// direct, two disks taking turns daily: every other day a backup; 7 daily ones span 12 days
		{Rotation{Mode: "direct", Rhythm: "daily"}, 2, prox.Keep{Daily: 7}, 7, 12, 12},
		// only the newest: nothing to span
		{Rotation{Rhythm: "weekly", TransferLast: 1}, 2, prox.Keep{Last: 1}, 1, 0, 0},
		// 7 daily, 4 weekly, 3 monthly: the monthly ones reach about four months back
		{Rotation{Mode: "direct", Rhythm: "daily"}, 2, prox.Keep{Daily: 7, Weekly: 4, Monthly: 3}, 14, 90, 125},
		// copy of all backups since the last turn, a week away: the disk gets 14 at a time
		{Rotation{Rhythm: "weekly"}, 2, prox.Keep{Last: 30}, 30, 29, 29},
	}
	for i, c := range cases {
		n, span, gaps := simulateKeep(c.r, c.disks, c.k, now)
		if n < c.count-1 || n > c.count+1 || span < c.low || span > c.high || len(gaps) != max(n-1, 0) {
			t.Errorf("case %d: %d backups over %d days, gaps %v", i, n, span, gaps)
		}
		sum := 0
		for _, g := range gaps {
			sum += g
		}
		if sum != span && sum != span+1 {
			t.Errorf("case %d: gaps add up to %d, span %d", i, sum, span)
		}
	}
	if n, span, _ := simulateKeep(Rotation{Mode: "direct", Rhythm: "daily"}, 1, prox.Keep{}, now); n != 730 || span != 729 {
		t.Errorf("keep all: %d over %d", n, span)
	}
	if diskSizeFor(1.5e12) != 2e12 || diskSizeText(2e12) != "2 TB" || diskSizeText(5e11) != "500 GB" {
		t.Error("disk sizes")
	}
}
