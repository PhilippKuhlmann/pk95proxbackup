package fake

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var archiveRe = regexp.MustCompile(`backup/(vm|ct)/(\d+)/`)

// backupConfig is the configuration a restore of a demo backup produces. VM 101 has a PCI
// card passed through and an ISO inserted, VMs with even IDs run the guest agent.
func backupConfig(archive, kind string, id int) map[string]string {
	orig := 0
	if m := archiveRe.FindStringSubmatch(archive); m != nil {
		orig = atoi(m[2])
	}
	name := map[int]string{100: "dc01", 101: "fileserver", 102: "nextcloud", 110: "pihole", 111: "homeassistant", 112: "test-win11", 250: "alt-fileserver", 200: "erp", 201: "mailserver", 210: "webshop"}[orig]
	if name == "" {
		name = "gast-" + strconv.Itoa(orig)
	}
	mac := "BC:24:11:00:" + strconv.Itoa(10+id%80) + ":01"
	if kind == "lxc" {
		return map[string]string{"hostname": name, "cores": "2", "memory": "1024", "onboot": "1",
			"rootfs": "local-lvm:vm-" + strconv.Itoa(id) + "-disk-0,size=8G",
			"mp0":    "/srv/daten,mp=/daten", "net0": "name=eth0,bridge=vmbr0,hwaddr=" + mac + ",ip=192.168.10.53/24,type=veth"}
	}
	c := map[string]string{"name": name, "cores": "8", "sockets": "2", "memory": "16384", "balloon": "8192", "onboot": "1",
		"scsi0": "local-lvm:vm-" + strconv.Itoa(id) + "-disk-0,size=64G", "net0": "virtio=" + mac + ",bridge=vmbr9,firewall=1",
		"ostype": "l26", "startup": "order=1"}
	if orig%2 == 0 {
		c["agent"] = "1"
	}
	if orig == 101 {
		c["scsi1"] = "local-lvm:vm-" + strconv.Itoa(id) + "-disk-1,size=500G"
		c["hostpci0"] = "0000:03:00.0,pcie=1"
		c["ide2"] = "local:iso/debian-13.iso,media=cdrom"
		c["usb0"] = "host=2-1"
	}
	return c
}

// DemoPorts are the TCP ports the demo VMs listen on, by original ID (a test guest is named
// restoretest-<ID>); 112 is the Windows VM. Containers listen on 22, 53 and 80.
var DemoPorts = map[string][]int{
	"100": {22, 53, 88, 389, 636}, "102": {22, 80, 443, 3306}, "112": {135, 445, 3389},
}

// procNetTCP writes /proc/net/tcp with these ports listening (state 0A) and one connection.
func procNetTCP(ports []int) string {
	out := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	for i, p := range ports {
		out += fmt.Sprintf("%4d: 00000000:%04X 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 %d 1 0000000000000000 100 0 0 10 0\n", i, p, 10000+i)
	}
	return out + "  99: 0100007F:9C40 0100007F:0016 01 00000000:00000000 00:00000000 00000000     0        0 9999 1 0000000000000000 20 4 30 10 -1\n"
}

