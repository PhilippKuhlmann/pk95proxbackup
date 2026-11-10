package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pk95proxbackup/internal/api"
	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/fake"
	"pk95proxbackup/internal/hostexec"
	"pk95proxbackup/internal/store"
	"pk95proxbackup/web"
)

type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func (c *client) do(method, path string, body any, csrf bool) (int, map[string]any) {
	c.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("Content-Type", "application/json")
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

func setup(t *testing.T) (*client, *core.Core) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	signer, _ := hostexec.LoadOrCreateKey(filepath.Join(dir, "key"))
	c := core.New(st, dir, "test", signer)
	stop, err := fake.Setup(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	if err := os.WriteFile(api.SetupCodePath(dir), []byte("ABCD-EFGH\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.New(c, web.FS, false))
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: srv.URL, http: &http.Client{Jar: jar}}, c
}

func TestSetupLoginAndCSRF(t *testing.T) {
	cl, _ := setup(t)
	if code, _ := cl.do("GET", "/api/overview", nil, false); code != 401 {
		t.Fatalf("overview without login: %d", code)
	}
	if code, out := cl.do("POST", "/api/setup", map[string]string{"code": "WRONG", "username": "admin", "password": "0123456789"}, false); code != 422 || out["fields"] == nil {
		t.Fatalf("wrong code: %d %v", code, out)
	}
	if code, out := cl.do("POST", "/api/setup", map[string]string{"code": "abcd-efgh", "username": "admin", "password": "kurz"}, false); code != 422 {
		t.Fatalf("short password: %d %v", code, out)
	}
	if code, out := cl.do("POST", "/api/setup", map[string]string{"code": "abcd-efgh", "username": "admin", "password": "0123456789"}, false); code != 200 {
		t.Fatalf("setup: %d %v", code, out)
	}
	if code, _ := cl.do("POST", "/api/setup", map[string]string{"code": "abcd-efgh", "username": "x", "password": "0123456789"}, false); code != 409 {
		t.Fatalf("second setup must fail: %d", code)
	}
	if code, _ := cl.do("GET", "/api/overview", nil, false); code != 200 {
		t.Fatalf("overview after setup: %d", code)
	}
	// Mutating requests need the custom header (CSRF protection).
	if code, _ := cl.do("PUT", "/api/settings", map[string]any{"notify": map[string]any{}}, false); code != 403 {
		t.Fatalf("missing X-PK95 header: %d", code)
	}
	if code, out := cl.do("PUT", "/api/settings", map[string]any{"notify": map[string]any{"min_severity": "info"}}, true); code != 200 {
		t.Fatalf("settings: %d %v", code, out)
	}
	cl.do("POST", "/api/logout", nil, false)
	if code, _ := cl.do("GET", "/api/overview", nil, false); code != 401 {
		t.Fatalf("after logout: %d", code)
	}
	if code, _ := cl.do("POST", "/api/login", map[string]string{"username": "admin", "password": "falsch-falsch"}, false); code != 401 {
		t.Fatalf("wrong password: %d", code)
	}
	if code, _ := cl.do("POST", "/api/login", map[string]string{"username": "admin", "password": "0123456789"}, false); code != 200 {
		t.Fatalf("login: %d", code)
	}
}

func login(t *testing.T) *client {
	cl, _ := setup(t)
	if code, out := cl.do("POST", "/api/setup", map[string]string{"code": "ABCD-EFGH", "username": "admin", "password": "0123456789"}, false); code != 200 {
		t.Fatalf("setup: %d %v", code, out)
	}
	return cl
}

func TestAddPBSStorageToPVE(t *testing.T) {
	cl := login(t)
	code, out := cl.do("POST", "/api/pve/cluster/storages", map[string]string{"pbs": "local", "store": "nas-backup", "name": "pbs-nas"}, true)
	if code != 200 {
		t.Fatalf("add storage: %d %v", code, out)
	}
	_, out = cl.do("GET", "/api/pve/cluster/storages", nil, false)
	found := false
	for _, s := range out["storages"].([]any) {
		m := s.(map[string]any)
		if m["storage"] == "pbs-nas" {
			found = true
			if m["server"] != "192.168.10.20" || m["datastore"] != "nas-backup" || m["username"] != "pk95-pve@pbs!pve-cluster" {
				t.Fatalf("storage %v", m)
			}
			if m["pbs_server"] != "local" {
				t.Fatalf("storage not matched to its server: %v", m)
			}
		}
	}
	if !found {
		t.Fatal("storage missing")
	}
	if code, out := cl.do("POST", "/api/pve/cluster/storages", map[string]string{"pbs": "local", "store": "x", "name": "1bad"}, true); code != 422 {
		t.Fatalf("bad name: %d %v", code, out)
	}
}

func TestRestoreNeedsStorage(t *testing.T) {
	cl := login(t)
	// offsite (external PBS) is not a storage of the cluster: clear error.
	code, out := cl.do("POST", "/api/restore", map[string]any{"cluster": "cluster", "node": "pve1", "pbs": "buero", "store": "offsite", "type": "vm", "bid": "100", "time": 1700000000, "vmid": 950, "storage": "local-lvm"}, true)
	if code != 409 {
		t.Fatalf("restore without storage: %d %v", code, out)
	}
	code, out = cl.do("POST", "/api/restore", map[string]any{"cluster": "cluster", "node": "pve1", "pbs": "local", "store": "backup", "type": "vm", "bid": "100", "time": 1700000000, "vmid": 950, "storage": "local-lvm"}, true)
	if code != 200 || out["upid"] == "" {
		t.Fatalf("restore: %d %v", code, out)
	}
}

func TestStaticSPA(t *testing.T) {
	cl, _ := setup(t)
	for _, p := range []string{"/", "/wechselplatten", "/app.js", "/style.css", "/fonts/SpaceGrotesk-Regular.woff2"} {
		resp, err := cl.http.Get(cl.base + p)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s: %v %v", p, err, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestReverseProxy(t *testing.T) {
	cl := login(t)
	get := func(xff string) map[string]any {
		req, _ := http.NewRequest("GET", cl.base+"/api/settings", nil)
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
			req.Header.Set("X-Forwarded-Proto", "https")
			req.Header.Set("X-Forwarded-Host", "backup.example.org")
		}
		resp, err := cl.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out
	}
	// Not trusted yet: the header is ignored for the client address, but the proxy is noticed.
	out := get("203.0.113.7")
	if out["client_ip"] != "127.0.0.1" {
		t.Fatalf("untrusted proxy must not set the client ip: %v", out["client_ip"])
	}
	if seen := out["seen_proxy"].(map[string]any); seen["url"] != "https://backup.example.org" || seen["proxy_ip"] != "127.0.0.1" {
		t.Fatalf("seen proxy %v", seen)
	}
	if out["public_url"] != "https://backup.example.org" {
		t.Fatalf("public url from the proxy: %v", out["public_url"])
	}
	if code, o := cl.do("PUT", "/api/settings/access", map[string]any{"public_url": "ftp://x", "trusted_proxies": []string{"kein-ip"}}, true); code != 422 {
		t.Fatalf("bad access settings: %d %v", code, o)
	}
	if code, o := cl.do("PUT", "/api/settings/access", map[string]any{"public_url": "https://pbs.example.org/", "trusted_proxies": []string{"127.0.0.1"}}, true); code != 200 {
		t.Fatalf("access: %d %v", code, o)
	}
	out = get("203.0.113.7, 10.0.0.9")
	if out["client_ip"] != "10.0.0.9" {
		t.Fatalf("trusted proxy: client ip %v", out["client_ip"])
	}
	if out["public_url"] != "https://pbs.example.org" {
		t.Fatalf("configured public url wins: %v", out["public_url"])
	}
}

func TestPVEWithoutPrivileges(t *testing.T) {
	fake.PVENoPrivs = true
	t.Cleanup(func() { fake.PVENoPrivs = false })
	cl := login(t)
	want := "pveum acl modify / --tokens 'root@pam!pk95' --roles Administrator"
	if !strings.Contains(api.PermHintForTest("u@pve!t", []string{"VM.Audit"}), "--users 'u@pve'") {
		t.Fatal("hint for a non-root user must grant the user too")
	}
	code, out := cl.do("GET", "/api/pve/cluster/jobs", nil, false)
	if code != 502 || !strings.Contains(out["error"].(string), want) || !strings.Contains(out["error"].(string), "Sys.Audit") {
		t.Fatalf("jobs: %d %v", code, out)
	}
	_, out = cl.do("GET", "/api/guests", nil, false)
	errs, _ := out["errors"].([]any)
	if len(errs) != 1 || !strings.Contains(errs[0].(string), want) {
		t.Fatalf("guests must explain the empty list: %v", out)
	}
	_, out = cl.do("GET", "/api/overview?fresh=1", nil, false)
	cls := out["overview"].(map[string]any)["clusters"].([]any)
	if m := cls[0].(map[string]any)["missing"]; m == nil || len(m.([]any)) == 0 {
		t.Fatalf("overview must list missing privileges: %v", cls[0])
	}
	// Saving the connection again is refused with the same hint.
	code, out = cl.do("PUT", "/api/pve/cluster", map[string]any{"name": "Cluster", "host": "127.0.0.1", "port": 1, "token_id": "root@pam!pk95", "fingerprint": "aa"}, true)
	if code != 422 && code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
}
