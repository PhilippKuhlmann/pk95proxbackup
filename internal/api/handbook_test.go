package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// The handbook shows the set-up but never a password, secret or key.
func TestHandbookDocument(t *testing.T) {
	admin := login(t)
	if code, out := admin.do("PUT", "/api/handbook", map[string]any{"contacts": "IT Muster <0123>", "order": "1. dc01", "notes": "Tresor"}, true); code != 200 || out["contacts"] != "IT Muster <0123>" {
		t.Fatalf("put: %d %v", code, out)
	}
	resp, err := admin.http.Get(admin.base + "/api/handbook/document")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	doc := string(b)
	if resp.StatusCode != 200 {
		t.Fatalf("document: %d %s", resp.StatusCode, doc)
	}
	for _, want := range []string{"Notfallhandbuch – Stand", "IT Muster &lt;0123&gt;", "Was tun, wenn", "install/proxmox.sh", "Cluster Zuhause", "pve1", "192.168.10.11", "Platte A", "Wo liegt welcher Server"} {
		if !strings.Contains(doc, want) {
			t.Errorf("document lacks %q", want)
		}
	}
	for _, secret := range []string{"root@pam!pk95proxbackup", `"key"`, "-----BEGIN", "password_hash"} {
		if strings.Contains(doc, secret) {
			t.Errorf("document contains %q", secret)
		}
	}
	if code, out := admin.do("POST", "/api/handbook/printed", nil, true); code != 200 || out["state"] != "current" {
		t.Fatalf("printed: %d %v", code, out)
	}
	_, out := admin.do("GET", "/api/overview", nil, false)
	if hb := out["handbook"].(map[string]any); hb["state"] != "current" {
		t.Fatalf("overview: %v", hb)
	}
	// Only administrators read it.
	if code, out := admin.do("POST", "/api/users", map[string]any{"username": "bediener", "password": "bedienen-bedienen", "role": "operator"}, true); code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	op := second(t, admin, "bediener", "bedienen-bedienen")
	for _, p := range []string{"/api/handbook", "/api/handbook/document"} {
		if code, _ := op.do("GET", p, nil, false); code != http.StatusForbidden {
			t.Errorf("operator %s: %d", p, code)
		}
	}
}
