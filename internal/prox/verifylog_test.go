package prox

import "testing"

func TestParseVerifyLog(t *testing.T) {
	// from PBS01: one snapshot was pruned while the check ran, the rest was fine
	log := lines(
		"2026-11-18T19:58:00+01:00: verify datastore local",
		"2026-11-18T19:58:00+01:00: found 41 groups",
		"2026-11-18T19:58:01+01:00: verify group local:ct/100 (8 snapshots)",
		"2026-11-18T19:58:01+01:00: verify local:ct/100/2026-11-15T09:00:01Z",
		"2026-11-18T19:58:01+01:00:   check pct.conf.blob",
		"2026-11-18T19:58:04+01:00:   verified 170.60/483.49 MiB in 2.70 seconds, speed 63.14/178.93 MiB/s (0 errors)",
		"2026-11-19T00:01:00+01:00: verify local:ct/250101/2026-10-04T08:00:16Z - manifest load error: unable to load blob '\"/mnt/datastore/local/ct/250101/2026-10-04T08:00:16Z/index.json.blob\"' - No such file or directory (os error 2)",
		"2026-11-19T00:10:00+01:00: verify local:vm/200/2026-11-18T11:00:00Z",
		"2026-11-19T00:10:01+01:00:   check drive-scsi0.img.fidx",
		"2026-11-19T00:10:05+01:00:   can't verify chunk, load failed - store 'local', unable to load chunk 'abc' - No such file or directory (os error 2)",
		"2026-11-19T00:10:09+01:00:   verified 100.00/200.00 MiB in 3.00 seconds, speed 30.00/60.00 MiB/s (1 errors)",
		"2026-11-19T00:35:00+01:00: Failed to verify the following snapshots/groups:",
		"2026-11-19T00:35:00+01:00: \tct/250101/2026-10-04T08:00:16Z",
		"2026-11-19T00:35:00+01:00: \tvm/200/2026-11-18T11:00:00Z",
		"2026-11-19T00:35:00+01:00: TASK ERROR: verification failed - please check the log for details")
	v := ParseVerifyLog(log)
	if v.Snapshots != 3 || len(v.Failures) != 2 {
		t.Fatalf("parsed %+v", v)
	}
	if f := v.Failures[0]; f.Path != "ct/250101/2026-10-04T08:00:16Z" || f.Error == "" || f.Error[:19] != "manifest load error" {
		t.Fatalf("pruned: %+v", f)
	}
	if f := v.Failures[1]; f.Path != "vm/200/2026-11-18T11:00:00Z" || f.Error[:19] != "can't verify chunk," {
		t.Fatalf("damaged: %+v", f)
	}
	ns, typ, id, tm, ok := VerifyPath("ns/kunde/vm/200/2026-11-18T11:00:00Z")
	if !ok || ns != "kunde" || typ != "vm" || id != "200" || tm != "2026-11-18T11:00:00Z" {
		t.Fatalf("path: %q %q %q %q", ns, typ, id, tm)
	}
}
