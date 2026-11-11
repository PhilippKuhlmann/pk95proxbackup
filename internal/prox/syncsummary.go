package prox

import (
	"regexp"
	"strconv"
	"strings"
)

// SyncSummary is what a PBS sync task (job or one-off pull) reports at its end.
type SyncSummary struct {
	Found  bool   // a summary line was in the log
	NoNew  bool   // "found no new data to pull"
	Amount string // e.g. "487.49 GiB"
	Bytes  int64
	Rate   string // e.g. "96.106 MiB/s"
	Chunks int
}

// PBS 3.3+: "Summary: sync job pulled 487.49 GiB in 189633 chunks (average rate: 96.106 MiB/s)"
// or "Summary: sync job found no new data to pull".
var syncSummaryRe = regexp.MustCompile(`Summary: sync job (?:pulled|pushed) ([\d.]+ ?[KMGTP]?i?B) in (\d+) chunks(?: \(average rate: ([\d.]+ ?[KMGTP]?i?B/s)\))?`)

// ParseSyncSummary reads the summary of a sync task log.
func ParseSyncSummary(lines []TaskLine) SyncSummary {
	var s SyncSummary
	for _, l := range lines {
		t := l.T
		if strings.Contains(t, "Summary: sync job found no new data") {
			s.Found, s.NoNew = true, true
		}
		if m := syncSummaryRe.FindStringSubmatch(t); m != nil {
			s.Found, s.NoNew = true, false
			s.Amount, s.Rate = m[1], m[3]
			s.Chunks, _ = strconv.Atoi(m[2])
			s.Bytes = ParseSize(m[1])
		}
	}
	return s
}

var sizeRe = regexp.MustCompile(`^([\d.]+)\s*([KMGTP]?)(i?)B$`)

// ParseSize turns "32.00 GiB", "484 MiB" or "1.5 TB" into bytes (0 when unreadable).
func ParseSize(s string) int64 {
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	base := 1000.0
	if m[3] == "i" {
		base = 1024
	}
	for i := 0; i < strings.Index("KMGTP", m[2])+1 && m[2] != ""; i++ {
		v *= base
	}
	return int64(v)
}
