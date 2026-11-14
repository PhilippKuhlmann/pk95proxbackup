package core

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"pk95proxbackup/internal/prox"
)

// Which disks of the Proxmox VE nodes matter for the backup: the physical disks under the
// virtual disks of this appliance (where its datastores live). The way down is
// VM disk → storage → LVM volume group / ZFS pool / directory → physical disk.

// SelfVM is the VM this appliance runs in.
type SelfVM struct {
	Cluster     string
	ClusterName string
	Node        string
	VMID        int
	Name        string
	Config      map[string]any
}

var smbiosUUIDRe = regexp.MustCompile(`uuid=([0-9a-fA-F-]{36})`)

// FindSelfVM looks for the VM whose SMBIOS UUID matches this machine.
func (c *Core) FindSelfVM(ctx context.Context) (*SelfVM, error) {
	uuid := ""
	if c.SelfUUID != nil {
		uuid = c.SelfUUID()
	}
	if uuid == "" {
		return nil, errors.New("die SMBIOS-UUID dieser Maschine ist nicht lesbar – läuft die Appliance in einer Proxmox-VM?")
	}
	clusters, _ := c.Clusters()
	if len(clusters) == 0 {
		return nil, errors.New("zuerst unter Verbindungen den Proxmox VE eintragen, auf dem diese VM läuft")
	}
	for _, cl := range clusters {
		pve := c.PVEClient(cl)
		guests, err := pve.Guests(ctx)
		if err != nil {
			continue
		}
		for _, g := range guests {
			if g.Type != "qemu" {
				continue
			}
			cfg, err := pve.VMConfig(ctx, g.Node, g.VMID)
			if err != nil {
				continue
			}
			sm, _ := cfg["smbios1"].(string)
			if m := smbiosUUIDRe.FindStringSubmatch(sm); m != nil && strings.EqualFold(m[1], uuid) {
				return &SelfVM{Cluster: cl.ID, ClusterName: cl.Name, Node: g.Node, VMID: g.VMID, Name: g.Name, Config: cfg}, nil
			}
		}
	}
	return nil, errors.New("diese VM wurde in keinem eingetragenen Proxmox VE gefunden")
}

var (
	driveKeyRe = regexp.MustCompile(`^(scsi|virtio|sata|ide)\d+$`)
	partRe     = regexp.MustCompile(`^(/dev/(?:nvme\d+n\d+|mmcblk\d+))p\d+$|^(/dev/[a-z]+)\d+$`)
	zfsPartRe  = regexp.MustCompile(`-part\d+$`)
)

// parentDisk turns a partition (/dev/sdb3, /dev/nvme0n1p3) into its disk.
func parentDisk(dev string) string {
	if m := partRe.FindStringSubmatch(dev); m != nil {
		if m[1] != "" {
			return m[1]
		}
		return m[2]
	}
	return dev
}

// matchDisk finds the node disk a name from the LVM/ZFS tree or a passthrough path stands for.
func matchDisk(name string, disks []prox.NodeDisk) (prox.NodeDisk, bool) {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, "/dev/") && !strings.HasPrefix(name, "/dev/disk/") {
		dev := parentDisk(name)
		for _, d := range disks {
			if d.DevPath == dev {
				return d, true
			}
		}
	}
	base := zfsPartRe.ReplaceAllString(path.Base(name), "")
	for _, d := range disks {
		if base == path.Base(d.DevPath) || parentDisk("/dev/"+base) == d.DevPath {
			return d, true
		}
	}
	for _, d := range disks {
		if s := strings.TrimSpace(d.Serial); len(s) >= 6 && strings.Contains(name, s) {
			return d, true
		}
	}
	return prox.NodeDisk{}, false
}

// vdisk is a virtual disk a backup server sees ("drive-scsi1" in the serial number).
type vdisk struct {
	key  string // scsi1
	size int64
}

// backupVM is a VM that holds backups: this appliance or a backup server running as VM.
type backupVM struct {
	label   string // "der Appliance-VM", "von PBS01"
	cluster PVECluster
	node    string
	vmid    int
	name    string
	cfg     map[string]any
}

var driveSerialRe = regexp.MustCompile(`^drive-((?:scsi|virtio|sata|ide)\d+)$`)

