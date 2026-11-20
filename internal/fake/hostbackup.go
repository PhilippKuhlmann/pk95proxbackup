package fake

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/prox"
)

// HostBackupDuration is how long a simulated host backup runs (tests shorten it).
var HostBackupDuration = 4 * time.Second

// NodeShell simulates the SSH login to a Proxmox VE node for the host backup: the backup
// lands as host/<node> in a datastore of a simulated Backup Server.
type NodeShell struct {
	PBS   *PBS
	Store string
	Node  string
	Fail  string // message of a failing backup ("" = works)
	mu    sync.Mutex
	runs  int
}

// Runs counts the backups that were started.
func (n *NodeShell) Runs() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.runs
}

// SetFail changes the message of a failing backup.
func (n *NodeShell) SetFail(msg string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Fail = msg
}

// Run implements hostexec.Runner.
func (n *NodeShell) Run(ctx context.Context, name string, args ...string) (string, error) {
	script := ""
	if name == "bash" && len(args) == 2 && args[0] == "-c" {
		script = args[1]
	}
	switch {
	case strings.HasPrefix(script, "# pk95-hostcheck"):
		return "pve-manager/9.0.10/a2f1c0de (running kernel: 6.14.11-2-pve)\n", nil
	case strings.HasPrefix(script, "# pk95-hostbackup"):
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(HostBackupDuration):
		}
		n.mu.Lock()
		n.runs++
		fail := n.Fail
		n.mu.Unlock()
		if fail != "" {
			return "PK95-ERR: " + fail + "\n", errors.New("bash: exit status 3")
		}
		now := time.Now()
		n.PBS.AddHostBackup(n.Store, n.Node, now.Unix(), 41*mib+int64(now.Day())*mib/3)
		return fmt.Sprintf("Starting backup: host/%s/%s\nUpload directory '/etc' to '…' as etc.pxar.didx\netc.pxar: had to backup 1.2 MiB of 9.8 MiB (compressed 380 KiB) in 0.41 s\nDuration: 3.62s\nEnd Time: %s\nPK95-OK\n",
			n.Node, now.UTC().Format("2006-01-02T15:04:05Z"), now.Format(time.RFC1123)), nil
	}
	return "", fmt.Errorf("%s: nicht simuliert", name)
}

// WriteFile implements hostexec.Runner.
func (n *NodeShell) WriteFile(context.Context, string, []byte, os.FileMode) error { return nil }

// RemoveFile implements hostexec.Runner.
func (n *NodeShell) RemoveFile(context.Context, string) error { return nil }

// Describe implements hostexec.Runner.
func (n *NodeShell) Describe() string { return "root@" + n.Node }

// AddHostBackup stores a host backup (as proxmox-backup-client on a node would).
func (p *PBS) AddHostBackup(st, node string, t, size int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s := p.stores[st]; s != nil {
		s.snaps = append(s.snaps, hostSnap(node, t, size))
	}
}

const mib = int64(1) << 20

func hostSnap(node string, t, size int64) prox.Snapshot {
	return prox.Snapshot{Type: "host", ID: node, Time: t, Size: size, Owner: "pk95-pve@pbs!pve-cluster", Verification: &struct {
		State string `json:"state"`
	}{State: "ok"}}
}

// hostTree is the content of the archives of a simulated host backup.
var hostTree = map[string][]string{
	"etc": {"hostname", "hosts", "resolv.conf", "fstab", "vzdump.conf", "network/interfaces", "default/grub", "kernel/cmdline",
		"modprobe.d/zfs.conf", "ssh/sshd_config", "apt/sources.list.d/proxmox.sources", "apt/sources.list.d/debian.sources",
		"pve/storage.cfg", "pve/user.cfg", "pve/datacenter.cfg", "pve/corosync.conf", "pve/jobs.cfg", "pve/firewall/cluster.fw",
		"pve/nodes/pve1/qemu-server/120.conf", "pve/nodes/pve1/lxc/110.conf", "pve/nodes/pve2/qemu-server/130.conf",
		"pve/priv/storage/pbs-appliance.pw", "pve/priv/authkey.key"},
	"root": {".bashrc", ".profile", ".ssh/authorized_keys", "scripts/smart-report.sh"},
	"info": {"LIESMICH.txt", "pveversion.txt", "hostname.txt", "ip-addr.txt", "ip-route.txt", "lsblk.txt", "blkid.txt", "df.txt",
		"zpool-status.txt", "zpool-list.txt", "zfs-list.txt", "lvm.txt", "cluster.txt", "guests.txt", "packages.txt", "boot.txt", "crontabs.txt", "config.db"},
}
