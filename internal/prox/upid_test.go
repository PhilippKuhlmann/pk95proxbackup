package prox

import "testing"

// Proxmox VE and the Backup Server write task IDs differently: the Backup Server has a task
// counter before the start time.
func TestUPIDStart(t *testing.T) {
	for upid, want := range map[string]int64{
		"UPID:pbs01:0000027E:000001B3:0000002E:6B02A1D7:reader:local\\x3avm-101-6ABFFF8B:root@pam:":              0x6B02A1D7,
		"UPID:pbs01:00000278:000001B9:0000001E:6B004456:syncjob:\\x2d\\x3alocal\\x3ausb\\x2dusb2:root@pam!pk95:": 0x6B004456,
		"UPID:pve1:0012ABCD:01234567:6740ABCD:vzdump::root@pam:":                                                 0x6740ABCD,
		"UPID:pve1:0012ABCD:01234567:6740ABCD:qmrestore:990001:root@pam:":                                        0x6740ABCD,
		"UPID:x": 0, "": 0,
	} {
		if got := UPIDStart(upid); got != want {
			t.Errorf("%s: %X, want %X", upid, got, want)
		}
	}
}
