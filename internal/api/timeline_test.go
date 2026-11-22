package api_test

import (
	"sort"
	"strings"
	"testing"
)

// The time plan shows every scheduled job with start times and the duration of its last run;
// administrators move times, everyone else only reads.
func TestTimeline(t *testing.T) {
	admin, _ := adminAndCore(t)
	items := func() map[string]map[string]any {
		t.Helper()
		code, out := admin.do("GET", "/api/zeitplan", nil, false)
		if code != 200 {
			t.Fatalf("timeline: %d %v", code, out)
		}
		m := map[string]map[string]any{}
		for _, x := range out["items"].([]any) {
			it := x.(map[string]any)
			m[it["key"].(string)] = it
		}
		return m
	}
	m := items()
	for key, want := range map[string]string{
		"pve:cluster:backup-nightly":      "täglich 21:00",
		"prune:local:pk95-prune-backup":   "täglich 00:00",
		"verify:local:pk95-verify-backup": "Sa 04:00",
		"gc:local:nas-backup":             "So 06:00",
		"sync:local:pk95-sync-offsite":    "täglich 03:00",
		"usb:demo":                        "täglich 22:30",
	} {
		it := m[key]
		if it == nil {
			t.Fatalf("%s missing: %s", key, itemKeys(m))
		}
		if it["text"] != want || len(it["starts"].([]any)) == 0 {
			t.Errorf("%s: %v / %d starts", key, it["text"], len(it["starts"].([]any)))
		}
	}
	if v := m["verify:local:pk95-verify-backup"]; v["duration"] != float64(38*60) || v["estimated"] == true {
		t.Errorf("verify took %v", v["duration"])
	}
	if g := m["gc:local:backup"]; g["duration"] == nil || g["duration"].(float64) < 120 || g["estimated"] == true {
		t.Errorf("gc took %v", g["duration"])
	}
	if m["usb:demo"]["editable"] == true || m["gc:local:backup"]["editable"] != true {
		t.Error("editable wrong")
	}

	// the proposal: in the demo the garbage collection of "backup" runs with its cleanup at 00:00
	code, out := admin.do("GET", "/api/zeitplan/vorschlag", nil, false)
	if code != 200 {
		t.Fatalf("proposal: %d %v", code, out)
	}
	changes := out["changes"].([]any)
	if len(changes) != 1 || changes[0].(map[string]any)["key"] != "gc:local:backup" || changes[0].(map[string]any)["to"] != "00:30" {
		t.Fatalf("proposal: %v", changes)
	}

	viewer := account(t, admin, "leser", "viewer")
	if code, _ := viewer.do("GET", "/api/zeitplan", nil, false); code != 200 {
		t.Fatalf("viewer cannot read: %d", code)
	}
	body := map[string]any{"key": "gc:local:backup", "time": "05:30", "weekdays": []int{7}}
	if code, _ := viewer.do("PUT", "/api/zeitplan", body, true); code != 403 {
		t.Fatalf("viewer changed a schedule: %d", code)
	}
	if code, out := admin.do("PUT", "/api/zeitplan", body, true); code != 200 || out["schedule"] != "sun 05:30" {
		t.Fatalf("move gc: %d %v", code, out)
	}
	if code, out := admin.do("PUT", "/api/zeitplan", map[string]any{"key": "prune:local:pk95-prune-backup", "schedule": "mon..fri 1:15"}, true); code != 200 {
		t.Fatalf("move prune: %d %v", code, out)
	}
	m = items()
	if m["gc:local:backup"]["text"] != "So 05:30" || m["prune:local:pk95-prune-backup"]["text"] != "Mo–Fr 01:15" {
		t.Fatalf("not moved: %v / %v", m["gc:local:backup"]["text"], m["prune:local:pk95-prune-backup"]["text"])
	}
	if code, out := admin.do("PUT", "/api/zeitplan", map[string]any{"key": "gc:local:backup", "schedule": "25:00"}, true); code != 422 {
		t.Fatalf("bad schedule: %d %v", code, out)
	}
	if code, _ := admin.do("PUT", "/api/zeitplan", map[string]any{"key": "usb:demo", "time": "01:00", "weekdays": []int{1}}, true); code == 200 {
		t.Fatal("a USB plan is set on its own page")
	}
	code, out = admin.do("GET", "/api/zeitplan/preview?time=02:30&weekdays=1,2,3,4,5", nil, false)
	if code != 200 || out["text"] != "Mo–Fr 02:30" || len(out["next"].([]any)) != 5 {
		t.Fatalf("preview: %d %v", code, out)
	}
}

func itemKeys(m map[string]map[string]any) string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
