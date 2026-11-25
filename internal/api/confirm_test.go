package api_test

import (
	"fmt"
	"testing"
)

// confirmToken gives the password once more and returns the token ("" when refused).
func confirmToken(t *testing.T, cl *client, password string) string {
	t.Helper()
	code, out := cl.do("POST", "/api/account/confirm", map[string]string{"password": password}, true)
	if code != 200 {
		return ""
	}
	return fmt.Sprint(out["token"])
}

// Deleting a backup needs the password again: a token that is good once, for this account and
// this session only.
func TestDeleteNeedsPassword(t *testing.T) {
	admin, _ := adminAndCore(t)
	_, snaps := admin.do("GET", "/api/guests/snapshots?pbs=local&store=backup&type=ct&bid=110", nil, false)
	list := snaps["snapshots"].([]any)
	at := func(i int) string {
		return fmt.Sprintf("/api/pbs/local/datastores/backup/snapshots?type=ct&bid=110&time=%v", list[i].(map[string]any)["backup-time"])
	}
	// without the password: nothing happens
	if code, out := admin.do("DELETE", at(0), nil, true); code != 403 || out["confirm_required"] != true {
		t.Fatalf("deleted without password: %d %v", code, out)
	}
	// a wrong password gives no token – and does not sign out
	if code, out := admin.do("POST", "/api/account/confirm", map[string]string{"password": "falsch-falsch"}, true); code != 403 || out["token"] != nil {
		t.Fatalf("wrong password: %d %v", code, out)
	}
	if code, _ := admin.do("GET", "/api/session", nil, false); code != 200 {
		t.Fatalf("signed out after a wrong password: %d", code)
	}
	tok := confirmToken(t, admin, "0123456789")
	if tok == "" {
		t.Fatal("no token for the right password")
	}
	hdr := map[string]string{"X-PK95-Confirm": tok}
	if code, out := admin.doWith("DELETE", at(0), nil, true, hdr); code != 200 {
		t.Fatalf("delete with token: %d %v", code, out)
	}
	// the token is used up
	if code, _ := admin.doWith("DELETE", at(1), nil, true, hdr); code != 403 {
		t.Fatalf("token used twice: %d", code)
	}
	// another administrator (another session) cannot use somebody else's token
	other := account(t, admin, "zweit", "admin")
	tok = confirmToken(t, admin, "0123456789")
	if code, _ := other.doWith("DELETE", at(1), nil, true, map[string]string{"X-PK95-Confirm": tok}); code != 403 {
		t.Fatalf("token of another session accepted: %d", code)
	}
	if tok2 := confirmToken(t, other, "0123456789"); tok2 != "" {
		t.Fatal("the password of another account gave a token")
	}

	// a datastore removed together with its backups: the password as well
	if code, out := admin.do("DELETE", "/api/pbs/local/datastores/nas-backup?destroy=1", nil, true); code != 403 || out["confirm_required"] != true {
		t.Fatalf("datastore destroyed without password: %d %v", code, out)
	}
	hdr = map[string]string{"X-PK95-Confirm": confirmToken(t, admin, "0123456789")}
	if code, out := admin.doWith("DELETE", "/api/pbs/local/datastores/nas-backup?destroy=1", nil, true, hdr); code != 200 {
		t.Fatalf("destroy with password: %d %v", code, out)
	}
}
