package api_test

import "testing"

// Target times: administrators set them, everyone reads them; the guest list carries the
// estimate.
func TestRTOTargets(t *testing.T) {
	admin, _ := adminAndCore(t)
	viewer := account(t, admin, "leser", "viewer")
	if code, out := viewer.do("GET", "/api/rto", nil, false); code != 200 || out["default"] != float64(0) {
		t.Fatalf("viewer reads: %d %v", code, out)
	}
	if code, _ := viewer.do("PUT", "/api/rto", map[string]any{"default": 60}, true); code != 403 {
		t.Fatalf("viewer set a target: %d", code)
	}
	if code, _ := admin.do("PUT", "/api/rto", map[string]any{"default": 2}, true); code != 400 {
		t.Fatalf("2 minutes: %d", code)
	}
	if code, out := admin.do("PUT", "/api/rto", map[string]any{"default": 240}, true); code != 200 || out["default"] != float64(240) {
		t.Fatalf("default: %d %v", code, out)
	}
	code, out := admin.do("PUT", "/api/rto/guests/cluster/100", map[string]any{"minutes": 30}, true)
	if code != 200 || out["guests"].(map[string]any)["cluster/100"] != float64(30) {
		t.Fatalf("guest target: %d %v", code, out)
	}
	if code, out = admin.do("PUT", "/api/rto/guests/cluster/100", map[string]any{"minutes": nil}, true); code != 200 || len(out["guests"].(map[string]any)) != 0 {
		t.Fatalf("back to default: %d %v", code, out)
	}
	_, out = admin.do("GET", "/api/guests", nil, false)
	n := 0
	for _, x := range out["guests"].([]any) {
		g := x.(map[string]any)
		r, ok := g["rto"].(map[string]any)
		if !ok {
			continue
		}
		n++
		if r["secs"].(float64) <= 0 || r["target"] != float64(240) || r["basis"] == "" {
			t.Fatalf("estimate of %v: %v", g["vmid"], r)
		}
	}
	if n == 0 {
		t.Fatal("no guest with an estimate")
	}
}