// driveSize reads "size=1536G" of a drive entry (bytes, 0 = not given; sizeRe: restoretest.go).
func driveSize(val string) int64 {
	m := sizeRe.FindStringSubmatch(val)
	if m == nil {
		return 0
	}
	var f float64
	_, _ = fmt.Sscan(m[1], &f)
	mult := map[string]float64{"": 1, "K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}[m[2]]
	return int64(f * mult)
}

func sameSize(a, b int64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= 1<<30 || d*100 <= a
}

// backupVMs finds the VMs whose disks hold backups: this appliance (by its SMBIOS UUID) and
// every backup server whose disks are virtual (by the drive names and sizes it sees).
func (c *Core) backupVMs(ctx context.Context, trace *[]string) []backupVM {
	var out []backupVM
	self, selfErr := c.FindSelfVM(ctx)
	if self != nil {
		if cl, err := c.Cluster(self.Cluster); err == nil {
			out = append(out, backupVM{label: "der Appliance-VM", cluster: cl, node: self.Node, vmid: self.VMID, name: self.Name, cfg: self.Config})
			*trace = append(*trace, fmt.Sprintf("Diese Appliance läuft als VM %d „%s“ auf %s.", self.VMID, self.Name, self.Node))
		}
	} else if selfErr != nil {
		*trace = append(*trace, "Die VM dieser Appliance wurde nicht gefunden: "+selfErr.Error()+".")
	}
	// Backup servers with virtual disks.
	type want struct {
		srv   PBSServer
		disks []vdisk
	}
	var wants []want
	servers, _ := c.Servers()
	for _, s := range servers {
		if s.Local && self != nil {
			continue // that is this appliance
		}
		disks, err := c.PBSClient(s).Disks(ctx)
		if err != nil {
			continue
		}
		var vd []vdisk
		for _, d := range disks {
			if m := driveSerialRe.FindStringSubmatch(strings.TrimSpace(d.Serial)); m != nil {
				vd = append(vd, vdisk{key: m[1], size: d.Size})
			}
		}
		if len(vd) > 0 {
			wants = append(wants, want{s, vd})
		}
	}
	if len(wants) == 0 {
		return out
	}
	// Look at the configuration of every VM once.
	type cand struct {
		cl  PVECluster
		g   prox.Guest
		cfg map[string]any
	}
	var vms []cand
	clusters, _ := c.Clusters()
	for _, cl := range clusters {
		pve := c.PVEClient(cl)
		guests, err := pve.Guests(ctx)
		if err != nil {
			continue
		}
		for _, g := range guests {
			if g.Type != "qemu" || g.Template != 0 {
				continue
			}
			if cfg, err := pve.VMConfig(ctx, g.Node, g.VMID); err == nil {
				vms = append(vms, cand{cl, g, cfg})
			}
		}
	}
	for _, w := range wants {
		var hits []cand
		for _, v := range vms {
			if self != nil && v.cl.ID == self.Cluster && v.g.VMID == self.VMID {
				continue
			}
			all := true
			for _, d := range w.disks {
				val, ok := v.cfg[d.key].(string)
				if !ok || !sameSize(driveSize(val), d.size) {
					all = false
					break
				}
			}
			if all {
				hits = append(hits, v)
			}
		}
		if len(hits) > 1 { // several VMs with the same disks: the name decides
			var named []cand
			for _, h := range hits {
				if strings.EqualFold(h.g.Name, w.srv.Name) || strings.EqualFold(h.g.Name, strings.Split(w.srv.Host, ".")[0]) {
					named = append(named, h)
				}
			}
			hits = named
		}
		var desc []string
		for _, d := range w.disks {
			desc = append(desc, d.key+" "+fmtBytes(d.size))
		}
		if len(hits) != 1 {
			*trace = append(*trace, fmt.Sprintf("%s hat virtuelle Platten (%s), aber dazu passt %s VM.", w.srv.Name, strings.Join(desc, ", "),
				map[bool]string{true: "keine", false: "mehr als eine"}[len(hits) == 0]))
			continue
		}
		h := hits[0]
		*trace = append(*trace, fmt.Sprintf("%s läuft als VM %d „%s“ auf %s (Platten %s).", w.srv.Name, h.g.VMID, h.g.Name, h.g.Node, strings.Join(desc, ", ")))
		out = append(out, backupVM{label: "von " + w.srv.Name, cluster: h.cl, node: h.g.Node, vmid: h.g.VMID, name: h.g.Name, cfg: h.cfg})
	}
	return out
}

// nodeLayout caches what lies on the disks of a node.
type nodeLayout struct {
	disks    []prox.NodeDisk
	storages map[string]prox.Storage
	vgs      []prox.VolumeGroup
	vgsRead  bool
	pools    map[string][]string
	dirs     []prox.NodeDirectory
	dirsRead bool
}

// backupDiskUses returns the physical disks of the Proxmox VE nodes under the virtual disks of
// the VMs that hold backups: key "pve:<cluster>|<node>|<devpath>" → what they hold. ok is false
// when no such VM is found (then nobody can tell which disks of the nodes matter). trace says
// how each disk was followed down.
func (c *Core) backupDiskUses(ctx context.Context) (map[string]string, bool, []string) {
	trace := []string{}
	vms := c.backupVMs(ctx, &trace)
	if len(vms) == 0 {
		return nil, false, trace
	}
	out := map[string]map[string][]string{} // disk → "who|where" → drives
	layouts := map[string]*nodeLayout{}
	for _, vm := range vms {
		pve := c.PVEClient(vm.cluster)
		lk := vm.cluster.ID + "|" + vm.node
		lay := layouts[lk]
		if lay == nil {
			lay = &nodeLayout{storages: map[string]prox.Storage{}, pools: map[string][]string{}}
			lay.disks, _ = pve.NodeDisks(ctx, vm.node)
			if list, err := pve.Storages(ctx); err == nil {
				for _, s := range list {
					lay.storages[s.Storage] = s
				}
			}
			layouts[lk] = lay
		}
		keys := make([]string, 0, len(vm.cfg))
		for k := range vm.cfg {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if !driveKeyRe.MatchString(key) {
				continue
			}
			val := fmt.Sprint(vm.cfg[key])
			if strings.Contains(val, "media=cdrom") || strings.HasPrefix(val, "none") {
				continue
			}
			vol, _, _ := strings.Cut(val, ",")
			who := strings.TrimPrefix(strings.TrimPrefix(vm.label, "der "), "von ")
			where := ""
			step := fmt.Sprintf("%s %s (%s):", strings.TrimPrefix(vm.label, "der "), key, fmtBytes(driveSize(val)))
			var names []string
			if strings.HasPrefix(vol, "/dev/") {
				where = "durchgereicht"
				names = []string{vol}
				step += " durchgereicht " + vol
			} else {
				stName, _, _ := strings.Cut(vol, ":")
				st := lay.storages[stName]
				where = "auf " + stName
				switch st.Type {
				case "lvm", "lvmthin":
					if !lay.vgsRead {
						lay.vgs, _ = pve.NodeLVM(ctx, vm.node)
						lay.vgsRead = true
					}
					for _, vg := range lay.vgs {
						if vg.Name == st.VGName {
							names = append(names, vg.PVs...)
						}
					}
					step += fmt.Sprintf(" Speicher %s (LVM %s) → %s", stName, st.VGName, strings.Join(names, ", "))
				case "zfspool":
					pool, _, _ := strings.Cut(st.Pool, "/")
					if _, done := lay.pools[pool]; !done {
						lay.pools[pool], _ = pve.NodeZFSDevices(ctx, vm.node, pool)
					}
					names = lay.pools[pool]
					step += fmt.Sprintf(" Speicher %s (ZFS-Pool %s) → %s", stName, pool, strings.Join(names, ", "))
				case "dir":
					if !lay.dirsRead {
						lay.dirs, _ = pve.NodeDirectories(ctx, vm.node)
						lay.dirsRead = true
					}
					for _, d := range lay.dirs {
						if d.Path != "" && strings.HasPrefix(st.Path, d.Path) {
							names = append(names, d.Device)
						}
					}
					step += fmt.Sprintf(" Speicher %s (Verzeichnis %s) → %s", stName, st.Path, strings.Join(names, ", "))
				case "":
					step += " Speicher " + stName + " unbekannt"
				default:
					step += fmt.Sprintf(" Speicher %s (%s) liegt nicht auf Platten des Knotens", stName, st.Type)
				}
			}
			var found []string
			for _, n := range names {
				if d, ok := matchDisk(n, lay.disks); ok {
					k := "pve:" + vm.cluster.ID + "|" + vm.node + "|" + d.DevPath
					if out[k] == nil {
						out[k] = map[string][]string{}
					}
					g := who + "|" + where
					if !slices.Contains(out[k][g], key) {
						out[k][g] = append(out[k][g], key)
					}
					if !slices.Contains(found, d.DevPath) {
						found = append(found, d.DevPath)
					}
				}
			}
			if len(found) > 0 {
				step += " ⇒ " + vm.node + ": " + strings.Join(found, ", ")
			} else if len(names) > 0 {
				step += " ⇒ keine dieser Geräte unter den Platten von " + vm.node + " gefunden"
			}
			trace = append(trace, step)
		}
	}
	res := map[string]string{}
	for k, groups := range out {
		var parts []string
		for g, drives := range groups {
			who, where, _ := strings.Cut(g, "|")
			parts = append(parts, who+": "+strings.Join(drives, ", ")+" "+where)
		}
		sort.Strings(parts)
		res[k] = strings.Join(parts, "; ")
	}
	return res, true, trace
}
