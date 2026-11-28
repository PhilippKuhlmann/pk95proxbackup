package api_test

import "testing"

// A plan that connects its test guests needs a bridge; the bridges of a node are listed; kept
// guests come with the plans and can be deleted.
func TestRestoreTestNetworkAndKeep(t *testing.T) {
	admin, _ := adminAndCore(t)
	code, out := admin.do("GET", "/api/pve/cluster/nodes/pve1/bridges", nil, false)
	if code != 200 || len(out["bridges"].([]any)) == 0 {
		t.Fatalf("bridges: %d %v", code, out)
	}
	plan := map[string]any{"name": "Testsystem", "cluster": "cluster", "node": "pve1", "storage": "local-lvm", "pbs": "local", "store": "backup",
		"select": "all", "time": "02:00", "weekdays": []int{6}, "keep": true, "keep_running": true, "network": "bridge", "vlan": 20}
	if code, out = admin.do("POST", "/api/restoretests", plan, true); code != 422 || out["fields"] == nil {
		t.Fatalf("without bridge: %d %v", code, out)
	}
	plan["bridge"] = "vmbr1"
	if code, out = admin.do("POST", "/api/restoretests", plan, true); code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
	id := out["id"].(string)
	_, out = admin.do("GET", "/api/restoretests", nil, false)
	p := out["plans"].([]any)[0].(map[string]any)
	if p["bridge"] != "vmbr1" || p["vlan"] != float64(20) || p["keep"] != true || out["kept"] == nil {
		t.Fatalf("plan: %v / kept %v", p, out["kept"])
	}
	viewer := account(t, admin, "leser", "viewer")
	if code, _ := viewer.do("POST", "/api/restoretests/"+id+"/kept/delete", nil, true); code != 403 {
		t.Fatalf("viewer deleted kept guests: %d", code)
	}
	if code, _ := admin.do("POST", "/api/restoretests/"+id+"/kept/delete", nil, true); code != 200 {
		t.Fatalf("delete kept: %d", code)
	}
	if code, _ := admin.do("DELETE", "/api/restoretests/"+id, nil, true); code != 200 {
		t.Fatalf("delete plan: %d", code)
	}
}

// The job list shows the excludes of an administrator; the ones of kept test guests apart.
func TestJobListHidesTestExcludes(t *testing.T) {
	admin, c := setup(t)
	if code, out := admin.do("POST", "/api/setup", map[string]string{"code": "ABCD-EFGH", "username": "admin", "password": "0123456789"}, false); code != 200 {
		t.Fatalf("setup: %d %v", code, out)
	}
	// 130 is in the job, 990000 not (adding it failed): only 130 is shown apart
	_ = c.St.SetSetting("rtexclude", map[string]map[string][]string{"cluster": {"backup-nightly": {"130", "990000"}}})
	_, out := admin.do("GET", "/api/pve/cluster/jobs", nil, false)
	for _, x := range out["jobs"].([]any) {
		j := x.(map[string]any)
		if j["id"] != "backup-nightly" {
			continue
		}
		if j["exclude"] != "112,120" || len(j["test_guests"].([]any)) != 1 {
			t.Fatalf("job: %v", j)
		}
		return
	}
	t.Fatal("job missing")
}
