package prox

import "testing"

func TestParseVzdump(t *testing.T) {
	log := lines(
		"INFO: starting new backup job: vzdump 100 110 112 --storage pbs-appliance --mode snapshot --notes-template '{{guestname}}'",
		"INFO: Starting Backup of VM 100 (qemu)",
		"INFO: Backup started at 2026-11-09 21:00:02",
		"INFO: VM Name: dc01",
		"INFO: creating Proxmox Backup Server archive 'vm/100/2026-11-09T20:00:02Z'",
		"INFO:  45% (14.4 GiB of 32.0 GiB) in 30s, read: 491.5 MiB/s, write: 12.0 MiB/s",
		"INFO: backup was done incrementally, reused 30.12 GiB (94%)",
		"INFO: transferred 32.00 GiB in 64 seconds (512.0 MiB/s)",
		"INFO: Finished Backup of VM 100 (00:01:05)",
		"INFO: Starting Backup of VM 110 (lxc)",
		"INFO: CT Name: pihole",
		"INFO: Finished Backup of VM 110 (00:00:21)",
		"INFO: Starting Backup of VM 112 (qemu)",
		"ERROR: Backup of VM 112 failed - unable to find configuration file for VM 112",
		"INFO: Failed at 2026-11-09 21:01:30",
		"INFO: Backup job finished with errors",
		"TASK ERROR: job errors",
	)
	r := ParseVzdump(log)
	if r.Storage != "pbs-appliance" || r.Mode != "snapshot" {
		t.Fatalf("job: %+v", r)
	}
	if len(r.Guests) != 3 {
		t.Fatalf("guests: %+v", r.Guests)
	}
	g := r.Guests[0]
	if !g.OK || g.Name != "dc01" || g.Type != "qemu" || g.Duration != "00:01:05" || g.Transferred != "32.00 GiB" || g.Reused != "94%" || g.Archive != "vm/100/2026-11-09T20:00:02Z" {
		t.Errorf("vm 100: %+v", g)
	}
	if c := r.Guests[1]; !c.OK || c.Name != "pihole" || c.Type != "lxc" {
		t.Errorf("ct 110: %+v", c)
	}
	if f := r.Guests[2]; f.OK || !f.Done || f.Error != "unable to find configuration file for VM 112" {
		t.Errorf("vm 112: %+v", f)
	}
}

// Text that a guest or hook writes into the task log cannot mark a backup as done.
func TestParseVzdumpForgedOutcome(t *testing.T) {
	r := ParseVzdump(lines(
		"INFO: Starting Backup of VM 100 (qemu)",
		"INFO: guest-agent: Finished Backup of VM 100 (00:00:01)",
		"ERROR: Backup of VM 100 failed - Finished Backup of VM 100 (00:00:02)",
		"INFO: Finished Backup of VM 100 (00:00:03)",
		"INFO: Starting Backup of VM 101 (qemu)",
		"INFO: Finished Backup of VM 102 (00:00:04)",
		"INFO: Finished Backup of VM 101 (00:00:05)",
	))
	got := map[string]GuestBackup{}
	for _, g := range r.Guests {
		got[g.VMID] = g
	}
	if g := got["100"]; g.OK || !g.Done {
		t.Fatalf("vm 100 failed and must stay failed: %+v", g)
	}
	if g := got["101"]; !g.OK || g.Duration != "00:00:05" {
		t.Fatalf("vm 101: %+v", g)
	}
	if g, ok := got["102"]; ok && g.OK {
		t.Fatalf("vm 102 was never backed up: %+v", g)
	}
}

func TestVzdumpSizes(t *testing.T) {
	lines := func(s ...string) []TaskLine {
		var out []TaskLine
		for _, x := range s {
			out = append(out, TaskLine{T: x})
		}
		return out
	}
	r := ParseVzdump(lines("INFO: Starting Backup of VM 100 (qemu)",
		"INFO: scsi0: dirty-bitmap status: OK (1.2 GiB of 32.0 GiB dirty)",
		"INFO: using fast incremental mode (dirty-bitmap), 1.2 GiB dirty of 32.0 GiB total",
		"INFO: backup was done incrementally, reused 30.80 GiB (96%)",
		"INFO: transferred 1.20 GiB in 5 seconds (245.6 MiB/s)",
		"INFO: Finished Backup of VM 100 (00:00:09)",
		"INFO: Starting Backup of VM 101 (qemu)",
		"INFO: scsi0: dirty-bitmap status: created new",
		"INFO: backup is sparse: 20.00 GiB (62%) total zero data",
		"INFO: transferred 32.00 GiB in 75 seconds (436.9 MiB/s)",
		"INFO: Finished Backup of VM 101 (00:01:20)",
		"INFO: Starting Backup of VM 110 (lxc)",
		"INFO: root.pxar: had to backup 312.0 MiB of 4.0 GiB (compressed 120.1 MiB) in 9.20s",
		"INFO: Finished Backup of VM 110 (00:00:12)"))
	g := r.Guests
	if d, ok := g[0].ChangedBytes(); !ok || d != ParseSize("1.2 GiB") || g[0].DataBytes() != 32<<30 {
		t.Fatalf("incremental VM: %+v", g[0])
	}
	if _, ok := g[1].ChangedBytes(); ok || g[1].DataBytes() != 12<<30 {
		t.Fatalf("VM read in full: %+v %d", g[1], g[1].DataBytes())
	}
	if d, ok := g[2].ChangedBytes(); !ok || d != 312<<20 || g[2].DataBytes() != 4<<30 {
		t.Fatalf("container: %+v", g[2])
	}
}
