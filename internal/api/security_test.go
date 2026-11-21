package api_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// adminAndCore sets up the appliance with its first account and returns the data dir too.
func adminAndCore(t *testing.T) (*client, string) {
	t.Helper()
	cl, c := setup(t)
	if code, out := cl.do("POST", "/api/setup", map[string]string{"code": "ABCD-EFGH", "username": "admin", "password": "0123456789"}, false); code != 200 {
		t.Fatalf("setup: %d %v", code, out)
	}
	return cl, c.DataDir
}

func account(t *testing.T, admin *client, user, role string) *client {
	t.Helper()
	pw := user + "-" + user + "-1"
	if code, out := admin.do("POST", "/api/users", map[string]any{"username": user, "password": pw, "role": role}, true); code != 200 {
		t.Fatalf("create %s: %d %v", user, code, out)
	}
	return second(t, admin, user, pw)
}

// The screenshot route hands out only pictures a restore test recorded – never files of the
// data directory (SSH key, database, TLS key).
func TestScreenshotOnlyRecordedPictures(t *testing.T) {
	admin, dataDir := adminAndCore(t)
	viewer := account(t, admin, "leser", "viewer")
	for _, q := range []string{"run=..&file=ssh_ed25519", "run=..&file=pk95proxbackup.db", "run=.&file=key.pem", "run=x&file=.."} {
		if code, _ := viewer.do("GET", "/api/restoretests/screenshot?"+q, nil, false); code == 200 {
			t.Errorf("%s: served", q)
		}
	}
	// a file that is there but was not recorded by a run
	_ = os.MkdirAll(filepath.Join(dataDir, "restoretests", "lauf1"), 0o700)
	_ = os.WriteFile(filepath.Join(dataDir, "restoretests", "lauf1", "vm-100.png"), []byte("x"), 0o600)
	if code, _ := viewer.do("GET", "/api/restoretests/screenshot?run=lauf1&file=vm-100.png", nil, false); code != 404 {
		t.Fatalf("unrecorded picture: %d", code)
	}
}

// Reading a foreign disk mounts it as root on the Backup Server: administrators only.
func TestForeignScanNeedsAdmin(t *testing.T) {
	admin, _ := adminAndCore(t)
	viewer := account(t, admin, "leser", "viewer")
	for _, p := range []string{"/api/pbs/local/foreign/scan?uuid=0000", "/api/pbs/local/foreign"} {
		if code, _ := viewer.do("GET", p, nil, false); code != 403 {
			t.Errorf("viewer %s: %d", p, code)
		}
	}
}

// Browsing a backup never hands a PBS token or an encryption key to a cluster on behalf of a
// non-administrator; and each storage gets its own token, so adding one never revokes another.
func TestStorageCreationNeedsAdmin(t *testing.T) {
	admin, _ := adminAndCore(t)
	files := account(t, admin, "kunde", "files")
	code, out := files.do("GET", "/api/restore/files?cluster=cluster&pbs=local&store=nas-backup&type=vm&bid=100&time=1794168000", nil, false)
	if code == 200 || !strings.Contains(fmt.Sprint(out["error"]), "Administrator") {
		t.Fatalf("files account added a storage: %d %v", code, out)
	}
	storagesOn := func(store string) map[string]string {
		_, out := admin.do("GET", "/api/pve/cluster/storages", nil, false)
		got := map[string]string{}
		for _, s := range out["storages"].([]any) {
			m := s.(map[string]any)
			if m["datastore"] == store {
				got[fmt.Sprint(m["storage"])] = fmt.Sprint(m["username"])
			}
		}
		return got
	}
	if got := storagesOn("nas-backup"); len(got) != 0 {
		t.Fatalf("storage added: %v", got)
	}
	for _, name := range []string{"nas-a", "nas-b"} {
		if code, out := admin.do("POST", "/api/pve/cluster/storages", map[string]string{"pbs": "local", "store": "nas-backup", "name": name}, true); code != 200 {
			t.Fatalf("add %s: %d %v", name, code, out)
		}
	}
	got := storagesOn("nas-backup")
	if len(got) != 2 || got["nas-a"] == got["nas-b"] {
		t.Fatalf("two storages must have two tokens: %v", got)
	}
	// an existing name is refused before any token is touched
	if code, _ := admin.do("POST", "/api/pve/cluster/storages", map[string]string{"pbs": "local", "store": "nas-backup", "name": "nas-a"}, true); code == 200 {
		t.Fatal("duplicate storage name accepted")
	}
}

// Requests at the same time count against the login limit as well.
func TestLoginLimitConcurrent(t *testing.T) {
	cl := login(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[int]int{}
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(cl.base+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"falsch-falsch"}`))
			if err != nil {
				return
			}
			resp.Body.Close()
			mu.Lock()
			codes[resp.StatusCode]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if codes[http.StatusUnauthorized] > 10 || codes[http.StatusTooManyRequests] < 20 {
		t.Fatalf("checked %d passwords (limit 10): %v", codes[http.StatusUnauthorized], codes)
	}
}
