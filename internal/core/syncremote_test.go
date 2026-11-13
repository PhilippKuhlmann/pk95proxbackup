package core_test

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"

	"pk95proxbackup/internal/fake"
)

func TestCheckRemote(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	local, _ := c.Server("local")
	// The office server copies from this appliance. A wrong address: no answer.
	res := c.CheckRemote(ctx, "buero", "local", "backup", "127.0.0.1:1")
	if res.OK || !strings.Contains(res.Hint, "antwortet kein Backup-Server") {
		t.Fatalf("wrong address: %+v", res)
	}
	// The right one (in the simulation the appliance listens on localhost).
	addr := net.JoinHostPort(local.Host, strconv.Itoa(local.Port))
	res = c.CheckRemote(ctx, "buero", "local", "backup", addr)
	if !res.OK || len(res.Stores) == 0 {
		t.Fatalf("right address: %+v", res)
	}
	// The office server reads with a token of its own that may only read.
	roles := fake.ACLRoles("/datastore/backup", "pk95-sync@pbs!to-buero")
	if len(roles) == 0 || roles[0] != "DatastoreReader" {
		t.Fatalf("sync token roles: %v", roles)
	}
	// The address is kept for the next job without an address.
	srv, _ := c.Server("buero")
	pbs := c.PBSClient(srv)
	name, err := c.RemoteFor(ctx, pbs, srv, "local", "nas-backup", "")
	if err != nil {
		t.Fatal(err)
	}
	rs, _ := pbs.Remotes(ctx)
	for _, r := range rs {
		if r.Name == name && (r.Host != local.Host || r.Port != local.Port || r.AuthID != "pk95-sync@pbs!to-buero") {
			t.Fatalf("remote: %+v", r)
		}
	}
	if roles := fake.ACLRoles("/datastore/nas-backup", "pk95-sync@pbs!to-buero"); len(roles) == 0 {
		t.Fatal("second datastore not granted")
	}
}
