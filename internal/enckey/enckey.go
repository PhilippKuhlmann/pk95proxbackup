// Package enckey creates and checks the encryption keys of Proxmox Backup Server clients (the
// key file Proxmox VE keeps per PBS storage). A key is 32 random bytes; its fingerprint is
// computed exactly like proxmox-backup-client does, so imported keys can be checked for typos.
package enckey

import (
	"bytes"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// fingerprintInput is hashed with the ID key derived from the key (CryptConfig::fingerprint).
var fingerprintInput = []byte{110, 208, 239, 119, 71, 31, 255, 77, 85, 199, 168, 254, 74, 157, 182, 33,
	97, 64, 127, 19, 76, 114, 93, 223, 48, 153, 45, 37, 236, 69, 237, 38}

const (
	paperBegin = "-----BEGIN PROXMOX BACKUP KEY-----"
	paperEnd   = "-----END PROXMOX BACKUP KEY-----"
)

// File is the JSON key file as written by proxmox-backup-client (kdf none: data is the key).
type File struct {
	KDF         json.RawMessage `json:"kdf"`
	Created     string          `json:"created"`
	Modified    string          `json:"modified"`
	Data        string          `json:"data"`
	Fingerprint string          `json:"fingerprint,omitempty"`
}

// Fingerprint returns the fingerprint of a raw 32-byte key ("aa:bb:…", 32 bytes).
func Fingerprint(key []byte) string {
	id, err := pbkdf2.Key(sha256.New, string(key), []byte("_id_key"), 10, 32)
	if err != nil {
		panic(err)
	}
	h := sha256.New()
	h.Write(fingerprintInput)
	h.Write(id)
	return colons(h.Sum(nil))
}

func colons(b []byte) string {
	s := hex.EncodeToString(b)
	var out strings.Builder
	for i := 0; i < len(s); i += 2 {
		if i > 0 {
			out.WriteByte(':')
		}
		out.WriteString(s[i : i+2])
	}
	return out.String()
}

// New returns a new key file (JSON) and its fingerprint.
func New() (string, string) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	now := time.Now().Format(time.RFC3339)
	f := File{KDF: json.RawMessage("null"), Created: now, Modified: now, Data: base64.StdEncoding.EncodeToString(key), Fingerprint: Fingerprint(key)}
	b, _ := json.Marshal(f)
	return string(b), f.Fingerprint
}

// Parse reads a key file as JSON or as paper key (with BEGIN/END lines), checks it and returns
// it as compact JSON with its fingerprint.
func Parse(text string) (string, string, error) {
	text = strings.TrimSpace(text)
	if i := strings.Index(text, paperBegin); i >= 0 {
		text = text[i+len(paperBegin):]
		if j := strings.Index(text, paperEnd); j >= 0 {
			text = text[:j]
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "", errors.New("Bitte den Schlüssel einfügen.")
	}
	var f File
	if err := json.Unmarshal([]byte(text), &f); err != nil {
		return "", "", errors.New("Das ist kein Schlüssel im Format von Proxmox Backup (JSON mit „data“ und „fingerprint“).")
	}
	if k := strings.TrimSpace(string(f.KDF)); k != "" && k != "null" {
		return "", "", errors.New("Dieser Schlüssel ist mit einem Passwort geschützt. Proxmox VE braucht ihn ohne Passwort: mit „proxmox-backup-client key change-passphrase --kdf none“ umwandeln.")
	}
	key, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(f.Data), ""))
	if err != nil || len(key) != 32 {
		return "", "", errors.New("Der Schlüssel ist unvollständig oder hat einen Tippfehler (Feld „data“).")
	}
	fp := Fingerprint(key)
	if f.Fingerprint != "" && !strings.EqualFold(strings.TrimSpace(f.Fingerprint), fp) {
		return "", "", errors.New("Fingerabdruck und Schlüssel passen nicht zusammen – vermutlich ein Tippfehler im Feld „data“.")
	}
	now := time.Now().Format(time.RFC3339)
	if f.Created == "" {
		f.Created = now
	}
	if f.Modified == "" {
		f.Modified = f.Created
	}
	f.KDF, f.Data, f.Fingerprint = json.RawMessage("null"), base64.StdEncoding.EncodeToString(key), fp
	b, _ := json.Marshal(f)
	return string(b), fp, nil
}

// Paper formats a key file like "proxmox-backup-client key paperkey" (text form).
func Paper(keyJSON string) string {
	var b bytes.Buffer
	if err := json.Indent(&b, []byte(keyJSON), "", "  "); err != nil { // keeps the field order
		return keyJSON
	}
	return paperBegin + "\n" + b.String() + "\n" + paperEnd + "\n"
}

// ID is the fingerprint without colons (for URLs).
func ID(fp string) string { return strings.ReplaceAll(strings.ToLower(fp), ":", "") }

// Short is the first eight bytes of a fingerprint, as Proxmox shows it in lists.
func Short(fp string) string {
	if len(fp) > 23 {
		return fp[:23]
	}
	return fp
}
