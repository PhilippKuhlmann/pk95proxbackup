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