// guestOp answers config, status and agent calls of a guest.
func (p *PVE) guestOp(w http.ResponseWriter, m string, seg []string, f url.Values) {
	id := atoi(seg[3])
	g := p.guest(id)
	op := strings.Join(seg[4:], "/")
	if op == "resize" && seg[2] == "qemu" && m == "PUT" {
		p.resizeDisk(w, seg[1], id, f)
		return
	}
	if op == "move_disk" && seg[2] == "qemu" && m == "POST" {
		p.reassignDisk(w, seg[1], id, f)
		return
	}
	if op == "config" && seg[2] == "qemu" && p.cfg[id] == nil {
		// guests that were never restored: the USB passthrough helper only needs smbios/usb
		p.legacyConfig(w, m, id, f)
		return
	}
	if g == nil {
		apiErr(w, 500, "Configuration file '"+seg[2]+"/"+seg[3]+".conf' does not exist")
		return
	}
	cfg := p.cfg[id]
	if cfg == nil {
		cfg = map[string]string{"name": g.Name}
		p.cfg[id] = cfg
	}
	switch {
	case op == "config" && m == "GET":
		out := map[string]any{}
		keys := make([]string, 0, len(cfg))
		for k := range cfg {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out[k] = cfg[k]
		}
		data(w, out)
	case op == "config" && m == "PUT":
		if p.realUSB(w, f) {
			return
		}
		for _, d := range strings.Split(f.Get("delete"), ",") {
			delete(cfg, d)
		}
		for k := range f {
			if k == "delete" || k == "digest" {
				continue
			}
			cfg[k] = f.Get(k)
		}
		if n := cfg["name"] + cfg["hostname"]; n != "" {
			g.Name = n
		}
		g.Tags = cfg["tags"]
		data(w, nil)
	case op == "status/current":
		st := map[string]any{"status": g.Status, "qmpstatus": g.Status}
		if cfg["agent"] == "1" {
			st["agent"] = 1
		}
		data(w, st)
	case op == "status/start":
		if strings.Contains(cfg["net0"], "bridge=vmbr9") {
			apiErr(w, 500, "bridge 'vmbr9' does not exist")
			return
		}
		if _, ok := cfg["hostpci0"]; ok {
			apiErr(w, 500, "PCI device 0000:03:00.0 is in use by VM 101")
			return
		}
		g.Status = "running"
		p.started[id] = time.Now()
		data(w, p.tasks.start(seg[1], "qmstart", seg[3], time.Second, nil, nil))
	case op == "status/stop" || op == "status/shutdown":
		g.Status = "stopped"
		data(w, p.tasks.start(seg[1], "qmstop", seg[3], time.Second, nil, nil))
	case op == "agent/ping":
		if g.Status != "running" || cfg["agent"] != "1" || time.Since(p.started[id]) < 2*time.Second {
			apiErr(w, 500, "QEMU guest agent is not running")
			return
		}
		data(w, map[string]any{})
	case op == "agent/get-osinfo":
		data(w, map[string]any{"result": map[string]string{"pretty-name": "Debian GNU/Linux 13 (trixie)", "name": "Debian GNU/Linux"}})
	case op == "agent/get-host-name":
		data(w, map[string]any{"result": map[string]string{"host-name": cfg["name"]}})
	case op == "agent/file-read":
		// Linux guests: their listening ports; the Windows VM has no /proc
		orig := strings.TrimPrefix(cfg["name"], "restoretest-")
		if orig == "112" || f.Get("file") != "/proc/net/tcp" && f.Get("file") != "/proc/net/tcp6" {
			apiErr(w, 500, "guest-file-open failed: No such file or directory")
			return
		}
		if f.Get("file") == "/proc/net/tcp6" {
			data(w, map[string]any{"content": procNetTCP(nil)})
			return
		}
		data(w, map[string]any{"content": procNetTCP(DemoPorts[orig])})
	case op == "agent/exec":
		data(w, map[string]any{"pid": 4711})
	case op == "agent/exec-status":
		out := "\r\nAktive Verbindungen\r\n\r\n  Proto  Lokale Adresse         Remoteadresse          Status\r\n"
		for _, port := range DemoPorts[strings.TrimPrefix(cfg["name"], "restoretest-")] {
			out += fmt.Sprintf("  TCP    0.0.0.0:%d            0.0.0.0:0              LISTENING\r\n", port)
		}
		data(w, map[string]any{"exited": 1, "exitcode": 0, "out-data": out})
	case op == "vncproxy":
		apiErr(w, 501, "VNC ist im Demo-Modus nicht verfügbar")
	default:
		apiErr(w, 404, "not implemented (demo): "+m+" "+op)
	}
}

func (p *PVE) legacyConfig(w http.ResponseWriter, m string, id int, f url.Values) {
	cfg := p.usbCfg[id]
	if cfg == nil {
		cfg = map[string]string{}
		p.usbCfg[id] = cfg
	}
	if m == "PUT" {
		if p.realUSB(w, f) {
			return
		}
		for _, d := range strings.Split(f.Get("delete"), ",") {
			delete(cfg, d)
		}
		for k := range f {
			if strings.HasPrefix(k, "usb") {
				cfg[k] = f.Get(k)
			}
		}
		data(w, nil)
		return
	}
	out := map[string]any{"name": "vm", "memory": 4096}
	if u, ok := p.smbios[id]; ok {
		out["smbios1"] = "uuid=" + u
	}
	for k, v := range cfg {
		out[k] = v
	}
	data(w, out)
}

// reassignDisk answers POST …/qemu/{vmid}/move_disk with target-vmid: the disk of a stopped VM
// moves to another VM on the same node (as Proxmox VE 7.2+ does).
func (p *PVE) reassignDisk(w http.ResponseWriter, node string, id int, f url.Values) {
	g, t := p.guest(id), p.guest(atoi(f.Get("target-vmid")))
	disk, key := f.Get("disk"), f.Get("target-disk")
	cfg := p.cfg[id]
	if g == nil || cfg == nil {
		apiErr(w, 500, "Configuration file 'qemu-server/"+strconv.Itoa(id)+".conf' does not exist")
		return
	}
	val, ok := cfg[disk]
	switch {
	case !ok:
		apiErr(w, 500, "disk '"+disk+"' does not exist")
		return
	case g.Status == "running":
		apiErr(w, 500, "Cannot move disk to another VM while the source VM is running - detach first")
		return
	case t == nil || t.Node != node:
		apiErr(w, 500, "target VM is not on node "+node)
		return
	}
	tcfg := p.cfg[t.VMID]
	if tcfg == nil {
		if p.usbCfg[t.VMID] == nil {
			p.usbCfg[t.VMID] = map[string]string{}
		}
		tcfg = p.usbCfg[t.VMID]
	}
	if _, used := tcfg[key]; used {
		apiErr(w, 500, "target disk key '"+key+"' is already in use")
		return
	}
	storage, rest, _ := strings.Cut(val, ":")
	_, opts, _ := strings.Cut(rest, ",")
	n := 0
	for k, v := range tcfg {
		if driveKeyRe.MatchString(k) && strings.Contains(v, ":vm-") {
			n++
		}
	}
	tcfg[key] = storage + ":vm-" + strconv.Itoa(t.VMID) + "-disk-" + strconv.Itoa(n) + "," + opts
	delete(cfg, disk)
	data(w, p.tasks.start(node, "qmmove", strconv.Itoa(id), time.Second, []string{"reassign " + disk + " to VM " + strconv.Itoa(t.VMID) + " as " + key}, nil))
}

var driveKeyRe = regexp.MustCompile(`^(scsi|virtio|sata|ide)\d+$`)
