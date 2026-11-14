package totp

import (
	"strings"
	"testing"
	"time"
)

// RFC 6238 appendix B (SHA-1 key "12345678901234567890"), last six digits.
func TestRFCVectors(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	for ts, want := range map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037"} {
		got, err := CodeAt(secret, ts/Period)
		if err != nil || got != want {
			t.Fatalf("t=%d: %s %v, want %s", ts, got, err, want)
		}
	}
}

func TestVerifySkewAndReplay(t *testing.T) {
	s := NewSecret()
	now := time.Unix(1_800_000_000, 0)
	cur := Step(now)
	prev, _ := CodeAt(s, cur-1)
	next, _ := CodeAt(s, cur+1)
	far, _ := CodeAt(s, cur+3)
	if st, ok := Verify(s, prev, now, 0); !ok || st != cur-1 {
		t.Fatal("previous step must be accepted (clock drift)")
	}
	if _, ok := Verify(s, next[:3]+" "+next[3:], now, 0); !ok {
		t.Fatal("next step with space must be accepted")
	}
	if _, ok := Verify(s, far, now, 0); ok {
		t.Fatal("code three steps ahead must be rejected")
	}
	if _, ok := Verify(s, prev, now, cur-1); ok {
		t.Fatal("used step must not be accepted again")
	}
	if _, ok := Verify(s, "12345", now, 0); ok {
		t.Fatal("short code accepted")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, hashes := NewRecoveryCodes(10)
	if len(codes) != 10 || len(hashes) != 10 || len(codes[0]) != 14 || strings.ContainsAny(codes[0], "01OIL") {
		t.Fatalf("codes: %v", codes)
	}
	rest, ok := UseRecovery(hashes, strings.ToLower(strings.ReplaceAll(codes[3], "-", " ")))
	if !ok || len(rest) != 9 {
		t.Fatal("recovery code (lower case, spaces) not accepted")
	}
	if _, ok := UseRecovery(rest, codes[3]); ok {
		t.Fatal("recovery code used twice")
	}
	if len(hashes) != 10 {
		t.Fatal("input slice was changed")
	}
}

func TestURIAndQR(t *testing.T) {
	u := URI("PK95 ProxBackup", "admin@pbs", "ABCDEFGH")
	if !strings.HasPrefix(u, "otpauth://totp/PK95%20ProxBackup:admin@pbs?") || !strings.Contains(u, "secret=ABCDEFGH") || !strings.Contains(u, "issuer=PK95+ProxBackup") {
		t.Fatalf("uri: %s", u)
	}
	svg, err := QRSVG(u)
	if err != nil || !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, `<path fill="#000" d="M`) {
		t.Fatalf("qr: %v %.80s", err, svg)
	}
	if GroupSecret("ABCDEFGHIJ") != "ABCD EFGH IJ" {
		t.Fatal(GroupSecret("ABCDEFGHIJ"))
	}
}
