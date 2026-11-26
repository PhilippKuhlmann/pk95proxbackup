package core

import "testing"

// Slower only when the last two backups that read enough ran at less than half the usual speed.
func TestJudgeSpeed(t *testing.T) {
	gib := int64(1 << 30)
	run := func(mibs ...float64) []SpeedSample {
		var out []SpeedSample
		for i, v := range mibs {
			// 8 GiB each, as many seconds as the speed needs
			out = append(out, SpeedSample{T: int64(i), Bytes: 8 * gib, Secs: int64(8 * 1024 / v)})
		}
		return out
	}
	if j := judgeSpeed(run(200, 210, 190, 205, 200, 198, 40, 42)); !j.Slow || j.Usual < 190 || j.Last > 45 || len(j.Points) != 8 {
		t.Fatalf("two slow backups: %+v", j)
	}
	if j := judgeSpeed(run(200, 210, 190, 205, 200, 198, 205, 40)); j.Slow {
		t.Fatalf("one slow backup is no trend: %+v", j)
	}
	if j := judgeSpeed(run(200, 40, 42)); j.Slow || j.Usual != 0 {
		t.Fatalf("too few backups to judge: %+v", j)
	}
	// small backups say nothing about speed
	small := run(200, 210, 190, 205, 200, 198)
	small = append(small, SpeedSample{T: 10, Bytes: gib / 10, Secs: 30}, SpeedSample{T: 11, Bytes: gib / 10, Secs: 30})
	if j := judgeSpeed(small); j.Slow {
		t.Fatalf("small backups judged: %+v", j)
	}
}
