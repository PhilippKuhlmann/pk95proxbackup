package shares

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	ok := Share{ID: "nas_1", Name: "NAS", Type: "nfs", Source: "192.168.1.5:/volume1/pbs"}
	if errs := ok.Validate(); len(errs) != 0 {
		t.Fatalf("valid nfs share rejected: %v", errs)
	}
	cifs := Share{ID: "smb", Name: "SMB", Type: "cifs", Source: `\\nas\backup`, Username: "u", Password: "p"}
	if errs := cifs.Validate(); len(errs) != 0 || cifs.Source != "//nas/backup" {
		t.Fatalf("windows path not normalized: %v %q", errs, cifs.Source)
	}
	for _, bad := range []Share{
		{ID: "Bad-Id", Name: "x", Type: "nfs", Source: "h:/x"},
		{ID: "ok", Name: "x", Type: "nfs", Source: "no-colon"},
		{ID: "ok", Name: "x", Type: "cifs", Source: "//h"},
		{ID: "ok", Name: "x", Type: "nfs", Source: "h:/x", Options: "a b"},
		{ID: "ok", Name: "x", Type: "cifs", Source: "//h/s", Password: "a\nb"},
		{ID: "ok", Name: "x", Type: "zfs", Source: "h:/x"},
	} {
		if errs := bad.Validate(); len(errs) == 0 {
			t.Errorf("invalid share accepted: %+v", bad)
		}
	}
}

func TestUnit(t *testing.T) {
	s := Share{ID: "nas", Name: "NAS", Type: "cifs", Source: "//nas/pbs", Username: "u", Password: "p", Options: "vers=3.1.1"}
	u := s.Unit()
	for _, want := range []string{"Where=/mnt/pk95/nas", "What=//nas/pbs", "Type=cifs", "credentials=/etc/pk95proxbackup/cifs-nas.cred", "uid=34", "vers=3.1.1", "nofail"} {
		if !strings.Contains(u, want) {
			t.Errorf("unit misses %q:\n%s", want, u)
		}
	}
	if strings.Contains(u, "vers=3.0") {
		t.Error("default vers must not be added when the user set one")
	}
	if strings.Contains(u, "password") {
		t.Error("password must only be in the credentials file")
	}
	if s.UnitName() != "mnt-pk95-nas.mount" {
		t.Errorf("unit name %q", s.UnitName())
	}
}
