package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Enlarging a datastore of a Backup Server that runs as VM in a connected Proxmox VE: the
// virtual disk grows in Proxmox VE, then the Backup Server sees the new size, grows the
// partition and the filesystem (ext4, xfs) – all while it keeps running.

// GrowPlan is what enlarging a datastore involves, or why it is not possible.
type GrowPlan struct {
	ServerID   string `json:"server_id"`
	ServerName string `json:"server_name"`
	Store      string `json:"store"`
	Path       string `json:"path"`
	Device     string `json:"device,omitempty"` // /dev/sdb1
	Disk       string `json:"disk,omitempty"`   // sdb
	Partition  string `json:"partition,omitempty"`
	FS         string `json:"fs,omitempty"`
	FSSize     int64  `json:"fs_size,omitempty"`
	// The virtual disk in Proxmox VE.
	Cluster     string `json:"cluster,omitempty"`
	ClusterName string `json:"cluster_name,omitempty"`
	Node        string `json:"node,omitempty"`
	VMID        int    `json:"vmid,omitempty"`
	VMName      string `json:"vm_name,omitempty"`
	Drive       string `json:"drive,omitempty"`   // scsi1
	Storage     string `json:"storage,omitempty"` // hddpool
	Size        int64  `json:"size,omitempty"`    // current size of the virtual disk
	Free        int64  `json:"free,omitempty"`    // free space on the storage
	// Pending: the virtual disk is clearly larger than the filesystem (an earlier attempt
	// stopped half way, or the disk was enlarged by hand) – growing to the same size catches up.
	Pending bool   `json:"pending,omitempty"`
	Problem string `json:"problem,omitempty"` // why it cannot be enlarged from here
}

// growInfoScript finds what a datastore path lies on (the marker lets the demo answer).
const growInfoScript = `# pk95-grow-info
p="$1"
set -- $(findmnt -no SOURCE,FSTYPE --target "$p")
src="${1%%\[*}"; fs="$2"; d="${src#/dev/}"
disk=$(lsblk -dno PKNAME "$src" 2>/dev/null)
[ -n "$disk" ] || disk="$d"
echo "src=$src"
echo "fs=$fs"
echo "disk=$disk"
echo "part=$(cat /sys/class/block/$d/partition 2>/dev/null)"
echo "serial=$(lsblk -dno SERIAL /dev/$disk 2>/dev/null)"
echo "fssize=$(df -B1 --output=size "$p" | tail -1)"`

// growScript lets the Backup Server see the larger disk and grows partition and filesystem.
const growScript = `# pk95-grow
set -e
disk="$1"; part="$2"; dev="$3"; fs="$4"; p="$5"; want="$6"
echo 1 > /sys/class/block/$disk/device/rescan
i=0
while [ $(( $(cat /sys/class/block/$disk/size) * 512 )) -lt "$want" ] && [ $i -lt 30 ]; do sleep 1; i=$((i+1)); done
if [ $(( $(cat /sys/class/block/$disk/size) * 512 )) -lt "$want" ]; then echo "Der Backup-Server sieht die größere Platte nicht."; exit 3; fi
if [ -n "$part" ] && [ "$part" != "0" ]; then
  if command -v growpart >/dev/null 2>&1; then
    growpart /dev/$disk $part || [ $? -eq 1 ]
  else
    sfdisk --relocate gpt-bak-std /dev/$disk >/dev/null 2>&1 || true
    echo ", +" | sfdisk -N $part --no-reread /dev/$disk
    partx -u /dev/$disk
  fi
fi
case "$fs" in
  ext2|ext3|ext4) resize2fs "$dev" ;;
  xfs) xfs_growfs "$p" ;;
esac
echo "fssize=$(df -B1 --output=size "$p" | tail -1)"`

// growFS are the filesystems that grow while mounted.
var growFS = map[string]bool{"ext2": true, "ext3": true, "ext4": true, "xfs": true}

func keyValues(out string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), "="); ok {
			m[k] = strings.TrimSpace(v)
		}
	}
	return m
}

