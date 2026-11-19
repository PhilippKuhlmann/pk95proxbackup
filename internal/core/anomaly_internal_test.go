package core

import "testing"

func TestJudgeBackup(t *testing.T) {
	const gib = 1 << 30
	hist := func(up, size int64, n int) []backupSample {
		var h []backupSample
		for i := 0; i < n; i++ {
			h = append(h, backupSample{T: int64(i), Up: up + int64(i%3)*up/10, Size: size})
		}
		return h
	}
	for _, c := range []struct {
		name string
		h    []backupSample
		s    backupSample
		want string
	}{
		{"too little history", hist(gib/10, 50*gib, 4), backupSample{Up: 40 * gib, Size: 50 * gib}, ""},
		{"normal day", hist(gib, 50*gib, 10), backupSample{Up: 2 * gib, Size: 50 * gib}, ""},
		{"ransomware-like jump", hist(gib, 50*gib, 10), backupSample{Up: 20 * gib, Size: 50 * gib}, "burst"},
		// a tiny guest that usually sends 2 MiB: 900 MiB is many times more, but below 1 GiB
		{"small absolute jump", hist(2<<20, 4*gib, 10), backupSample{Up: 900 << 20, Size: 4 * gib}, ""},
		{"much smaller", hist(gib/10, 50*gib, 10), backupSample{Up: gib / 10, Size: 20 * gib}, "shrink"},
		{"small guest shrinks", hist(1<<20, 500<<20, 10), backupSample{Up: 1 << 20, Size: 100 << 20}, ""},
	} {
		if got, _, _ := judgeBackup(c.h, c.s); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
