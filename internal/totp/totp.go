// Package totp implements time-based one-time passwords (RFC 6238, SHA-1, 6 digits, 30 s) as used
// by authenticator apps, plus recovery codes and the QR code for setting up an app.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"rsc.io/qr"
)

const (
	// Period is the length of one time step.
	Period = 30
	// Digits is the length of a code.
	Digits = 6
	// Skew is the number of time steps accepted before and after the current one (clock drift).
	Skew = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a random 160-bit secret in Base32 (without padding).
func NewSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b32.EncodeToString(b)
}

// Step returns the time step of t.
func Step(t time.Time) int64 { return t.Unix() / Period }

// CodeAt returns the code of the secret for a time step.
func CodeAt(secret string, step int64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil {
		return "", fmt.Errorf("ungültiger Schlüssel: %w", err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", Digits, v%1_000_000), nil
}

// NormalizeCode strips spaces and dashes an app or the user may add ("123 456").
func NormalizeCode(code string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, strings.TrimSpace(code))
}

// Verify checks code against the secret at time now. It returns the matched time step; a step not
// after lastStep is rejected so that a code cannot be used twice.
func Verify(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	code = NormalizeCode(code)
	if len(code) != Digits {
		return 0, false
	}
	cur := Step(now)
	for d := -Skew; d <= Skew; d++ {
		st := cur + int64(d)
		if st <= lastStep {
			continue
		}
		want, err := CodeAt(secret, st)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return st, true
		}
	}
	return 0, false
}

// URI is the otpauth link an authenticator app reads from the QR code.
func URI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {fmt.Sprint(Digits)}, "period": {fmt.Sprint(Period)}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// GroupSecret formats the secret in blocks of four for typing it in by hand.
func GroupSecret(secret string) string {
	var b strings.Builder
	for i, r := range secret {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// QRSVG renders text as a QR code (black on white, with quiet zone) as SVG.
func QRSVG(text string) (string, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	const quiet = 4
	n := c.Size + 2*quiet
	var p strings.Builder
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if !c.Black(x, y) {
				continue
			}
			// Join horizontal runs into one rectangle.
			run := 1
			for x+run < c.Size && c.Black(x+run, y) {
				run++
			}
			fmt.Fprintf(&p, "M%d %dh%dv1h-%dz", x+quiet, y+quiet, run, run)
			x += run - 1
		}
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="QR-Code"><rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="%s"/></svg>`,
		n, n, n, n, p.String()), nil
}

// Recovery code alphabet without look-alikes (0/O, 1/I/L).
const recoveryChars = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// NewRecoveryCodes returns n codes like "K7QX-M2PA-9RTW" and their hashes for storing.
func NewRecoveryCodes(n int) (codes, hashes []string) {
	for i := 0; i < n; i++ {
		b := make([]byte, 12)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		var s strings.Builder
		for j, v := range b {
			if j > 0 && j%4 == 0 {
				s.WriteByte('-')
			}
			s.WriteByte(recoveryChars[int(v)%len(recoveryChars)])
		}
		codes = append(codes, s.String())
		hashes = append(hashes, HashRecovery(s.String()))
	}
	return codes, hashes
}

// HashRecovery hashes a recovery code (case, spaces and dashes do not matter).
func HashRecovery(code string) string {
	h := sha256.Sum256([]byte(strings.ToUpper(NormalizeCode(code))))
	return hex.EncodeToString(h[:])
}

// UseRecovery looks code up in hashes and returns the remaining hashes when it matches.
func UseRecovery(hashes []string, code string) ([]string, bool) {
	if len(NormalizeCode(code)) < 12 {
		return hashes, false
	}
	h := HashRecovery(code)
	for i, v := range hashes {
		if subtle.ConstantTimeCompare([]byte(v), []byte(h)) == 1 {
			rest := append(append([]string{}, hashes[:i]...), hashes[i+1:]...)
			return rest, true
		}
	}
	return hashes, false
}