// PlanGrow finds the virtual disk under a datastore and how much may be added.
func (c *Core) PlanGrow(ctx context.Context, serverID, store string) (GrowPlan, error) {
	pbs, srv, err := c.PBS(serverID)
	if err != nil {
		return GrowPlan{}, err
	}
	p := GrowPlan{ServerID: srv.ID, ServerName: srv.Name, Store: store}
	cfgs, err := pbs.Datastores(ctx)
	if err != nil {
		return p, err
	}
	found := false
	for _, d := range cfgs {
		if d.Name != store {
			continue
		}
		found, p.Path = true, d.Path
		switch {
		case d.BackingDevice != "":
			p.Problem = "Eine Wechsel- oder Archiv-Platte lässt sich nicht vergrößern – dafür eine größere Platte nehmen."
		case d.Backend != "":
			p.Problem = "Ein Cloud-Datastore (S3) hat keine feste Größe."
		}
	}
	if !found {
		return p, fmt.Errorf("Datastore %s nicht gefunden.", store)
	}
	if p.Problem != "" {
		return p, nil
	}
	run, err := c.Runner(srv)
	if err != nil {
		p.Problem = "Dafür braucht die Appliance den SSH-Zugang zu " + srv.Name + " (einrichten unter Einstellungen → Aktualisierung)."
		return p, nil
	}
	out, err := run.Run(ctx, "sh", "-c", growInfoScript, "sh", p.Path)
	if err != nil {
		return p, fmt.Errorf("Platte des Datastores nicht lesbar: %w", err)
	}
	kv := keyValues(out)
	p.Device, p.FS, p.Disk, p.Partition = kv["src"], kv["fs"], kv["disk"], kv["part"]
	p.FSSize, _ = strconv.ParseInt(kv["fssize"], 10, 64)
	m := driveSerialRe.FindStringSubmatch(kv["serial"])
	switch {
	case !strings.HasPrefix(p.Device, "/dev/") || strings.HasPrefix(p.Device, "/dev/mapper/") || strings.HasPrefix(p.Device, "/dev/dm-"):
		p.Problem = fmt.Sprintf("Der Datastore liegt auf %s – vergrößern lässt sich hier nur eine Partition oder Platte mit ext4 oder xfs.", orDash(p.Device))
	case !growFS[p.FS]:
		p.Problem = fmt.Sprintf("Das Dateisystem %s lässt sich nicht im laufenden Betrieb vergrößern (nur ext4 und xfs).", orDash(p.FS))
	case m == nil:
		p.Problem = "Die Platte des Datastores ist keine virtuelle Platte einer Proxmox-VM – eine echte Platte lässt sich nicht vergrößern."
	}
	if p.Problem != "" {
		return p, nil
	}
	p.Drive = m[1]
	var vm *backupVM
	var trace []string
	for _, v := range c.backupVMs(ctx, &trace) {
		if v.server == srv.ID {
			vm = &v
			break
		}
	}
	if vm == nil {
		p.Problem = "Die VM von " + srv.Name + " wurde in keinem verbundenen Proxmox VE gefunden."
		return p, nil
	}
	p.Cluster, p.ClusterName, p.Node, p.VMID, p.VMName = vm.cluster.ID, vm.cluster.Name, vm.node, vm.vmid, vm.name
	val, _ := vm.cfg[p.Drive].(string)
	p.Size = driveSize(val)
	p.Storage, _, _ = strings.Cut(val, ":")
	if p.Size == 0 || p.Storage == "" {
		p.Problem = "Die Platte " + p.Drive + " steht nicht in der Konfiguration der VM."
		return p, nil
	}
	p.Pending = p.FSSize > 0 && p.FSSize < p.Size-p.Size/50
	pve := c.PVEClient(vm.cluster)
	if perms, err := pve.Permissions(ctx); err == nil {
		if _, ok := perms["/"]["VM.Config.Disk"]; !ok {
			p.Problem = "Dem Token von " + vm.cluster.Name + " fehlt das Recht VM.Config.Disk."
			return p, nil
		}
	}
	if sts, err := pve.NodeStorages(ctx, vm.node, ""); err == nil {
		for _, s := range sts {
			if s.Storage == p.Storage {
				p.Free = s.Avail
			}
		}
	}
	if p.Free < 1<<30 && !p.Pending {
		p.Problem = "Auf " + p.Storage + " (" + vm.node + ") ist kein Platz mehr frei."
	}
	return p, nil
}

