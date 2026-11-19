package core_test

import (
	"context"
	"strings"
	"testing"

	"pk95proxbackup/internal/fake"
)

// The office PBS runs as VM 130; its datastore "offsite" is ext4 on the virtual disk scsi1.
// Enlarging it grows the disk in Proxmox VE, then partition and filesystem on the PBS.
func TestGrowDatastore(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	const gib = int64(1) << 30

	if p, err := c.PlanGrow(ctx, "buero", "offsite"); err != nil || !strings.Contains(p.Problem, "SSH-Zugang") {
		t.Fatalf("without SSH: %+v %v", p, err)
	}
	if _, err := c.EnableSSH(ctx, "buero", "10.20.0.5", 22, "root"); err != nil {
		t.Fatal(err)
	}
	p, err := c.PlanGrow(ctx, "buero", "offsite")
	if err != nil || p.Problem != "" {
		t.Fatalf("plan: %+v %v", p, err)
	}
	if p.VMID != 130 || p.Drive != "scsi1" || p.Storage != "tank" || p.Size != 3500*gib || p.FS != "ext4" || p.Partition != "1" || p.Free != 600*gib || p.Pending {
		t.Fatalf("plan: %+v", p)
	}
	// What cannot be enlarged from here, and why.
	if p, err := c.PlanGrow(ctx, "local", "usb-a"); err != nil || !strings.Contains(p.Problem, "Wechsel") {
		t.Fatalf("rotation disk: %+v %v", p, err)
	}
	if p, err := c.PlanGrow(ctx, "local", "backup"); err != nil || !strings.Contains(p.Problem, "/dev/mapper/pve-root") {
		t.Fatalf("datastore on LVM: %+v %v", p, err)
	}
	if _, err := c.GrowDatastore(ctx, "buero", "offsite", 3000, "admin"); err == nil || !strings.Contains(err.Error(), "über den bisherigen") {
		t.Fatalf("smaller: %v", err)
	}
	if _, err := c.GrowDatastore(ctx, "buero", "offsite", 5000, "admin"); err == nil || !strings.Contains(err.Error(), "nur 600") {
		t.Fatalf("more than the storage has: %v", err)
	}

	p, err = c.GrowDatastore(ctx, "buero", "offsite", 4000, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if p.Size != 4000*gib || p.FSSize < 3900*gib || fake.Current.PVE.DriveSize(130, "scsi1") != 4000*gib {
		t.Fatalf("after growing: %+v, disk %d", p, fake.Current.PVE.DriveSize(130, "scsi1"))
	}
	if !strings.Contains(eventTitles(t, c), "Datastore offsite vergrößert") {
		t.Fatalf("not reported:\n%s", eventTitles(t, c))
	}
	pbs, _, _ := c.PBS("buero")
	us, _ := pbs.Usage(ctx)
	for _, u := range us {
		if u.Store == "offsite" && u.Total < 3900*gib {
			t.Fatalf("PBS still reports %d", u.Total)
		}
	}
	// The disk was enlarged by hand in Proxmox VE: the same size again catches up.
	fake.Current.PVE.SetVMDisks(130, map[string]string{"scsi1": "tank:vm-130-disk-1,size=4300G"})
	fake.Current.Offsite.SetDiskSize("sdb", 4300*gib)
	if p, _ = c.PlanGrow(ctx, "buero", "offsite"); !p.Pending {
		t.Fatalf("pending growth not seen: %+v", p)
	}
	if p, err = c.GrowDatastore(ctx, "buero", "offsite", 4300, "admin"); err != nil || p.FSSize < 4200*gib {
		t.Fatalf("catch up: %+v %v", p, err)
	}
}
