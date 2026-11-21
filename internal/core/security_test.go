package core_test

import (
	"context"
	"testing"

	"pk95proxbackup/internal/fake"
)

// A copy target must never receive the appliance's own login on the source (it may
// administer the source): when the read-only token cannot be set up, saving fails.
func TestSyncRemoteNeverGetsAdminToken(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	fake.PBSNoUserAdmin = true
	t.Cleanup(func() { fake.PBSNoUserAdmin = false })
	local, _ := c.Server("local")
	srv, _ := c.Server("buero")
	pbs := c.PBSClient(srv)
	if _, err := c.RemoteFor(ctx, pbs, srv, "local", "backup", "127.0.0.1:8007"); err == nil {
		t.Fatal("without a read-only token the remote must not be set up")
	}
	rs, _ := pbs.Remotes(ctx)
	for _, r := range rs {
		if r.AuthID == local.AuthID {
			t.Fatalf("the target got the appliance's login: %+v", r)
		}
	}
}

// Headers of a client that is not a trusted proxy never become the address in links.
func TestPublicURLIgnoresUntrustedProxy(t *testing.T) {
	c := newCore(t)
	before := c.PublicURL()
	c.NoteProxy("203.0.113.7", "https", "backup.example.org")
	if got := c.PublicURL(); got != before {
		t.Fatalf("untrusted proxy changed the public url: %q", got)
	}
	for _, bad := range [][2]string{{"javascript", "x.example.org"}, {"https", "evil.example.net/pfad"}, {"https", "a b"}, {"https", `x"y`}} {
		c.NoteProxy("203.0.113.7", bad[0], bad[1])
		if s := c.SeenProxy(); s.URL != "https://backup.example.org" {
			t.Fatalf("%v accepted: %+v", bad, s)
		}
	}
	if err := c.St.SetSetting("access", map[string]any{"trusted_proxies": []string{"203.0.113.7"}}); err != nil {
		t.Fatal(err)
	}
	if got := c.PublicURL(); got != "https://backup.example.org" {
		t.Fatalf("trusted proxy: %q", got)
	}
}
