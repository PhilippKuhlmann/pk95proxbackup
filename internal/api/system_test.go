package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func (c *client) multipart(path string, fields map[string]string, file []byte, csrf bool) (int, map[string]any) {
	c.t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	if file != nil {
		fw, _ := w.CreateFormFile("file", "x.pk95cfg")
		_, _ = fw.Write(file)
	}
	_ = w.Close()
	req, _ := http.NewRequest("POST", c.base+path, &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if csrf {
		req.Header.Set("X-PK95", "1")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestSelfBackupAndSetupRestore(t *testing.T) {
	cl, c := setup(t)
	var restarted atomic.Bool
	c.Restart = func() { restarted.Store(true) }
	// Before an account exists, a backup file restores the appliance (with the setup code).
	if code, out := cl.multipart("/api/setup/restore", map[string]string{"code": "WRONG", "password": "x"}, []byte("nonsense"), false); code != 422 || out["fields"] == nil {
		t.Fatalf("wrong code: %d %v", code, out)
	}
	if code, out := cl.multipart("/api/setup/restore", map[string]string{"code": "ABCD-EFGH", "password": "abcdefghijkl"}, []byte("nonsense"), false); code != 422 {
		t.Fatalf("not a backup: %d %v", code, out)
	}

	// Set up, switch the backup on, download it.
	if code, out := cl.do("POST", "/api/setup", map[string]string{"code": "ABCD-EFGH", "username": "admin", "password": "0123456789"}, false); code != 200 {
		t.Fatalf("setup: %d %v", code, out)
	}
	if code, out := cl.do("PUT", "/api/selfbackup", map[string]any{"enabled": true, "password": "kurz", "repeat": "kurz"}, true); code != 422 {
		t.Fatalf("short password: %d %v", code, out)
	}
	if code, out := cl.do("PUT", "/api/selfbackup", map[string]any{"enabled": true, "password": "Langes-Passwort-1", "repeat": "anders"}, true); code != 422 {
		t.Fatalf("repeat differs: %d %v", code, out)
	}
	if code, out := cl.do("PUT", "/api/selfbackup", map[string]any{"enabled": true, "password": "Langes-Passwort-1", "repeat": "Langes-Passwort-1"}, true); code != 200 || out["has_key"] != true {
		t.Fatalf("enable: %d %v", code, out)
	}
	resp, err := cl.http.Get(cl.base + "/api/selfbackup/download")
	if err != nil {
		t.Fatal(err)
	}
	file, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.HasPrefix(file, []byte("PK95CFG1")) {
		t.Fatalf("download: %d %q", resp.StatusCode, file[:min(len(file), 16)])
	}
	if code, _ := cl.multipart("/api/selfbackup/restore", map[string]string{"password": "Langes-Passwort-1"}, file, false); code != 403 {
		t.Fatalf("restore without X-PK95: %d", code)
	}
	if code, out := cl.multipart("/api/selfbackup/restore", map[string]string{"password": "falsch-falsch-1"}, file, true); code != 422 || out["fields"] == nil {
		t.Fatalf("wrong password: %d %v", code, out)
	}
	code, out := cl.multipart("/api/selfbackup/restore", map[string]string{"password": "Langes-Passwort-1"}, file, true)
	if code != 200 || out["host"] == "" {
		t.Fatalf("restore: %d %v", code, out)
	}
	// The restart is due a second later.
	for i := 0; i < 40 && !restarted.Load(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if !restarted.Load() {
		t.Fatal("restore must restart the appliance")
	}
	// The setup restore is closed once an account exists.
	if code, _ := cl.multipart("/api/setup/restore", map[string]string{"code": "ABCD-EFGH", "password": "Langes-Passwort-1"}, file, false); code != 409 {
		t.Fatalf("setup restore after setup: %d", code)
	}
}

// System updates: list, start (admins only), reboot refused while work runs.
func TestSysUpdatesAPI(t *testing.T) {
	admin := login(t)
	code, out := admin.do("GET", "/api/sysupdates", nil, false)
	if code != 200 || len(out["servers"].([]any)) < 2 {
		t.Fatalf("list: %d %v", code, out)
	}
	if code, out = admin.do("POST", "/api/pbs/buero/upgrade", nil, true); code != 409 || !strings.Contains(fmt.Sprint(out["error"]), "SSH") {
		t.Fatalf("server without shell: %d %v", code, out)
	}
	// switch SSH on from here: only when it works, as root
	if code, out = admin.do("POST", "/api/pbs/buero/ssh", map[string]any{"host": "10.20.0.5", "user": "backup"}, true); code != 422 || !strings.Contains(fmt.Sprint(out["error"]), "nicht root") {
		t.Fatalf("not root: %d %v", code, out)
	}
	if _, out = admin.do("GET", "/api/sysupdates", nil, false); out["servers"].([]any)[1].(map[string]any)["shell"] == true {
		t.Fatal("failed attempt switched SSH on")
	}
	if code, out = admin.do("POST", "/api/pbs/buero/ssh", map[string]any{"host": "10.20.0.5", "port": 22, "user": "root"}, true); code != 200 || out["output"] == "" {
		t.Fatalf("ssh: %d %v", code, out)
	}
	if code, out = admin.do("POST", "/api/pbs/local/ssh", map[string]any{}, true); code != 422 {
		t.Fatalf("ssh for the appliance: %d %v", code, out)
	}
	if code, out = admin.do("POST", "/api/pbs/buero/upgrade", nil, true); code != 200 || out["state"] != "running" {
		t.Fatalf("upgrade over ssh: %d %v", code, out)
	}
	admin.do("POST", "/api/users", map[string]any{"username": "bediener", "password": "bedienen-bedienen", "role": "operator"}, true)
	op := second(t, admin, "bediener", "bedienen-bedienen")
	for _, p := range []string{"/api/pbs/local/upgrade", "/api/pbs/local/reboot", "/api/pbs/local/packages"} {
		if code, _ := op.do("POST", p, map[string]any{}, true); code != 403 {
			t.Errorf("operator %s: %d", p, code)
		}
	}
	if code, out = admin.do("PUT", "/api/pbs/local/autoupdate", map[string]any{"enabled": true, "weekdays": []int{}, "time": "4 Uhr"}, true); code != 422 {
		t.Fatalf("bad plan: %d %v", code, out)
	}
	if code, out = admin.do("PUT", "/api/pbs/local/autoupdate", map[string]any{"enabled": true, "weekdays": []int{6, 7}, "time": "04:30", "reboot": true}, true); code != 200 {
		t.Fatalf("plan: %d %v", code, out)
	}
	if code, _ = op.do("PUT", "/api/pbs/local/autoupdate", map[string]any{"enabled": false, "time": "04:30"}, true); code != 403 {
		t.Fatalf("operator changed the plan: %d", code)
	}
	_, out = admin.do("GET", "/api/sysupdates", nil, false)
	for _, v := range out["servers"].([]any) {
		if m := v.(map[string]any); m["server_id"] == "local" && (m["auto"].(map[string]any)["enabled"] != true || m["next"] == nil) {
			t.Fatalf("plan not shown: %v", m)
		}
	}
	if code, out = admin.do("POST", "/api/pbs/local/datastores/backup/verify", nil, true); code != 200 {
		t.Fatalf("verify: %d %v", code, out)
	}
	if code, out = admin.do("POST", "/api/pbs/local/reboot", map[string]any{}, true); code != 409 || len(out["busy"].([]any)) == 0 {
		t.Fatalf("reboot while verifying: %d %v", code, out)
	}
}
