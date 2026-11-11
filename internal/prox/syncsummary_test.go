package prox

import "testing"

func TestParseSyncSummary(t *testing.T) {
	s := ParseSyncSummary([]TaskLine{{T: "2026-11-10T15:02:33+01:00: Summary: sync job pulled 487.49 GiB in 189633 chunks (average rate: 96.106 MiB/s)"}})
	if !s.Found || s.NoNew || s.Amount != "487.49 GiB" || s.Rate != "96.106 MiB/s" || s.Chunks != 189633 || s.Bytes < 523e9 || s.Bytes > 524e9 {
		t.Fatalf("pulled: %+v", s)
	}
	s = ParseSyncSummary([]TaskLine{{T: "2026-11-10T05:53:35+01:00: Summary: sync job found no new data to pull"}})
	if !s.Found || !s.NoNew {
		t.Fatalf("nothing new: %+v", s)
	}
	if ParseSyncSummary([]TaskLine{{T: "TASK OK"}}).Found {
		t.Fatal("no summary")
	}
	for in, want := range map[string]int64{"484 MiB": 484 << 20, "32.00 GiB": 32 << 30, "1.5 TB": 1500e9, "12 B": 12, "x": 0} {
		if got := ParseSize(in); got != want {
			t.Errorf("ParseSize(%q) = %d, want %d", in, got, want)
		}
	}
}
