package api_test

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Host backups are browsed through the PVE storage that wrote them, by administrators only
// (they hold /etc/pve/priv).
func TestHostBackupFiles(t *testing.T) {
	admin := login(t)
	code, out := admin.do("GET", "/api/hostbackup", nil, false)
	if code != 200 {
		t.Fatalf("list: %d %v", code, out)
	}
	cl := out["clusters"].([]any)[0].(map[string]any)
	nodes := cl["nodes"].([]any)
	snaps := nodes[0].(map[string]any)["snapshots"].([]any)
	if len(nodes) != 2 || len(snaps) == 0 || out["ssh_key"] == "" {
		t.Fatalf("view: %v", out)
	}
	tm := num(snaps[0].(map[string]any)["time"])
	q := "/api/restore/files?cluster=cluster&pbs=local&store=backup&type=host&bid=pve1&storage=pbs-appliance&time=" + tm
	code, out = admin.do("GET", q, nil, false)
	if code != 200 || fmt.Sprint(out["volume"]) != "pbs-appliance:backup/host/pve1/"+time.Unix(int64(snaps[0].(map[string]any)["time"].(float64)), 0).UTC().Format("2006-01-02T15:04:05Z") {
		t.Fatalf("files: %d %v", code, out)
	}
	var names []string
	for _, e := range out["entries"].([]any) {
		names = append(names, e.(map[string]any)["text"].(string))
	}
	if strings.Join(names, ",") != "etc.pxar.didx,info.pxar.didx,root.pxar.didx" {
		t.Fatalf("archives: %v", names)
	}
	// A storage that does not point to the datastore is refused.
	if code, _ := admin.do("GET", strings.Replace(q, "storage=pbs-appliance", "storage=local", 1), nil, false); code == 200 {
		t.Fatal("foreign storage accepted")
	}

	if code, out := admin.do("POST", "/api/users", map[string]any{"username": "kunde", "password": "kunde-kunde-1", "role": "files"}, true); code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	files := second(t, admin, "kunde", "kunde-kunde-1")
	if code, _ := files.do("GET", q, nil, false); code != 403 {
		t.Fatalf("files role reads host backup: %d", code)
	}
	if code, _ := files.do("GET", "/api/restore/files?cluster=cluster&pbs=local&store=backup&type=ct&bid=110&time=1794168000", nil, false); code != 200 {
		t.Fatalf("files role cannot read a container: %d", code)
	}
	// Settings: wrong values come back per field.
	code, out = admin.do("PUT", "/api/hostbackup/cluster", map[string]any{"enabled": true, "storage": "pbs-appliance", "time": "abc", "keep": 30}, true)
	if code != 422 || !strings.Contains(fmt.Sprint(out), "time") {
		t.Fatalf("validation: %d %v", code, out)
	}
	if code, out := admin.do("POST", "/api/hostbackup/cluster/check", map[string]any{"node": "pve2"}, true); code != 200 || !strings.HasPrefix(fmt.Sprint(out["version"]), "pve-manager/") {
		t.Fatalf("check: %d %v", code, out)
	}
}
