package core

import (
	"errors"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/enckey"
)

// EncKey is an encryption key for backups (the key file Proxmox VE uses for a PBS storage).
// The appliance keeps every key it hands to Proxmox VE: without it, encrypted backups cannot be
// restored once the cluster is gone. Its own daily backup (encrypted) carries the keys along.
type EncKey struct {
	Fingerprint string `json:"fingerprint"`
	Name        string `json:"name"`
	Key         string `json:"key"` // JSON key file – secret
	Created     int64  `json:"created"`
	Saved       int64  `json:"saved,omitempty"` // when the user confirmed a copy outside
}

const encKeysSetting = "enckeys"

var encKeyMu sync.Mutex

// EncKeys returns all keys (with the secret).
func (c *Core) EncKeys() []EncKey {
	var out []EncKey
	_ = c.St.GetSetting(encKeysSetting, &out)
	return out
}

// EncKey finds a key by fingerprint or by its ID (fingerprint without colons).
func (c *Core) EncKey(fpOrID string) (EncKey, bool) {
	id := enckey.ID(fpOrID)
	for _, k := range c.EncKeys() {
		if enckey.ID(k.Fingerprint) == id {
			return k, true
		}
	}
	return EncKey{}, false
}

func (c *Core) updateEncKeys(fn func([]EncKey) ([]EncKey, error)) error {
	encKeyMu.Lock()
	defer encKeyMu.Unlock()
	keys, err := fn(c.EncKeys())
	if err != nil {
		return err
	}
	return c.St.SetSetting(encKeysSetting, keys)
}

// AddEncKey stores a key: a new one when keyText is empty, otherwise the given key file
// (JSON or paper form). A key that is there already keeps its entry. existed reports that.
func (c *Core) AddEncKey(name, keyText string) (k EncKey, existed bool, err error) {
	var js, fp string
	if strings.TrimSpace(keyText) == "" {
		js, fp = enckey.New()
	} else if js, fp, err = enckey.Parse(keyText); err != nil {
		return EncKey{}, false, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Schlüssel " + enckey.Short(fp)[:11]
	}
	err = c.updateEncKeys(func(keys []EncKey) ([]EncKey, error) {
		for _, old := range keys {
			if old.Fingerprint == fp {
				k, existed = old, true
				return keys, nil
			}
		}
		k = EncKey{Fingerprint: fp, Name: name, Key: js, Created: time.Now().Unix()}
		if keyText != "" {
			k.Saved = k.Created // imported: there is a copy outside already
		}
		return append(keys, k), nil
	})
	return k, existed, err
}

// SetEncKey renames a key and/or marks it as saved outside.
func (c *Core) SetEncKey(id, name string, saved bool) error {
	return c.updateEncKeys(func(keys []EncKey) ([]EncKey, error) {
		for i := range keys {
			if enckey.ID(keys[i].Fingerprint) != enckey.ID(id) {
				continue
			}
			if n := strings.TrimSpace(name); n != "" {
				keys[i].Name = n
			}
			if saved && keys[i].Saved == 0 {
				keys[i].Saved = time.Now().Unix()
			}
			return keys, nil
		}
		return nil, errors.New("Schlüssel nicht gefunden.")
	})
}

// DeleteEncKey removes a key from the appliance.
func (c *Core) DeleteEncKey(id string) error {
	return c.updateEncKeys(func(keys []EncKey) ([]EncKey, error) {
		for i := range keys {
			if enckey.ID(keys[i].Fingerprint) == enckey.ID(id) {
				return append(keys[:i], keys[i+1:]...), nil
			}
		}
		return nil, errors.New("Schlüssel nicht gefunden.")
	})
}
