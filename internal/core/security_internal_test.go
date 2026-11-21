package core

import (
	"path/filepath"
	"strings"
	"testing"
)

// The test guest keeps only allowed options; a disk of another guest stops the test.
func TestIsolateAllowlist(t *testing.T) {
	cfg := map[string]any{
		"name": "web", "memory": 4096, "cores": 2, "digest": "abc", "boot": "order=scsi0",
		"scsi0":      "local-lvm:vm-990001-disk-0,size=32G",
		"efidisk0":   "local-lvm:vm-990001-disk-1,size=4M",
		"scsi1":      "/dev/disk/by-id/ata-WDC,size=1T",
		"ide2":       "local:iso/debian.iso,media=cdrom",
		"args":       "-device ivshmem-plain,memdev=x",
		"virtiofs0":  "share,cache=auto",
		"ivshmem":    "size=32",
		"hookscript": "local:snippets/hook.sh",
		"audio0":     "device=ich9-intel-hda,driver=spice",
		"net0":       "virtio=BC:24:11:00:00:01,bridge=vmbr0",
		"serial0":    "socket",
		"serial1":    "/dev/ttyS0",
	}
	p, notes, err := isolate(cfg, "qemu", "101", 990001, RestoreTestPlan{}, []string{"vmbr0"})
	if err != nil {
		t.Fatal(err)
	}
	del := "," + p["delete"].(string) + ","
	for _, k := range []string{"args", "virtiofs0", "ivshmem", "hookscript", "audio0", "scsi1", "serial1"} {
		if !strings.Contains(del, ","+k+",") {
			t.Errorf("%s not removed: %s", k, del)
		}
	}
	for _, k := range []string{"scsi0", "efidisk0", "digest", "name", "memory", "serial0", "boot"} {
		if strings.Contains(del, ","+k+",") {
			t.Errorf("%s removed: %s", k, del)
		}
	}
	if p["ide2"] != "none,media=cdrom" || !strings.Contains(p["net0"].(string), "link_down=1") {
		t.Fatalf("params: %v", p)
	}
	if !strings.Contains(strings.Join(notes, ";"), "scsi1 (Pfad auf dem Host) entfernt") {
		t.Fatalf("notes: %v", notes)
	}
	// a volume of the production guest: never start
	cfg["scsi2"] = "local-lvm:vm-101-disk-0,size=32G"
	if _, _, err := isolate(cfg, "qemu", "101", 990001, RestoreTestPlan{}, nil); err == nil {
		t.Fatal("a disk of another guest must stop the test")
	}
	// raw lxc keys cannot be removed through the API
	ct := map[string]any{"hostname": "ct", "rootfs": "local-lvm:subvol-990002-disk-0,size=8G", "lxc": [][]string{{"lxc.mount.entry", "/ x none bind"}}}
	if _, _, err := isolate(ct, "lxc", "110", 990002, RestoreTestPlan{}, nil); err == nil {
		t.Fatal("raw lxc keys must stop the test")
	}
	delete(ct, "lxc")
	ct["mp0"] = "/srv/daten,mp=/daten"
	ct["dev0"] = "/dev/ttyUSB0"
	p, _, err = isolate(ct, "lxc", "110", 990002, RestoreTestPlan{}, nil)
	if err != nil || !strings.Contains(p["delete"].(string), "mp0") || !strings.Contains(p["delete"].(string), "dev0") {
		t.Fatalf("lxc: %v %v", p, err)
	}
}

// With a cloned SMBIOS UUID the running VM with this machine's network card wins.
func TestNarrowSelf(t *testing.T) {
	vms := []SelfVM{
		{VMID: 120, running: true, Config: map[string]any{"net0": "virtio=BC:24:11:AA:AA:AA,bridge=vmbr0"}},
		{VMID: 121, running: true, Config: map[string]any{"net0": "virtio=BC:24:11:BB:BB:BB,bridge=vmbr0"}},
		{VMID: 122, running: false, Config: map[string]any{"net0": "virtio=BC:24:11:AA:AA:AA,bridge=vmbr0"}},
	}
	got := narrowSelf(vms, map[string]bool{"bc:24:11:aa:aa:aa": true})
	if len(got) != 1 || got[0].VMID != 120 {
		t.Fatalf("narrowed to %+v", got)
	}
	if got := narrowSelf(vms, map[string]bool{"bc:24:11:cc:cc:cc": true}); len(got) != 2 {
		t.Fatalf("no network card matches: must stay ambiguous, got %+v", got)
	}
}

// A screenshot path never leaves the restore-test directory.
func TestScreenshotPathStaysInside(t *testing.T) {
	c := &Core{DataDir: "/data"}
	for _, x := range [][2]string{{"..", "ssh_ed25519"}, {".", "key.pem"}, {"run", ".."}, {"../..", "x"}} {
		p := c.ScreenshotPath(x[0], x[1])
		if rel, err := filepath.Rel("/data/restoretests", p); err != nil || strings.HasPrefix(rel, "..") || strings.Count(rel, string(filepath.Separator)) != 1 {
			t.Errorf("%v → %s", x, p)
		}
	}
}
