package api_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// All backups of a guest go in one step – after typing its ID; protected ones stay.
func TestDeleteGuestBackups(t *testing.T) {
	admin, _ := adminAndCore(t)
	places := func(typ, id string) (map[string]map[string]any, map[string]any) {
		t.Helper()
		code, out := admin.do("GET", "/api/guests/backups?type="+typ+"&bid="+id, nil, false)
		if code != 200 {
			t.Fatalf("places of %s/%s: %d %v", typ, id, code, out)
		}
		m := map[string]map[string]any{}
		for _, x := range out["places"].([]any) {
			p := x.(map[string]any)
			m[fmt.Sprint(p["server"], "/", p["store"])] = p
		}
		return m, out
	}
	del := func(cl *client, typ, id, confirm string, where ...string) (int, map[string]any) {
		q := url.Values{"type": {typ}, "bid": {id}, "confirm": {confirm}, "places": {strings.Join(where, ",")}}
		return cl.do("DELETE", "/api/guests/backups?"+q.Encode(), nil, true)
	}

	// a guest no connected Proxmox VE knows any more
	m, out := places("vm", "250")
	if m["local/backup"] == nil || len(out["in_job"].([]any)) != 0 || out["name"] != "alt-fileserver" {
		t.Fatalf("vm/250: %v", out)
	}
	viewer := account(t, admin, "leser", "viewer")
	if code, _ := del(viewer, "vm", "250", "250", "local/backup"); code != 403 {
		t.Fatalf("viewer deleted: %d", code)
	}
	if code, _ := del(admin, "vm", "250", "25", "local/backup"); code != 422 {
		t.Fatalf("wrong confirmation accepted: %d", code)
	}
	var all []string
	for k, p := range m {
		if p["mounted"] == true {
			all = append(all, k)
		}
	}
	code, res := del(admin, "vm", "250", "250", all...)
	if code != 200 || res["deleted"].(float64) < 1 || len(res["failed"].([]any)) != 0 {
		t.Fatalf("delete vm/250: %d %v", code, res)
	}
	if m, _ = places("vm", "250"); m["local/backup"] != nil {
		t.Fatalf("still there: %v", m)
	}

	// a guest still in a backup job, one backup protected
	m, out = places("vm", "100")
	if len(out["in_job"].([]any)) == 0 || m["local/backup"] == nil {
		t.Fatalf("vm/100: %v", out)
	}
	_, snaps := admin.do("GET", "/api/guests/snapshots?pbs=local&store=backup&type=vm&bid=100", nil, false)
	list := snaps["snapshots"].([]any)
	first := list[0].(map[string]any)
	if code, out := admin.do("PUT", "/api/pbs/local/datastores/backup/protected", map[string]any{"type": "vm", "bid": "100", "time": first["backup-time"], "protected": true}, true); code != 200 {
		t.Fatalf("protect: %d %v", code, out)
	}
	code, res = del(admin, "vm", "100", "100", "local/backup")
	if code != 200 || int(res["deleted"].(float64)) != len(list)-1 || len(res["kept"].([]any)) != 1 {
		t.Fatalf("delete vm/100: %d %v (had %d)", code, res, len(list))
	}
	if m, _ = places("vm", "100"); m["local/backup"]["count"] != float64(1) || m["local/backup"]["protected"] != float64(1) {
		t.Fatalf("left on backup: %v", m["local/backup"])
	}
}
