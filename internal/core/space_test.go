package core_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
)

// The script reads real PBS index files (4096-byte header, then 32-byte digests for fixed or
// 8-byte offset + digest for dynamic indexes) and the chunk files they name.
func TestSpaceScript(t *testing.T) {
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("no perl")
	}
	base := t.TempDir()
	digest := func(n int) []byte { s := sha256.Sum256([]byte{byte(n)}); return s[:] }
	sizes := map[int]int{1: 4096, 2: 8192, 3: 12288, 4: 16384, 5: 20480}
	for n, size := range sizes {
		h := hex.EncodeToString(digest(n))
		dir := filepath.Join(base, ".chunks", h[:4])
		_ = os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(dir, h), bytes.Repeat([]byte{byte(n)}, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	index := func(path string, dynamic bool, ns ...int) {
		var b bytes.Buffer
		b.Write(make([]byte, 4096))
		for i, n := range ns {
			if dynamic {
				_ = binary.Write(&b, binary.LittleEndian, uint64((i+1)*4096))
			}
			b.Write(digest(n))
		}
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	index(filepath.Join(base, "vm/100/2026-11-01T21:00:00Z/drive-scsi0.img.fidx"), false, 1, 2, 1)
	index(filepath.Join(base, "vm/100/2026-11-02T21:00:00Z/drive-scsi0.img.fidx"), false, 1, 3)
	index(filepath.Join(base, "vm/101/2026-11-02T21:00:00Z/drive-scsi0.img.fidx"), false, 2, 4)
	index(filepath.Join(base, "ns/kunde/ct/200/2026-11-02T21:00:00Z/root.pxar.didx"), true, 4, 5)
	_ = os.WriteFile(filepath.Join(base, "vm/101/2026-11-02T21:00:00Z/index.json.blob"), []byte("{}"), 0o644)

	out, err := exec.Command("perl", "-e", core.SpaceScriptForTest, base).CombinedOutput()
	if err != nil {
		t.Fatalf("perl: %v\n%s", err, out)
	}
	s, err := core.ParseSpaceForTest(string(out))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if s.Chunks != 5 || s.Total != 61440 || len(s.Guests) != 3 {
		t.Fatalf("totals: %+v\n%s", s, out)
	}
	want := map[string][3]int64{"vm/100": {20480, 16384, 3}, "vm/101": {12288, 0, 2}, "kunde/ct/200": {28672, 20480, 2}}
	for _, g := range s.Guests {
		w := want[g.Group]
		if g.Share != w[0] || g.Exclusive != w[1] || g.Chunks != w[2] {
			t.Errorf("%s: share %d excl %d chunks %d, want %v", g.Group, g.Share, g.Exclusive, g.Chunks, w)
		}
		if g.Group == "kunde/ct/200" && (g.NS != "kunde" || g.Type != "ct" || g.ID != "200") {
			t.Errorf("namespace: %+v", g)
		}
	}
	// No datastore there.
	out, _ = exec.Command("perl", "-e", core.SpaceScriptForTest, t.TempDir()).CombinedOutput()
	if _, err := core.ParseSpaceForTest(string(out)); err == nil {
		t.Fatalf("empty dir accepted: %s", out)
	}
}

func TestSpaceGrowth(t *testing.T) {
	if g, d := core.Growth30ForTest([]string{"2026-10-01", "2026-10-21", "2026-10-31"}, []int64{100, 150, 160}); g != 60 || d != 30 {
		t.Fatalf("30 days: %d %d", g, d)
	}
	if g, d := core.Growth30ForTest([]string{"2026-10-21", "2026-10-31"}, []int64{100, 110}); g != 30 || d != 10 {
		t.Fatalf("10 days scaled: %d %d", g, d)
	}
	if g, _ := core.Growth30ForTest([]string{"2026-10-28", "2026-10-31"}, []int64{100, 200}); g != 0 {
		t.Fatalf("too short: %d", g)
	}
}

// In the demo: the local datastore is measured with names, newest size and growth; a server
// without shell access cannot be measured.
func TestMeasureSpace(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	if err := c.MeasureSpace(ctx, "local", "backup"); err != nil {
		t.Fatal(err)
	}
	s := c.Space("local", "backup")
	if len(s.Guests) < 5 || s.Total == 0 || s.Measured == 0 || !s.Shell || s.Error != "" {
		t.Fatalf("space: %+v", s)
	}
	var sum int64
	by := map[string]core.GuestSpace{}
	for i, g := range s.Guests {
		sum += g.Share
		by[g.Group] = g
		if i > 0 && g.Share > s.Guests[i-1].Share {
			t.Fatal("not sorted by share")
		}
	}
	if sum != s.Total {
		t.Fatalf("shares %d, total %d", sum, s.Total)
	}
	nc := by["vm/102"]
	if nc.Name != "nextcloud" || nc.Snapshots == 0 || nc.Logical == 0 || nc.Growth30 <= 0 || nc.Basis != 30 {
		t.Fatalf("nextcloud: %+v", nc)
	}
	if old := by["vm/250"]; old.Name != "alt-fileserver" || !old.Gone || nc.Gone {
		t.Fatalf("former guest: %+v", old)
	}
	if h := by["host/pve1"]; h.Name != "pve1" || h.Type != "host" || h.Gone {
		t.Fatalf("host: %+v", h)
	}
	if err := c.StartSpace("buero", "offsite"); err == nil {
		t.Fatal("server without SSH accepted")
	}

	// The daily run measures every fixed datastore of the appliance (not the USB disks).
	c.ScheduleSpaceForTest(ctx, time.Now().Add(25*time.Hour))
	for i := 0; i < 300 && c.Space("local", "nas-backup").Measured == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if c.Space("local", "nas-backup").Measured == 0 {
		t.Fatal("nas-backup not measured")
	}
	if c.Space("local", "usb-a").Measured != 0 {
		t.Fatal("USB disk measured")
	}
}
