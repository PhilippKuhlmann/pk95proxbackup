package enckey

import (
	"encoding/json"
	"strings"
	"testing"
)

// A key made with "proxmox-backup-client key create --kdf none" (PBS 4) and its fingerprint.
const realKey = `{"kdf":null,"created":"2026-11-14T09:34:56+01:00","modified":"2026-11-14T09:34:56+01:00","data":"B+I3jBVKr5Mp/d+bQ0QkM/DRU9VpPrjv7nJOTncZOnc=","fingerprint":"a5:e3:68:7c:17:c9:80:dd:6a:1b:db:ee:ef:d0:48:de:52:ea:dd:6c:b0:14:ba:80:e4:1f:63:c9:f2:4d:e1:18"}`
const realFP = "a5:e3:68:7c:17:c9:80:dd:6a:1b:db:ee:ef:d0:48:de:52:ea:dd:6c:b0:14:ba:80:e4:1f:63:c9:f2:4d:e1:18"

func TestParseRealKey(t *testing.T) {
	js, fp, err := Parse(realKey)
	if err != nil || fp != realFP {
		t.Fatalf("Parse = %q, %v", fp, err)
	}
	if !strings.Contains(js, `"created":"2026-11-14T09:34:56+01:00"`) || !strings.Contains(js, `"kdf":null`) {
		t.Fatalf("key changed: %s", js)
	}
	// paper form with BEGIN/END and indentation
	if _, fp2, err := Parse("Notiz\n" + Paper(realKey) + "\n"); err != nil || fp2 != realFP {
		t.Fatalf("paper: %q, %v", fp2, err)
	}
	// without fingerprint: computed
	var f File
	_ = json.Unmarshal([]byte(realKey), &f)
	f.Fingerprint = ""
	b, _ := json.Marshal(f)
	if _, fp3, err := Parse(string(b)); err != nil || fp3 != realFP {
		t.Fatalf("no fingerprint: %q, %v", fp3, err)
	}
}

func TestParseRejects(t *testing.T) {
	typo := strings.Replace(realKey, "B+I3", "B+I4", 1)
	for name, in := range map[string]string{
		"empty":    " ",
		"no json":  "hallo",
		"typo":     typo,
		"short":    `{"kdf":null,"data":"AAAA"}`,
		"password": `{"kdf":{"Scrypt":{"n":65536,"r":8,"p":1,"salt":"x"}},"data":"B+I3jBVKr5Mp/d+bQ0QkM/DRU9VpPrjv7nJOTncZOnc="}`,
	} {
		if _, _, err := Parse(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestNew(t *testing.T) {
	js, fp := New()
	js2, fp2, err := Parse(js)
	if err != nil || fp2 != fp || js2 != js {
		t.Fatalf("new key does not parse back: %v %s %s", err, fp, fp2)
	}
	if len(fp) != 95 || Short(fp) != fp[:23] || len(ID(fp)) != 64 {
		t.Fatalf("fingerprint form: %s", fp)
	}
	if a, _ := New(); a == js {
		t.Fatal("keys repeat")
	}
}

// The paper form looks like the one of proxmox-backup-client (same field order).
func TestPaper(t *testing.T) {
	want := "-----BEGIN PROXMOX BACKUP KEY-----\n{\n  \"kdf\": null,\n  \"created\": \"2026-11-14T09:34:56+01:00\",\n  \"modified\": \"2026-11-14T09:34:56+01:00\",\n" +
		"  \"data\": \"B+I3jBVKr5Mp/d+bQ0QkM/DRU9VpPrjv7nJOTncZOnc=\",\n  \"fingerprint\": \"" + realFP + "\"\n}\n-----END PROXMOX BACKUP KEY-----\n"
	if got := Paper(realKey); got != want {
		t.Fatalf("paper:\n%s\nwant:\n%s", got, want)
	}
}
