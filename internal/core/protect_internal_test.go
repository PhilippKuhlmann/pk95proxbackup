package core

import (
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
	disks := []prox.NodeDisk{{DevPath: "/dev/sda", Serial: "WD-WCC4NJPSFNKL"}, {DevPath: "/dev/nvme0n1", Serial: "S73JNJ0X501388B"}, {DevPath: "/dev/sdb", Serial: "ZGY8ABCD"}}
	for name, want := range map[string]string{
		"/dev/sda3":      "/dev/sda",
		"/dev/nvme0n1p3": "/dev/nvme0n1",
		"sdb":            "/dev/sdb",
		"sdb1":           "/dev/sdb",
		"nvme-Samsung_SSD_990_PRO_with_Heatsink_1TB_S73JNJ0X501388B-part3": "/dev/nvme0n1",
		"ata-WDC_WD30EZRX-00D8PB0_WD-WCC4NJPSFNKL-part1":                   "/dev/sda",
		"/dev/disk/by-id/ata-ST4000VN008-2DR166_ZGY8ABCD":                  "/dev/sdb",
	} {
		d, ok := matchDisk(name, disks)
		if !ok || d.DevPath != want {
			t.Errorf("%s → %q (%v), want %s", name, d.DevPath, ok, want)
		}
	}
	if _, ok := matchDisk("wwn-0x5000c500a1b2c3d4", disks); ok {
		t.Error("unknown device matched")
	}
}
