package core

import (
	"strings"
	"testing"
)

// The network card of a test guest: cut, or on the plan's bridge and VLAN – but never on the
// network of the original, and never on a bridge the node lacks.
func TestTestNet(t *testing.T) {
	bridges := []string{"vmbr0", "vmbr1"}
	ok := map[string]bool{"vmbr0": true, "vmbr1": true}
	cut := RestoreTestPlan{}
	conn := RestoreTestPlan{Network: "bridge", Bridge: "vmbr1", VLAN: 20, Node: "pve1"}
	cases := []struct {
		plan       RestoreTestPlan
		in         string
		want, note string
	}{
		{cut, "virtio=BC:24:11:00:01:01,bridge=vmbr0,firewall=1", "virtio=BC:24:11:00:01:01,bridge=vmbr0,firewall=1,link_down=1", ""},
		{cut, "virtio=AA,bridge=vmbr9", "virtio=AA,bridge=vmbr0,link_down=1", "bridge=vmbr9 fehlt"},
		{conn, "virtio=AA,bridge=vmbr0,tag=5,trunks=5;6,link_down=1,firewall=1", "virtio=AA,firewall=1,bridge=vmbr1,tag=20", ""},
		{conn, "name=eth0,bridge=vmbr0,hwaddr=AA,ip=10.0.0.5/24,type=veth", "name=eth0,hwaddr=AA,ip=10.0.0.5/24,type=veth,bridge=vmbr1,tag=20", ""},
		{conn, "virtio=AA,bridge=vmbr1,tag=20", "virtio=AA,bridge=vmbr1,tag=20,link_down=1", "Netz des Originals"},
		{RestoreTestPlan{Network: "bridge", Bridge: "vmbr7", Node: "pve1"}, "virtio=AA,bridge=vmbr0", "virtio=AA,bridge=vmbr0,link_down=1", "vmbr7 gibt es auf pve1 nicht"},
		{RestoreTestPlan{Network: "bridge", Bridge: "vmbr0"}, "virtio=AA,bridge=vmbr0,tag=30", "virtio=AA,bridge=vmbr0", ""},
	}
	for i, c := range cases {
		got, note := testNet("net0", c.in, c.plan, bridges, ok)
		if got != c.want || (c.note == "") != (note == "") || !strings.Contains(note, c.note) {
			t.Errorf("%d: %q / %q, want %q / %q", i, got, note, c.want, c.note)
		}
	}
}

func TestKeptName(t *testing.T) {
	for in, want := range map[string]string{"dc01": "test-dc01", "Web Server (alt)": "test-Web-Server-alt", "": "test-100", "äöü": "test-100"} {
		if got := keptName(in, "100"); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
