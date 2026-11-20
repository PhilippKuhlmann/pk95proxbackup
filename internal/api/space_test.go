package api_test

import (
	"testing"
	"time"
)

func TestSpaceAPI(t *testing.T) {
	admin := login(t)
	code, out := admin.do("GET", "/api/pbs/local/datastores/backup/space", nil, false)
	if code != 200 || out["shell"] != true || out["measured"].(float64) != 0 {
		t.Fatalf("before: %d %v", code, out)
	}
	if code, out := admin.do("POST", "/api/pbs/local/datastores/backup/space", nil, true); code != 200 {
		t.Fatalf("start: %d %v", code, out)
	}
	for i := 0; i < 100; i++ {
		_, out = admin.do("GET", "/api/pbs/local/datastores/backup/space", nil, false)
		if out["running"] == false && out["measured"].(float64) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if g := out["guests"].([]any); len(g) < 5 || out["total"].(float64) == 0 {
		t.Fatalf("after: %v", out)
	}
	if code, out := admin.do("GET", "/api/pbs/buero/datastores/offsite/space", nil, false); code != 200 || out["shell"] != false {
		t.Fatalf("no SSH: %d %v", code, out)
	}
	if code, _ := admin.do("POST", "/api/pbs/buero/datastores/offsite/space", nil, true); code != 409 {
		t.Fatalf("no SSH start: %d", code)
	}
}
