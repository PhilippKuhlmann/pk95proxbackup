package api_test

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/totp"
)

// second signs in as another account in the same appliance.
func second(t *testing.T, admin *client, user, pw string) *client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: admin.base, http: &http.Client{Jar: jar}}
	if code, out := c.do("POST", "/api/login", map[string]string{"username": user, "password": pw}, false); code != 200 {
		t.Fatalf("login %s: %d %v", user, code, out)
	}
	return c
}

func num(v any) string { return fmt.Sprint(int64(v.(float64))) }

func TestRoles(t *testing.T) {
	admin := login(t)
	for _, u := range []map[string]any{
		{"username": "leser", "password": "lesen-lesen-1", "role": "viewer"},
		{"username": "bediener", "password": "bedienen-bedienen", "role": "operator"},
	} {
		if code, out := admin.do("POST", "/api/users", u, true); code != 200 {
			t.Fatalf("create %v: %d %v", u["username"], code, out)
		}
	}
	if code, _ := admin.do("POST", "/api/users", map[string]any{"username": "leser", "password": "0123456789", "role": "viewer"}, true); code != 422 {
		t.Fatal("duplicate account")
	}
	viewer := second(t, admin, "leser", "lesen-lesen-1")
	operator := second(t, admin, "bediener", "bedienen-bedienen")
	if _, out := viewer.do("GET", "/api/session", nil, false); out["role"] != "viewer" {
		t.Fatalf("session: %v", out)
	}

	// reading is fine for everybody
	if code, _ := viewer.do("GET", "/api/overview", nil, false); code != 200 {
		t.Fatal("viewer cannot read the overview")
	}
	// the viewer changes nothing and reads no backup contents or settings
	backup := map[string]any{"cluster": "cluster", "node": "pve1", "vmid": 100, "storage": "pbs-appliance"}
	for _, x := range []struct{ m, p string }{{"POST", "/api/guests/backup"}, {"PUT", "/api/settings"}, {"GET", "/api/settings"}, {"GET", "/api/restore/download?x=1"}, {"GET", "/api/users"}} {
		var body any
		if x.m != "GET" {
			body = backup
		}
		if code, out := viewer.do(x.m, x.p, body, true); code != 403 || !strings.Contains(fmt.Sprint(out["error"]), "Nur lesen") {
			t.Errorf("viewer %s %s: %d %v", x.m, x.p, code, out)
		}
	}
	// the operator does day-to-day work, but no configuration
	if code, out := operator.do("POST", "/api/guests/backup", backup, true); code != 200 {
		t.Fatalf("operator backup: %d %v", code, out)
	}
	if code, out := operator.do("POST", "/api/pbs/local/datastores/backup/verify", nil, true); code != 200 {
		t.Fatalf("operator verify: %d %v", code, out)
	}
	if code, _ := operator.do("POST", "/api/rotations/demo/disks/a1/rebuild", nil, true); code != 403 {
		t.Fatal("operator may not format a rotation disk")
	}
	if code, _ := operator.do("DELETE", "/api/pbs/local/datastores/backup", nil, true); code != 403 {
		t.Fatal("operator removed a datastore")
	}
	// own password: every role
	if code, out := viewer.do("POST", "/api/account/password", map[string]string{"current": "lesen-lesen-1", "new": "lesen-lesen-2"}, true); code != 200 {
		t.Fatalf("own password: %d %v", code, out)
	}

	// the last admin stays
	_, out := admin.do("GET", "/api/users", nil, false)
	ids := map[string]string{}
	for _, u := range out["users"].([]any) {
		m := u.(map[string]any)
		ids[m["username"].(string)] = num(m["id"])
	}
	if code, _ := admin.do("PUT", "/api/users/"+ids["admin"], map[string]any{"role": "viewer"}, true); code != 409 {
		t.Fatal("last admin demoted")
	}
	if code, _ := admin.do("DELETE", "/api/users/"+ids["admin"], nil, true); code != 409 {
		t.Fatal("own account deleted")
	}
	// promote the viewer: takes effect at once
	if code, _ := admin.do("PUT", "/api/users/"+ids["leser"], map[string]any{"role": "admin"}, true); code != 200 {
		t.Fatal("promote")
	}
	if code, _ := viewer.do("GET", "/api/users", nil, false); code != 200 {
		t.Fatal("new role not effective")
	}

	// audit log: changes, refused attempts, sign-ins
	_, out = admin.do("GET", "/api/audit", nil, false)
	var log strings.Builder
	for _, e := range out["entries"].([]any) {
		m := e.(map[string]any)
		fmt.Fprintf(&log, "%s %s %s\n", m["user"], m["action"], num(m["status"]))
	}
	for _, want := range []string{"bediener Gast gesichert 200", "leser Gast gesichert 403", "admin Konto angelegt 200", "bediener Anmeldung 200", "leser Eigenes Passwort geändert 200"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("audit log misses %q:\n%s", want, log.String())
		}
	}
	if code, out := admin.do("POST", "/api/login", map[string]string{"username": "bediener", "password": "falsch-falsch"}, false); code != 401 {
		t.Fatalf("wrong password: %d %v", code, out)
	}
	if _, out = admin.do("GET", "/api/audit?user=bediener", nil, false); !strings.Contains(fmt.Sprint(out["entries"].([]any)[0].(map[string]any)["action"]), "fehlgeschlagen") {
		t.Fatalf("failed sign-in not logged: %v", out)
	}
}

// An account with two-factor duty must set it up before it can do anything else.
func TestTOTPDuty(t *testing.T) {
	admin := login(t)
	if code, out := admin.do("POST", "/api/users", map[string]any{"username": "chefin", "password": "geheim-geheim", "role": "operator", "totp_required": true}, true); code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	c := second(t, admin, "chefin", "geheim-geheim")
	if _, out := c.do("GET", "/api/session", nil, false); out["totp_required"] != true {
		t.Fatalf("session: %v", out)
	}
	if code, out := c.do("GET", "/api/overview", nil, false); code != 403 || out["totp_required"] != true {
		t.Fatalf("overview before setup: %d %v", code, out)
	}
	_, out := c.do("POST", "/api/account/totp/setup", nil, true)
	secret := strings.ReplaceAll(out["secret"].(string), " ", "")
	code, _ := totp.CodeAt(secret, totp.Step(time.Now()))
	if st, out := c.do("POST", "/api/account/totp/enable", map[string]string{"code": code}, true); st != 200 {
		t.Fatalf("enable: %d %v", st, out)
	}
	if st, _ := c.do("GET", "/api/overview", nil, false); st != 200 {
		t.Fatal("still blocked after setup")
	}
}
