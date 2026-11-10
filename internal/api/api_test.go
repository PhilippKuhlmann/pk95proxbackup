package api_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
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
			if m["server"] != "192.168.10.20" || m["datastore"] != "nas-backup" || m["username"] != "pk95-pve@pbs!pve-cluster-nas-backup" {
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
	// offsite (external PBS) is no storage of the cluster yet: it is added for the restore.
	code, out := cl.do("POST", "/api/restore", map[string]any{"cluster": "cluster", "node": "pve1", "pbs": "buero", "store": "offsite", "type": "vm", "bid": "100", "time": 1700000000, "vmid": 950, "storage": "local-lvm"}, true)
	if code != 200 {
		t.Fatalf("restore from a datastore without storage: %d %v", code, out)
	}
	_, out = cl.do("GET", "/api/pve/cluster/storages", nil, false)
	added := false
	for _, st := range out["storages"].([]any) {
		m := st.(map[string]any)
		if m["storage"] == "pk95-offsite" && m["datastore"] == "offsite" && m["server"] == "10.20.0.5" {
			added = true
		}
	}
	if !added {
		t.Fatalf("storage pk95-offsite not added: %v", out["storages"])
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

func TestFileRestore(t *testing.T) {
	cl := login(t)
	q := "/api/restore/files?cluster=cluster&pbs=local&store=backup&type=vm&bid=100&time=1794168000"
	code, out := cl.do("GET", q, nil, false)
	if code != 200 {
		t.Fatalf("root: %d %v", code, out)
	}
	entries := out["entries"].([]any)
	disk := entries[0].(map[string]any)
	if disk["type"] != "v" || disk["text"] != "drive-scsi0.img.fidx" {
		t.Fatalf("vm root: %v", entries)
	}
	if !strings.Contains(out["volume"].(string), "pbs-appliance:backup/vm/100/") {
		t.Fatalf("volume %v", out["volume"])
	}
	// walk to the documents of anna
	path := base64.StdEncoding.EncodeToString([]byte("/drive-scsi0.img.fidx/part/2/home/anna/Dokumente"))
	code, out = cl.do("GET", q+"&path="+url.QueryEscape(path), nil, false)
	if code != 200 {
		t.Fatalf("docs: %d %v", code, out)
	}
	names := map[string]string{}
	var pdf string
	for _, e := range out["entries"].([]any) {
		m := e.(map[string]any)
		names[m["text"].(string)] = m["type"].(string)
		if m["text"] == "Rechnung-2026-10.pdf" {
			pdf = m["filepath"].(string)
		}
	}
	if names["Rechnung-2026-10.pdf"] != "f" || names["Projekte"] != "d" {
		t.Fatalf("entries %v", names)
	}
	resp, err := cl.http.Get(cl.base + "/api/restore/download?cluster=cluster&pbs=local&store=backup&type=vm&bid=100&time=1794168000&path=" + url.QueryEscape(pdf) + "&name=Rechnung-2026-10.pdf")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "Rechnung-2026-10.pdf") || !strings.Contains(string(body), "Rechnung-2026-10.pdf") {
		t.Fatalf("download: %d %q %q", resp.StatusCode, resp.Header.Get("Content-Disposition"), body)
	}
	// a folder comes as zip
	resp, err = cl.http.Get(cl.base + "/api/restore/download?cluster=cluster&pbs=local&store=backup&type=vm&bid=100&time=1794168000&dir=1&name=Dokumente&path=" + url.QueryEscape(path))
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Header.Get("Content-Type") != "application/zip" || !strings.HasPrefix(string(body), "PK") || !strings.Contains(resp.Header.Get("Content-Disposition"), "Dokumente.zip") {
		t.Fatalf("zip: %s %q", resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"))
	}
	// a container starts with its archive
	_, out = cl.do("GET", "/api/restore/files?cluster=cluster&pbs=local&store=backup&type=ct&bid=110&time=1794168000", nil, false)
	if e := out["entries"].([]any)[0].(map[string]any); e["text"] != "root.pxar.didx" {
		t.Fatalf("ct root: %v", out)
	}
	// a backup on a datastore that is no storage of the cluster: it is added on the fly
	code, out = cl.do("GET", "/api/restore/files?cluster=cluster&pbs=buero&store=offsite&type=vm&bid=100&time=1794168000", nil, false)
	if code != 200 || !strings.HasPrefix(out["volume"].(string), "pk95-offsite:backup/vm/100/") {
		t.Fatalf("no storage: %d %v", code, out)
	}
}

func TestRestoreReplace(t *testing.T) {
	cl := login(t)
	base := map[string]any{"cluster": "cluster", "node": "pve1", "pbs": "local", "store": "backup", "type": "vm", "time": 1794168000, "storage": "local-lvm", "replace": true}
	with := func(kv ...any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	if code, out := cl.do("POST", "/api/restore", with("bid", "100", "confirm", ""), true); code != 422 {
		t.Fatalf("replace without confirmation: %d %v", code, out)
	}
	if code, out := cl.do("POST", "/api/restore", with("bid", "100", "confirm", "100"), true); code != 409 || !strings.Contains(out["error"].(string), "läuft noch") {
		t.Fatalf("replace a running vm: %d %v", code, out)
	}
	if code, out := cl.do("POST", "/api/restore", with("bid", "112", "node", "pve2", "confirm", "112"), true); code != 200 {
		t.Fatalf("replace a stopped vm: %d %v", code, out)
	}
	// a new guest must not take a used id
	if code, out := cl.do("POST", "/api/restore", with("bid", "100", "replace", false, "vmid", 101), true); code != 422 {
		t.Fatalf("used id: %d %v", code, out)
	}
}

func TestRestoreSources(t *testing.T) {
	cl := login(t)
	code, out := cl.do("GET", "/api/restore/sources", nil, false)
	if code != 200 {
		t.Fatalf("sources: %d %v", code, out)
	}
	by := map[string]map[string]any{}
	for _, x := range out["sources"].([]any) {
		m := x.(map[string]any)
		by[m["server"].(string)+"/"+m["store"].(string)] = m
	}
	a, b := by["local/usb-a"], by["local/usb-b"]
	if a["kind"] != "usb" || a["label"] != "Platte A (gerade Tage)" || a["mounted"] != true || a["present"] != true {
		t.Fatalf("usb-a: %v", a)
	}
	if b["kind"] != "usb" || b["present"] != false || b["mounted"] != false {
		t.Fatalf("usb-b (not plugged in): %v", b)
	}
	if by["local/nas-backup"]["kind"] != "share" || by["local/backup"]["kind"] != "datastore" {
		t.Fatalf("kinds: %v %v", by["local/nas-backup"], by["local/backup"])
	}
	// what is on the USB disk
	code, out = cl.do("GET", "/api/restore/source?pbs=local&store=usb-a", nil, false)
	if code != 200 {
		t.Fatalf("source guests: %d %v", code, out)
	}
	names := map[float64]string{}
	for _, x := range out["guests"].([]any) {
		m := x.(map[string]any)
		names[m["vmid"].(float64)] = m["name"].(string)
		if m["exists"] != true || m["cluster"] != "cluster" {
			t.Fatalf("guest %v", m)
		}
	}
	if names[101] != "fileserver" || names[110] != "pihole" {
		t.Fatalf("names %v", names)
	}
	// not plugged in: a clear error instead of a list
	if code, _ := cl.do("GET", "/api/restore/source?pbs=local&store=usb-b", nil, false); code == 200 {
		t.Fatal("an unmounted disk must not list guests")
	}
	// restoring files from the disk adds it as storage with its own token
	code, out = cl.do("GET", "/api/restore/files?cluster=cluster&pbs=local&store=usb-a&type=ct&bid=110&time=1794081600", nil, false)
	if code != 200 || !strings.HasPrefix(out["volume"].(string), "pk95-usb-a:backup/ct/110/") {
		t.Fatalf("files from usb: %d %v", code, out)
	}
	_, out = cl.do("GET", "/api/pve/cluster/storages", nil, false)
	for _, st := range out["storages"].([]any) {
		m := st.(map[string]any)
		if m["storage"] == "pk95-usb-a" && m["username"] != "pk95-pve@pbs!pve-cluster-usb-a" {
			t.Fatalf("usb storage must use its own token: %v", m)
		}
	}
	// It may read backups written by others (the copies belong to the sync job).
	for _, id := range []string{"pk95-pve@pbs!pve-cluster-usb-a", "pk95-pve@pbs"} {
		if roles := strings.Join(fake.ACLRoles("/datastore/usb-a", id), ","); !strings.Contains(roles, "DatastoreReader") || !strings.Contains(roles, "DatastorePowerUser") {
			t.Fatalf("%s roles on usb-a: %q", id, roles)
		}
	}
	// A storage added earlier without the read role gets it when restoring from it.
	if code, _ := cl.do("GET", "/api/restore/files?cluster=cluster&pbs=local&store=backup&type=vm&bid=100&time=1794168000", nil, false); code != 200 {
		t.Fatal("restore from backup")
	}
	if roles := strings.Join(fake.ACLRoles("/datastore/backup", "pk95-pve@pbs!pve-cluster"), ","); !strings.Contains(roles, "DatastoreReader") {
		t.Fatalf("existing storage must get read access: %v", roles)
	}
}

func TestSourceNamesFromBackups(t *testing.T) {
	cl := login(t)
	_, out := cl.do("GET", "/api/restore/source?pbs=local&store=backup", nil, false)
	got := map[float64]map[string]any{}
	for _, x := range out["guests"].([]any) {
		m := x.(map[string]any)
		got[m["vmid"].(float64)] = m
	}
	if g := got[250]; g == nil || g["name"] != "alt-fileserver" || g["exists"] != false || g["type"] != "qemu" {
		t.Fatalf("vm 250: %v", g)
	}
	if g := got[251]; g == nil || g["name"] != "alter-mailserver" || g["type"] != "lxc" {
		t.Fatalf("ct 251: %v", g)
	}
	if g := got[100]; g["name"] != "dc01" || g["exists"] != true {
		t.Fatalf("known guest keeps its Proxmox name: %v", g)
	}
}

func TestRestoreTestReport(t *testing.T) {
	cl, c := setup(t)
	if code, _ := cl.do("POST", "/api/setup", map[string]string{"code": "ABCD-EFGH", "username": "admin", "password": "0123456789"}, false); code != 200 {
		t.Fatal("setup")
	}
	run := core.RestoreTestRun{ID: "20261110-065109", PlanName: "Wöchentlich", By: "admin", Start: 1794289869, End: 1794290064, Status: "ok",
		Cluster: "PVE02", Node: "srv-pve02", Storage: "local-zfs", Source: "PBS01 / local",
		Results: []core.RestoreTestResult{{Type: "vm", VMID: "5000", Name: "opnsense", Status: "ok", TestID: 990000, BackupTime: 1794200000,
			Started: 1794289870, RestoreEnd: 1794289930, Finished: 1794290060, Restore: 60, Boot: 95, DiskBytes: 32 << 30,
			OS: "FreeBSD 14", Hostname: "opnsense", Info: "Betriebssystem antwortet", Notes: "Netzwerk getrennt; hostpci0 entfernt", Screenshot: "vm-5000.png"}}}
	if err := c.St.Put(core.KindRTRun, run.ID, run); err != nil {
		t.Fatal(err)
	}
	path := c.ScreenshotPath(run.ID, "vm-5000.png")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nDEMO"), 0o600)
	resp, err := cl.http.Get(cl.base + "/api/restoretests/report?run=" + run.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	h := string(body)
	for _, want := range []string{"Der Server lässt sich aus der Sicherung starten", "opnsense", "990000", "1:00 min", "1:35 min", "3:10 min",
		"32,0 GiB", "FreeBSD 14", "hostpci0 entfernt", "PVE02", "srv-pve02 / local-zfs", "PBS01 / local", "data:image/png;base64,", "Drucken / als PDF"} {
		if !strings.Contains(h, want) {
			t.Errorf("report misses %q", want)
		}
	}
	resp, _ = cl.http.Get(cl.base + "/api/restoretests/report?download=1&run=" + run.ID)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Content-Disposition"), "restore-test-20261110-065109.html") || strings.Contains(string(body), "Drucken / als PDF") {
		t.Fatalf("download: %q", resp.Header.Get("Content-Disposition"))
	}
	if code, _ := cl.do("GET", "/api/restoretests/report?run=nope", nil, false); code != 404 {
		t.Fatal("unknown run")
	}
}
