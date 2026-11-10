package fake

import (
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
	name := map[int]string{100: "dc01", 101: "fileserver", 102: "nextcloud", 110: "pihole", 111: "homeassistant", 112: "test-win11", 250: "alt-fileserver"}[orig]
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
		c["hostpci0"] = "0000:03:00.0,pcie=1"
		c["ide2"] = "local:iso/debian-13.iso,media=cdrom"
		c["usb0"] = "host=2-1"
	}
	return c
}

// guestOp answers config, status and agent calls of a guest.
func (p *PVE) guestOp(w http.ResponseWriter, m string, seg []string, f url.Values) {
	id := atoi(seg[3])
	g := p.guest(id)
	op := strings.Join(seg[4:], "/")
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
