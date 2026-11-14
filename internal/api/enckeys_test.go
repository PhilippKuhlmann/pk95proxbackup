package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"pk95proxbackup/internal/enckey"
	"pk95proxbackup/internal/fake"
)

func storageKey(t *testing.T, name string) string {
	t.Helper()
	for _, st := range fake.Current.PVE.StorageList() {
		if st.Storage == name {
			return st.EncryptionKey
		}
	}
	t.Fatalf("storage %s missing", name)
	return ""
}

func TestEncryptionKeys(t *testing.T) {
	cl := login(t)

	// A new storage with a new key: the appliance keeps the key, PVE gets it.
	code, out := cl.do("POST", "/api/pve/cluster/storages", map[string]string{"pbs": "local", "store": "nas-backup", "name": "pbs-nas", "key": "new"}, true)
	id, _ := out["new_key"].(string)
	if code != 200 || len(id) != 64 {
		t.Fatalf("add encrypted storage: %d %v", code, out)
	}
	fp := storageKey(t, "pbs-nas")
	if enckey.ID(fp) != id {
		t.Fatalf("PVE key %q, appliance key %q", fp, id)
	}
	_, out = cl.do("GET", "/api/enckeys", nil, false)
	keys := out["keys"].([]any)
	if len(keys) != 1 {
		t.Fatalf("keys: %v", out)
	}
	k := keys[0].(map[string]any)
	if k["id"] != id || k["saved"] != nil || len(k["used"].([]any)) != 1 || k["key"] != nil {
		t.Fatalf("key view (no secret, not saved yet, used once): %v", k)
	}
	// the secret only on request
	_, out = cl.do("GET", "/api/enckeys/"+id, nil, false)
	if js, _ := out["key"].(string); !strings.Contains(js, fp) || !strings.Contains(out["qr"].(string), "<svg") || !strings.Contains(out["paper"].(string), "BEGIN PROXMOX BACKUP KEY") {
		t.Fatalf("key details: %v", out)
	}
	resp, err := cl.http.Get(cl.base + "/api/enckeys/" + id + "/paper")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(page), fp) || !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("paper: %d %s", resp.StatusCode, page)
	}
	if code, _ := cl.do("PUT", "/api/enckeys/"+id, map[string]any{"name": "Haupt", "saved": true}, true); code != 200 {
		t.Fatalf("mark saved: %d", code)
	}
	_, out = cl.do("GET", "/api/enckeys", nil, false)
	if k := out["keys"].([]any)[0].(map[string]any); k["name"] != "Haupt" || k["saved"] == nil {
		t.Fatalf("after rename: %v", k)
	}

	// The same key for the existing storage; then off again.
	if code, out := cl.do("PUT", "/api/pve/cluster/storages/pbs-appliance/key", map[string]string{"key": id}, true); code != 200 {
		t.Fatalf("encrypt pbs-appliance: %d %v", code, out)
	}
	if storageKey(t, "pbs-appliance") != fp {
		t.Fatal("pbs-appliance has not got the key")
	}
	// in use: cannot be deleted
	if code, out := cl.do("POST", "/api/enckeys/"+id+"/delete", map[string]string{"password": "0123456789"}, true); code != 409 {
		t.Fatalf("delete used key: %d %v", code, out)
	}
	if code, _ := cl.do("PUT", "/api/pve/cluster/storages/pbs-appliance/key", map[string]string{"key": ""}, true); code != 200 || storageKey(t, "pbs-appliance") != "" {
		t.Fatalf("switch off: %d", code)
	}

	// Import: typo rejected, paper form accepted, existing key recognised.
	other, ofp := enckey.New()
	bad := strings.Replace(other, `"data":"`, `"data":"A`, 1)
	if code, out := cl.do("POST", "/api/enckeys", map[string]string{"name": "Alt", "key": bad}, true); code != 422 {
		t.Fatalf("broken key imported: %d %v", code, out)
	}
	if code, out := cl.do("POST", "/api/enckeys", map[string]string{"name": "Alt", "key": enckey.Paper(other)}, true); code != 200 || out["existed"] != false {
		t.Fatalf("import paper key: %d %v", code, out)
	}
	if _, out := cl.do("POST", "/api/enckeys", map[string]string{"key": other}, true); out["existed"] != true || out["name"] != "Alt" {
		t.Fatalf("second import: %v", out)
	}

	// Restore of backups encrypted with the imported key: a storage with that key is added.
	fake.Current.Offsite.SetSnapshotKey("offsite", ofp)
	_, out = cl.do("GET", "/api/pbs/buero/datastores/offsite/snapshots", nil, false)
	snap := out["snapshots"].([]any)[0].(map[string]any)
	if snap["fingerprint"] != ofp {
		t.Fatalf("snapshot key not listed: %v", snap)
	}
	req := map[string]any{"cluster": "cluster", "node": "pve1", "pbs": "buero", "store": "offsite", "type": snap["backup-type"], "bid": snap["backup-id"],
		"time": snap["backup-time"], "vmid": 951, "storage": "local-lvm"}
	if code, out := cl.do("POST", "/api/restore", req, true); code != 200 {
		t.Fatalf("restore encrypted: %d %v", code, out)
	}
	want := "pk95-offsite-" + enckey.ID(ofp)[:8]
	if storageKey(t, want) != ofp {
		t.Fatalf("restore storage %s without the key", want)
	}
	// a second restore uses it again
	before := len(fake.Current.PVE.StorageList())
	req["vmid"] = 952
	if code, out := cl.do("POST", "/api/restore", req, true); code != 200 || len(fake.Current.PVE.StorageList()) != before {
		t.Fatalf("second restore: %d %v", code, out)
	}

	// Without the key in the appliance: a clear message, nothing added.
	if code, _ := cl.do("POST", "/api/enckeys/"+enckey.ID(ofp)+"/delete", map[string]string{"password": "0123456789"}, true); code != 409 {
		t.Fatalf("key of the restore storage deleted: %d", code)
	}
	_, unknown := enckey.New()
	fake.Current.Offsite.SetSnapshotKey("offsite", unknown)
	code, out = cl.do("POST", "/api/restore", req, true)
	if msg, _ := out["error"].(string); code != 502 || !strings.Contains(msg, "Einstellungen → Verschlüsselung") || len(fake.Current.PVE.StorageList()) != before {
		t.Fatalf("restore without key: %d %v", code, out)
	}
	_, out = cl.do("GET", "/api/enckeys", nil, false)
	states := map[string]string{}
	for _, s := range out["storages"].([]any) {
		m := s.(map[string]any)
		states[m["storage"].(string)] = m["state"].(string)
	}
	if states["pbs-nas"] != "ok" || states["pbs-appliance"] != "plain" || states[want] != "ok" {
		t.Fatalf("storage states: %v", states)
	}
	if code, out := cl.do("POST", "/api/enckeys/"+id+"/delete", map[string]string{"password": "falsch"}, true); code != 422 {
		t.Fatalf("delete with wrong password: %d %v", code, out)
	}
}
