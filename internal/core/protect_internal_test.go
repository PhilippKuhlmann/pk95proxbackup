package core

import (
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/prox"
)

func TestWantedProtected(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, loc)
	at := func(y int, m time.Month, d int) int64 { return time.Date(y, m, d, 21, 0, 0, 0, loc).Unix() }
	snaps := []prox.Snapshot{
		{Type: "vm", ID: "100", Time: at(2026, 3, 9)},   // 0 current month: no
		{Type: "vm", ID: "100", Time: at(2026, 2, 27)},  // 1 February, not the last
		{Type: "vm", ID: "100", Time: at(2026, 2, 28)},  // 2 last of February
		{Type: "vm", ID: "100", Time: at(2026, 1, 31)},  // 3 January
		{Type: "vm", ID: "100", Time: at(2025, 11, 30)}, // 4 outside 3 months, but last of 2025
		{Type: "vm", ID: "100", Time: at(2025, 6, 30)},  // 5 2025, not the last
		{Type: "vm", ID: "100", Time: at(2023, 12, 31)}, // 6 2023: outside 2 years
		{Type: "ct", ID: "110", Time: at(2026, 2, 1)},   // 7 other group, February
		{Type: "host", ID: "x", Time: at(2026, 2, 1)},   // 8 not a guest
	}
	got := wantedProtected(snaps, 3, 2, now)
	want := map[int]bool{2: true, 3: true, 4: true, 7: true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if !got[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if len(wantedProtected(snaps, 0, 0, now)) != 0 {
		t.Fatal("nothing wanted when switched off")
	}
}

func TestMatchNodeDisk(t *testing.T) {
	disks := []prox.NodeDisk{{DevPath: "/dev/sda", Serial: "WD-WCC4NJPSFNKL"}, {DevPath: "/dev/nvme0n1", Serial: "S73JNJ0X501388B"}, {DevPath: "/dev/sdb", Serial: "ZGY8ABCD"},
		{DevPath: "/dev/nvme1n1", Serial: "S73JNJ0X501442X", WWN: "eui.0025384541401846"}, {DevPath: "/dev/sdc", Serial: "X1", WWN: "0x5000c500a1b2c3d4"}}
	for name, want := range map[string]string{
		"/dev/sda3":      "/dev/sda",
		"/dev/nvme0n1p3": "/dev/nvme0n1",
		"sdb":            "/dev/sdb",
		"sdb1":           "/dev/sdb",
		"nvme-Samsung_SSD_990_PRO_with_Heatsink_1TB_S73JNJ0X501388B-part3": "/dev/nvme0n1",
		"ata-WDC_WD30EZRX-00D8PB0_WD-WCC4NJPSFNKL-part1":                   "/dev/sda",
		"/dev/disk/by-id/ata-ST4000VN008-2DR166_ZGY8ABCD":                  "/dev/sdb",
		"/dev/disk/by-id/nvme-eui.0025384541401846-part3":                  "/dev/nvme1n1",
		"wwn-0x5000c500a1b2c3d4-part1":                                     "/dev/sdc",
	} {
		d, ok := matchDisk(name, disks)
		if !ok || d.DevPath != want {
			t.Errorf("%s → %q (%v), want %s", name, d.DevPath, ok, want)
		}
	}
	if _, ok := matchDisk("wwn-0x5000c500ffffffff", disks); ok {
		t.Error("unknown device matched")
	}
}

func TestChainAge(t *testing.T) {
	now := time.Now()
	day := func(d int) int64 { return now.Add(-time.Duration(d) * 24 * time.Hour).Unix() }
	if age(day(1), now, 3, 10) != "ok" || age(day(5), now, 3, 10) != "warn" || age(day(22), now, 10, 21) != "bad" {
		t.Fatal("age")
	}
	if worse("ok", "none") != "ok" || worse("warn", "bad") != "bad" || worse("bad", "ok") != "bad" {
		t.Fatal("worse")
	}
}

func TestRotationStaleness(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	now := time.Date(2026, 11, 14, 15, 0, 0, 0, loc) // a Saturday
	at := func(m time.Month, d int) int64 { return time.Date(2026, m, d, 22, 30, 0, 0, loc).Unix() }

	// daily, two disks, no plan: forgotten after 4 days
	daily := Rotation{Rhythm: "daily", Disks: []RotDisk{{Key: "a"}, {Key: "b"}}}
	if st, _ := daily.staleness(daily.Disks[0], at(11, 12), now); st {
		t.Error("2 days: not stale")
	}
	if st, hint := daily.staleness(daily.Disks[0], at(11, 9), now); !st || !strings.Contains(hint, "alle 2 Tage") {
		t.Errorf("5 days: %v %q", st, hint)
	}
	// plugged in (being copied right now): never "long not plugged in"
	if st, _ := daily.diskStale(daily.Disks[0], true, at(11, 7), now); st {
		t.Error("plugged-in disk with an old copy counts as stale")
	}
	if st, _ := daily.diskStale(daily.Disks[0], false, at(11, 7), now); !st {
		t.Error("disk away with an old copy is not stale")
	}

	// Mo–Do disks and one Friday disk: the Friday disk after two missed Fridays
	plan := Rotation{Rhythm: "daily", Disks: []RotDisk{{Key: "mo", Days: []int{1}}, {Key: "fr", Days: []int{5}}}}
	fr := plan.Disks[1]
	if st, _ := plan.staleness(fr, at(11, 6), now); st {
		t.Error("Friday disk used last Friday but one: not stale yet (one missed turn)")
	}
	if st, hint := plan.staleness(fr, at(10, 30), now); !st || !strings.Contains(hint, "Fr 13.11.") || !strings.Contains(hint, "Fr 06.11.") {
		t.Errorf("two missed Fridays: %v %q", st, hint)
	}

	// five Friday disks, one per Friday of the month: the 1st-Friday disk after two months
	first := RotDisk{Key: "f1", Days: []int{5}, Week: "1"}
	fives := Rotation{Rhythm: "daily", Disks: []RotDisk{first, {Key: "f2", Days: []int{5}, Week: "2"}}}
	if st, _ := fives.staleness(first, at(10, 2), now); st {
		t.Error("1st-Friday disk used on 2 Oct: fine")
	}
	if st, hint := fives.staleness(first, at(9, 5), now); !st || !strings.Contains(hint, "1. Freitag im Monat") {
		t.Errorf("1st-Friday disk last used 5 Sep: %v %q", st, hint)
	}
}
