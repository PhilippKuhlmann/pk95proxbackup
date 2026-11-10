package prox

import "testing"

func lines(ss ...string) []TaskLine {
	out := make([]TaskLine, len(ss))
	for i, s := range ss {
		out[i] = TaskLine{N: i + 1, T: s}
	}
	return out
}

func TestParseProgress(t *testing.T) {
	cases := []struct {
		name    string
		in      []TaskLine
		percent float64
		detail  string
		step    string
	}{
		{"sync", lines("sync group vm/101", "re-sync snapshot vm/101/2026-11-08T20:00:00Z", "percentage done: 45.00% (9/20 groups)"), 45, "9/20 groups", "vm/101"},
		{"gc", lines("Start GC phase1 (mark used chunks)", "marked 12% (3 of 25 index files)"), 12, "3 of 25 index files", ""},
		{"vzdump", lines("INFO: Starting Backup of VM 102 (qemu)", "INFO:   7% (1.4 GiB of 20.0 GiB) in 3s, read: 470 MiB/s", "INFO:  33% (6.6 GiB of 20.0 GiB) in 12s, read: 580 MiB/s"), 33, "6.6 GiB of 20.0 GiB", "VM/CT 102"},
		{"restore", lines("new volume ID is 'local-lvm:vm-900-disk-0'", "progress 64% (read 6.4 GiB of 10.0 GiB, zeroes = 1%)"), 64, "read 6.4 GiB of 10.0 GiB, zeroes = 1%", ""},
		{"pbs timestamps", lines("2026-11-09T21:30:23+01:00: sync group ct/110", "2026-11-09T21:30:25+01:00: percentage done: 60.00% (3/5 groups)"), 60, "3/5 groups", "ct/110"},
		{"none", lines("starting garbage collection on store x"), -1, "", ""},
	}
	for _, c := range cases {
		p := ParseProgress(c.in)
		if p.Percent != c.percent || p.Detail != c.detail || p.Step != c.step {
			t.Errorf("%s: got %+v", c.name, p)
		}
	}
}

func TestGuestNameFromConfig(t *testing.T) {
	if n := GuestNameFromConfig([]byte("cores: 2\nname: web01\n\n[snap]\nname: old\n")); n != "web01" {
		t.Errorf("vm: %q", n)
	}
	if n := GuestNameFromConfig([]byte("arch: amd64\nhostname: dns\n")); n != "dns" {
		t.Errorf("ct: %q", n)
	}
	if n := GuestNameFromConfig([]byte("[snap]\nname: old\n")); n != "" {
		t.Errorf("only main section: %q", n)
	}
}
