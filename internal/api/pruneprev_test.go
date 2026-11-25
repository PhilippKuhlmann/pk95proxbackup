package api_test

import "testing"

// The retention preview tells per guest what a cleanup would keep – and deletes nothing.
func TestPrunePreview(t *testing.T) {
	admin, _ := adminAndCore(t)
	count := func() int {
		_, out := admin.do("GET", "/api/pbs/local/datastores/backup/snapshots", nil, false)
		return len(out["snapshots"].([]any))
	}
	before := count()
	code, out := admin.do("POST", "/api/pbs/local/datastores/backup/prune-preview", map[string]any{"keep_last": 3}, true)
	if code != 200 {
		t.Fatalf("preview: %d %v", code, out)
	}
	groups := out["groups"].([]any)
	if len(groups) == 0 || out["remove"].(float64) == 0 {
		t.Fatalf("nothing to remove with keep-last 3: %v", out)
	}
	for _, x := range groups {
		g := x.(map[string]any)
		if g["keep"].(float64) > 3 || len(g["items"].([]any)) != int(g["keep"].(float64)+g["remove"].(float64)+g["protected"].(float64)) {
			t.Fatalf("group %v/%v: %v", g["type"], g["bid"], g)
		}
	}
	if int(out["keep"].(float64)+out["remove"].(float64)+out["protected"].(float64)) != before {
		t.Fatalf("preview covers %v+%v+%v of %d backups", out["keep"], out["remove"], out["protected"], before)
	}
	if count() != before {
		t.Fatal("the preview deleted backups")
	}
	// without any rule everything stays
	_, out = admin.do("POST", "/api/pbs/local/datastores/backup/prune-preview", map[string]any{}, true)
	if out["remove"].(float64) != 0 {
		t.Fatalf("no rule removed: %v", out["remove"])
	}
	if code, _ := admin.do("POST", "/api/pbs/local/datastores/backup/prune-preview", map[string]any{"keep_daily": -1}, true); code != 422 {
		t.Fatalf("negative value: %d", code)
	}
}
