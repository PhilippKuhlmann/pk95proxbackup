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

// backupDiskUses returns the physical disks under the virtual disks of this appliance:
// key "pve:<cluster>|<node>|<devpath>" → what they hold. ok is false when the VM is not found
// (then nobody can tell which disks of the nodes matter).
func (c *Core) backupDiskUses(ctx context.Context) (map[string]string, bool) {
	self, err := c.FindSelfVM(ctx)
	if err != nil {
		return nil, false
	}
	cl, err := c.Cluster(self.Cluster)
	if err != nil {
		return nil, false
	}
	pve := c.PVEClient(cl)
	disks, err := pve.NodeDisks(ctx, self.Node)
	if err != nil {
		return nil, false
	}
	storages := map[string]prox.Storage{}
	if list, err := pve.Storages(ctx); err == nil {
		for _, s := range list {
			storages[s.Storage] = s
		}
	}
	out := map[string][]string{}
	add := func(d prox.NodeDisk, what string) {
		k := "pve:" + cl.ID + "|" + self.Node + "|" + d.DevPath
		if !slices.Contains(out[k], what) {
			out[k] = append(out[k], what)
		}
	}
	var vgs []prox.VolumeGroup
	var dirs []prox.NodeDirectory
	pools := map[string][]string{}
	keys := make([]string, 0, len(self.Config))
	for k := range self.Config {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !driveKeyRe.MatchString(key) {
			continue
		}
		val := fmt.Sprint(self.Config[key])
		if strings.Contains(val, "media=cdrom") {
			continue
		}
		vol, _, _ := strings.Cut(val, ",")
		what := "Platte " + key + " der Appliance-VM"
		if strings.HasPrefix(vol, "/dev/") { // passed through
			if d, ok := matchDisk(vol, disks); ok {
				add(d, what+" (durchgereicht)")
			}
			continue
		}
		stName, _, ok := strings.Cut(vol, ":")
		if !ok {
			continue
		}
		st := storages[stName]
		what += " auf " + stName
		var names []string
		switch st.Type {
		case "lvm", "lvmthin":
			if vgs == nil {
				vgs, _ = pve.NodeLVM(ctx, self.Node)
			}
			for _, vg := range vgs {
				if vg.Name == st.VGName {
					names = append(names, vg.PVs...)
				}
			}
		case "zfspool":
			pool, _, _ := strings.Cut(st.Pool, "/")
			if _, done := pools[pool]; !done {
				pools[pool], _ = pve.NodeZFSDevices(ctx, self.Node, pool)
			}
			names = pools[pool]
		case "dir":
			if dirs == nil {
				dirs, _ = pve.NodeDirectories(ctx, self.Node)
			}
			for _, d := range dirs {
				if d.Path != "" && strings.HasPrefix(st.Path, d.Path) {
					names = append(names, d.Device)
				}
			}
		}
		for _, n := range names {
			if d, ok := matchDisk(n, disks); ok {
				add(d, what)
			}
		}
	}
	res := map[string]string{}
	for k, v := range out {
		res[k] = strings.Join(v, "; ")
	}
	return res, true
}
