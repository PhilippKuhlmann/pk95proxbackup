package api_test

import (
	"errors"
	"testing"
	"time"

	"pk95proxbackup/internal/api"
	"pk95proxbackup/internal/store"
	"pk95proxbackup/internal/totp"
)

func code(t *testing.T, secret string, step int64) string {
	t.Helper()
	c, err := totp.CodeAt(secret, step)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTOTPLogin(t *testing.T) {
	cl, c := setup(t)
	if st, out := cl.do("POST", "/api/setup", map[string]string{"code": "ABCD-EFGH", "username": "admin", "password": "0123456789"}, false); st != 200 {
		t.Fatalf("setup: %d %v", st, out)
	}
	if _, out := cl.do("GET", "/api/account/totp", nil, false); out["enabled"] != false {
		t.Fatalf("state: %v", out)
	}
	st, out := cl.do("POST", "/api/account/totp/setup", nil, true)
	secret, _ := out["secret"].(string)
	if st != 200 || secret == "" || len(out["qr"].(string)) < 100 {
		t.Fatalf("setup totp: %d %v", st, out)
	}
	now := totp.Step(time.Now())
	good := code(t, secret, now)
	bad := []byte(good)
	bad[0] = '0' + (bad[0]-'0'+1)%10
	if st, out := cl.do("POST", "/api/account/totp/enable", map[string]string{"code": string(bad)}, true); st != 422 {
		t.Fatalf("wrong code enabled: %d %v", st, out)
	}
	st, out = cl.do("POST", "/api/account/totp/enable", map[string]string{"code": good}, true)
	rec, _ := out["recovery"].([]any)
	if st != 200 || len(rec) != 10 {
		t.Fatalf("enable: %d %v", st, out)
	}
	if _, out := cl.do("GET", "/api/account/totp", nil, false); out["enabled"] != true || out["recovery_left"] != float64(10) {
		t.Fatalf("state after enable: %v", out)
	}

	// Password alone no longer opens a session.
	cl.do("POST", "/api/logout", nil, false)
	st, out = cl.do("POST", "/api/login", map[string]string{"username": "admin", "password": "0123456789"}, false)
	ticket, _ := out["ticket"].(string)
	if st != 200 || out["totp"] != true || ticket == "" || out["user"] != nil {
		t.Fatalf("login step 1: %d %v", st, out)
	}
	if st, _ := cl.do("GET", "/api/overview", nil, false); st != 401 {
		t.Fatalf("session after password only: %d", st)
	}
	if st, out := cl.do("POST", "/api/login/totp", map[string]string{"ticket": ticket, "code": good}, false); st != 422 {
		t.Fatalf("replayed code accepted: %d %v", st, out)
	}
	if st, out := cl.do("POST", "/api/login/totp", map[string]string{"ticket": ticket, "code": code(t, secret, now+1)}, false); st != 200 || out["user"] != "admin" {
		t.Fatalf("login step 2: %d %v", st, out)
	}
	if st, _ := cl.do("GET", "/api/overview", nil, false); st != 200 {
		t.Fatalf("overview after 2FA: %d", st)
	}
	if st, _ := cl.do("POST", "/api/login/totp", map[string]string{"ticket": ticket, "code": code(t, secret, now)}, false); st != 401 {
		t.Fatalf("ticket used twice: %d", st)
	}

	// Recovery code: once.
	login2 := func() string {
		cl.do("POST", "/api/logout", nil, false)
		_, out := cl.do("POST", "/api/login", map[string]string{"username": "admin", "password": "0123456789"}, false)
		return out["ticket"].(string)
	}
	tk := login2()
	if st, out := cl.do("POST", "/api/login/totp", map[string]string{"ticket": tk, "code": rec[0].(string)}, false); st != 200 || out["recovery_left"] != float64(9) {
		t.Fatalf("recovery login: %d %v", st, out)
	}
	tk = login2()
	if st, _ := cl.do("POST", "/api/login/totp", map[string]string{"ticket": tk, "code": rec[0].(string)}, false); st != 422 {
		t.Fatalf("recovery code used twice: %d", st)
	}
	// Too many wrong codes end the ticket.
	for i := 0; i < 3; i++ {
		cl.do("POST", "/api/login/totp", map[string]string{"ticket": tk, "code": "AAAA-BBBB-CCCC"}, false)
	}
	if st, _ := cl.do("POST", "/api/login/totp", map[string]string{"ticket": tk, "code": "AAAA-BBBB-CCCC"}, false); st != 401 {
		t.Fatalf("ticket after 5 wrong codes: %d", st)
	}
	tk = login2()
	if st, _ := cl.do("POST", "/api/login/totp", map[string]string{"ticket": tk, "code": rec[1].(string)}, false); st != 200 {
		t.Fatalf("login with second recovery code: %d", st)
	}

	// New recovery codes and switching off need the password.
	if st, _ := cl.do("POST", "/api/account/totp/recovery", map[string]string{"password": "falsch"}, true); st != 422 {
		t.Fatalf("recovery without password: %d", st)
	}
	if st, out := cl.do("POST", "/api/account/totp/recovery", map[string]string{"password": "0123456789"}, true); st != 200 || len(out["recovery"].([]any)) != 10 {
		t.Fatalf("new recovery codes: %d %v", st, out)
	}
	if st, _ := cl.do("POST", "/api/account/totp/disable", map[string]string{"password": "falsch"}, true); st != 422 {
		t.Fatalf("disable without password: %d", st)
	}
	if st, _ := cl.do("POST", "/api/account/totp/disable", map[string]string{"password": "0123456789"}, true); st != 200 {
		t.Fatalf("disable: %d", st)
	}
	cl.do("POST", "/api/logout", nil, false)
	if st, out := cl.do("POST", "/api/login", map[string]string{"username": "admin", "password": "0123456789"}, false); st != 200 || out["user"] != "admin" {
		t.Fatalf("plain login after disable: %d %v", st, out)
	}
	if err := api.DisableTOTP(c.St, "niemand"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

// The console rescue switches 2FA off for an account.
func TestTOTPConsoleRescue(t *testing.T) {
	cl, c := setup(t)
	cl.do("POST", "/api/setup", map[string]string{"code": "ABCD-EFGH", "username": "admin", "password": "0123456789"}, false)
	_, out := cl.do("POST", "/api/account/totp/setup", nil, true)
	cl.do("POST", "/api/account/totp/enable", map[string]string{"code": code(t, out["secret"].(string), totp.Step(time.Now()))}, true)
	if err := api.DisableTOTP(c.St, "admin"); err != nil {
		t.Fatal(err)
	}
	cl.do("POST", "/api/logout", nil, false)
	if st, out := cl.do("POST", "/api/login", map[string]string{"username": "admin", "password": "0123456789"}, false); st != 200 || out["user"] != "admin" {
		t.Fatalf("login after rescue: %d %v", st, out)
	}
}