func orDash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}

// GrowDatastore enlarges the virtual disk under a datastore to newGiB and grows partition and
// filesystem on the Backup Server.
func (c *Core) GrowDatastore(ctx context.Context, serverID, store string, newGiB int64, by string) (GrowPlan, error) {
	p, err := c.PlanGrow(ctx, serverID, store)
	if err != nil {
		return p, err
	}
	if p.Problem != "" {
		return p, errors.New(p.Problem)
	}
	want, old, oldFS := newGiB<<30, p.Size, p.FSSize
	catchUp := p.Pending && want <= old // only partition and filesystem follow the disk
	if catchUp {
		want = old
	}
	if want <= old && !catchUp {
		return p, fmt.Errorf("Die neue Größe muss über den bisherigen %s liegen.", fmtBytes(old))
	}
	if want-old > p.Free {
		return p, fmt.Errorf("Auf %s sind nur %s frei.", p.Storage, fmtBytes(p.Free))
	}
	cl, err := c.Cluster(p.Cluster)
	if err != nil {
		return p, err
	}
	pve := c.PVEClient(cl)
	upid := ""
	if !catchUp {
		if upid, err = pve.ResizeDisk(ctx, p.Node, p.VMID, p.Drive, strconv.FormatInt(newGiB, 10)+"G"); err != nil {
			return p, fmt.Errorf("Platte in Proxmox VE vergrößern: %w", err)
		}
	}
	if upid != "" {
		st, err := pve.WaitTask(ctx, upid, 10*time.Minute)
		if err != nil {
			return p, fmt.Errorf("Platte in Proxmox VE vergrößern: %w", err)
		}
		if st.ExitStatus != "" && st.ExitStatus != "OK" {
			return p, fmt.Errorf("Platte in Proxmox VE vergrößern: %s", st.ExitStatus)
		}
	}
	srv, _ := c.Server(serverID)
	run, err := c.Runner(srv)
	if err != nil {
		return p, err
	}
	out, err := run.Run(ctx, "sh", "-c", growScript, "sh", p.Disk, p.Partition, p.Device, p.FS, p.Path, strconv.FormatInt(want, 10))
	if err != nil {
		c.Event("critical", p.ServerName, "Datastore "+store+" nur halb vergrößert",
			"Die Platte "+p.Drive+" der VM "+strconv.Itoa(p.VMID)+" ist in Proxmox VE jetzt "+fmtBytes(want)+" groß, aber Partition oder Dateisystem auf "+p.ServerName+
				" sind nicht mitgewachsen: "+strings.TrimSpace(out)+" ("+err.Error()+"). Ein erneuter Versuch mit derselben Größe holt das nach.")
		return p, fmt.Errorf("Partition und Dateisystem vergrößern: %s %w", strings.TrimSpace(out), err)
	}
	if n, err := strconv.ParseInt(keyValues(out)["fssize"], 10, 64); err == nil {
		p.FSSize = n
	}
	p.Size, p.Pending = want, false
	what := fmt.Sprintf("Die Platte %s der VM %d „%s“ wurde von %s auf %s vergrößert", p.Drive, p.VMID, p.VMName, fmtBytes(old), fmtBytes(want))
	if catchUp {
		what = fmt.Sprintf("Partition und Dateisystem sind der Platte %s der VM %d „%s“ (%s) gefolgt", p.Drive, p.VMID, p.VMName, fmtBytes(want))
	}
	c.Event("info", p.ServerName, "Datastore "+store+" vergrößert",
		fmt.Sprintf("%s; der Datastore %s hat jetzt %s statt %s. Ausgelöst von %s.", what, store, fmtBytes(p.FSSize), fmtBytes(oldFS), by))
	c.RequestRefresh()
	return p, nil
}
