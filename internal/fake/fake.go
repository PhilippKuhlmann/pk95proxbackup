// Package fake simulates Proxmox Backup Server and Proxmox VE for the demo mode
// (PK95PB_DEMO=1) and the tests: TLS servers with the API subset the appliance uses.
package fake

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
	"pk95proxbackup/internal/shares"
)

const gib = int64(1) << 30

// ---- shared task bookkeeping ----

type task struct {
	UPID   string
	Node   string
	Type   string
	ID     string
	User   string
	Start  int64
	End    int64
	Status string
	Log    []string
	Dur    time.Duration
}

type tasks struct {
	mu   sync.Mutex
	list []*task
	n    int
}

func (t *tasks) start(node, typ, id string, dur time.Duration, lines []string, done func() (string, []string)) string {
	t.mu.Lock()
	t.n++
	upid := fmt.Sprintf("UPID:%s:%08X:%08X:%08X:%s:%s:root@pam:", node, 1000+t.n, t.n, time.Now().Unix(), typ, id)
	tk := &task{UPID: upid, Node: node, Type: typ, ID: id, User: "root@pam!pk95proxbackup", Start: time.Now().Unix(), Log: lines, Dur: dur}
	t.list = append(t.list, tk)
	t.mu.Unlock()
	go func() {
		time.Sleep(dur)
		status, more := "OK", []string(nil)
		if done != nil {
			status, more = done()
		}
		t.mu.Lock()
		tk.Log = append(tk.Log, more...)
		tk.Log = append(tk.Log, "TASK "+status)
		tk.End = time.Now().Unix()
		tk.Status = status
		t.mu.Unlock()
	}()
	return upid
}

func (t *tasks) get(upid string) *task {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, x := range t.list {
		if x.UPID == upid {
			cp := *x
			return &cp
		}
	}
	return nil
}

func (t *tasks) recent(limit int) []*task {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []*task{}
	for i := len(t.list) - 1; i >= 0 && len(out) < limit; i-- {
		cp := *t.list[i]
		out = append(out, &cp)
	}
	return out
}

func (t *tasks) seed(node, typ, id, status string, start time.Time, dur time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.n++
	upid := fmt.Sprintf("UPID:%s:%08X:%08X:%08X:%s:%s:root@pam:", node, 1000+t.n, t.n, start.Unix(), typ, id)
	t.list = append(t.list, &task{UPID: upid, Node: node, Type: typ, ID: id, User: "root@pam", Start: start.Unix(),
		End: start.Add(dur).Unix(), Status: status, Log: []string{"starting " + typ + " " + id, "TASK " + status}})
}

func taskStatus(w http.ResponseWriter, tk *task) {
	if tk == nil {
		apiErr(w, 404, "no such task")
		return
	}
	st := map[string]any{"status": "running", "type": tk.Type, "id": tk.ID, "starttime": tk.Start, "upid": tk.UPID}
	if tk.End != 0 {
		st["status"] = "stopped"
		st["exitstatus"] = tk.Status
	}
	data(w, st)
}

func taskLog(w http.ResponseWriter, tk *task) {
	if tk == nil {
		apiErr(w, 404, "no such task")
		return
	}
	log := append([]string(nil), tk.Log...)
	if tk.End == 0 && tk.Dur > 0 {
		// Running: report progress the way the real servers log it.
		pct := float64(time.Since(time.Unix(tk.Start, 0))) / float64(tk.Dur) * 100
		if pct > 99 {
			pct = 99
		}
		switch tk.Type {
		case "syncjob", "verify":
			log = append(log, fmt.Sprintf("percentage done: %.2f%% (%d/5 groups)", pct, int(pct/20)))
		case "garbage_collection":
			log = append(log, fmt.Sprintf("marked %d%% (%d of 40 index files)", int(pct), int(pct*0.4)))
		case "vzdump":
			log = append(log, fmt.Sprintf("INFO: %3d%% (%.1f GiB of 32.0 GiB) in %ds, read: 412 MiB/s", int(pct), pct*0.32, int(time.Since(time.Unix(tk.Start, 0)).Seconds())))
		case "qmrestore", "vzrestore":
			log = append(log, fmt.Sprintf("progress %d%% (read %.1f GiB of 32.0 GiB, zeroes = 3%%)", int(pct), pct*0.32))
		}
	}
	lines := []map[string]any{}
	for i, l := range log {
		lines = append(lines, map[string]any{"n": i + 1, "t": l})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": lines, "total": len(lines)})
}

func data(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
}

func apiErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": nil, "message": msg})
}

func form(r *http.Request) url.Values {
	_ = r.ParseForm()
	return r.Form
}

func boolv(v string) bool { return v == "1" || v == "true" }

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func startTLS(h http.Handler) (*httptest.Server, string) {
	s := httptest.NewUnstartedServer(h)
	s.StartTLS()
	sum := sha256.Sum256(s.Certificate().Raw)
	return s, prox.FormatFingerprint(fmt.Sprintf("%x", sum[:]))
}

// ---- demo setup ----

// Servers are the simulated servers of the last Setup (for tests).
type Servers struct {
	Local, Offsite *PBS
	PVE            *PVE
	GitHub         *GitHub
}

// Current holds the servers of the last Setup.
var Current Servers

// Setup starts the simulated servers and registers them (once) in the store.
func Setup(c *core.Core) (func(), error) {
	local := NewPBS("pbs-appliance", 120*gib)
	local.AddStore("backup", "/mnt/datastore/backup", "", 2000*gib, 0)
	local.AddStore("nas-backup", "/mnt/pk95/nas/nas-backup", "", 4000*gib, 0)
	local.AddStore("usb-a", "usb-a", "3f1c2a9e-0d5b-4f7e-9a61-2c8d1e5b7a10", 1800*gib, 0)
	local.AddStore("usb-b", "usb-b", "8b2e4c71-6a3d-4e2f-b0c9-5d7a1f3e9c22", 1800*gib, 0)
	local.AddDisk("sdb", "QEMU", "QEMU HARDDISK", "hdd", 2048*gib, "filesystem", "")
	local.AddDisk("sdc", "WD", "Elements 2620", "usb", 1863*gib, "filesystem", "3f1c2a9e-0d5b-4f7e-9a61-2c8d1e5b7a10")
	local.AddDisk("sdd", "Seagate", "Expansion HDD", "usb", 3726*gib, "unused", "")
	local.SetMounted("usb-a", true)
	// A disk from another customer's appliance (to be read in).
	local.AddDisk("sde", "Intenso", "External USB 3.0", "usb", 932*gib, "filesystem", "5d2a7c14-91e3-4b8f-a6d0-7e3c2b1f9a44")
	{
		var snaps []prox.Snapshot
		for d := 1; d <= 3; d++ {
			t := time.Now().AddDate(0, 0, -30*d).Unix()
			for _, g := range []struct {
				typ, id string
				gb      int64
			}{{"vm", "200", 90}, {"vm", "201", 45}, {"ct", "210", 6}} {
				snaps = append(snaps, prox.Snapshot{Type: g.typ, ID: g.id, Time: t + int64(len(g.id)), Size: g.gb * gib})
			}
		}
		local.AddForeign("5d2a7c14-91e3-4b8f-a6d0-7e3c2b1f9a44", "usb-montag", snaps)
	}

	offsite := NewPBS("pbs-buero", 64*gib)
	offsite.AddStore("offsite", "/mnt/datastore/offsite", "", 8000*gib, 0)

	pve := NewPVE([]string{"pve1", "pve2"})
	pve.NoPrivs = PVENoPrivs
	pve.AddGuest(100, "dc01", "pve1", "qemu", "running")
	pve.AddGuest(101, "fileserver", "pve1", "qemu", "running")
	pve.AddGuest(102, "nextcloud", "pve2", "qemu", "running")
	pve.AddGuest(110, "pihole", "pve2", "lxc", "running")
	pve.AddGuest(111, "homeassistant", "pve1", "qemu", "running")
	pve.AddGuest(112, "test-win11", "pve2", "qemu", "stopped")
	pve.AddGuest(120, "pk95proxbackup", "pve1", "qemu", "running")
	pve.SetSMBIOS(120, "6b0f6f6e-3c55-4e2b-9d0e-3a7c95b0f120")
	pve.usb["pve1"] = []prox.USBDevice{
		{BusNum: 2, DevNum: 3, Port: 1, USBPath: "1", VendID: "1058", ProdID: "2620", Manufacturer: "Western Digital", Product: "Elements 2620", Speed: "5000"},
		{BusNum: 2, DevNum: 4, Port: 2, USBPath: "2", VendID: "0bc2", ProdID: "2322", Manufacturer: "Seagate", Product: "Expansion HDD", Speed: "5000"},
		{BusNum: 1, DevNum: 2, Port: 3, USBPath: "3", VendID: "046d", ProdID: "c52b", Manufacturer: "Logitech", Product: "USB Receiver", Speed: "12"},
	}
	// history: backups of all guests on "backup"
	now := time.Now()
	for d := 13; d >= 0; d-- {
		t := time.Date(now.Year(), now.Month(), now.Day()-d, 21, 0, 0, 0, now.Location())
		if t.After(now) {
			continue
		}
		for _, g := range []struct {
			id  int
			typ string
			gb  int64
		}{{100, "vm", 60}, {101, "vm", 420}, {102, "vm", 180}, {110, "ct", 4}, {111, "vm", 32}} {
			local.AddSnapshot("backup", g.typ, strconv.Itoa(g.id), t.Add(time.Duration(g.id%10)*time.Minute).Unix(), g.gb*gib)
		}
	}
	for _, sn := range local.snapshotsOf("backup") {
		if sn.Time > now.AddDate(0, 0, -6).Unix() {
			offsite.AddSnapshot("offsite", sn.Type, sn.ID, sn.Time, sn.Size)
		}
		if sn.Time < now.AddDate(0, 0, -1).Unix() {
			local.AddSnapshot("usb-a", sn.Type, sn.ID, sn.Time, sn.Size)
		}
		if sn.Time < now.AddDate(0, 0, -2).Unix() {
			local.AddSnapshot("usb-b", sn.Type, sn.ID, sn.Time, sn.Size)
		}
	}
	local.usedExtra["backup"] = 0
	// Guests that no connected Proxmox VE knows any more (deleted or another cluster).
	local.AddSnapshot("backup", "vm", "250", now.AddDate(0, 0, -40).Unix(), 120*gib)
	local.AddSnapshot("backup", "ct", "251", now.AddDate(0, 0, -35).Unix(), 6*gib)
	for i := 1; i <= 6; i++ {
		t := time.Date(now.Year(), now.Month(), now.Day()-i, 21, 0, 0, 0, now.Location())
		pve.tasks.seed("pve1", "vzdump", "", "OK", t, 42*time.Minute)
		local.tasks.seed("pbs-appliance", "garbage_collection", "backup", "OK", t.Add(3*time.Hour), 9*time.Minute)
	}
	pve.tasks.seed("pve2", "vzdump", "112", "ERROR: VM 112 - unable to find configuration file", now.Add(-20*time.Hour), time.Minute)
	local.tasks.seed("pbs-appliance", "syncjob", "pk95r-demo-a1:backup:usb-a", "OK", now.Add(-26*time.Hour), 18*time.Minute)

	// SMART: disk A is fine, the spare Seagate has replaced sectors, the Intenso adapter
	// passes no SMART values on, the virtual disk has none.
	local.SetSmart("sdc", SmartATA("passed", 0, 0, 8760, 34))
	local.SetSmart("sdd", SmartATA("passed", 8, 0, 21500, 38))
	pve.AddNodeDisk("pve1", prox.NodeDisk{DevPath: "/dev/nvme0n1", Model: "Samsung SSD 980 PRO 1TB", Serial: "S5GXNF0R123456", Size: 1000 * gib, Type: "nvme", Health: "PASSED", Wearout: "97", Used: "LVM"},
		PVESmartNVMe(3, 0, 9120, 41))
	pve.AddNodeDisk("pve1", prox.NodeDisk{DevPath: "/dev/sda", Vendor: "ATA", Model: "WDC WD40EFRX-68N32N0", Serial: "WD-WCC7K1234567", Size: 3726 * gib, Type: "hdd", Health: "PASSED", Wearout: "N/A", Used: "ZFS"},
		PVESmartATA(0, 0, 34100, 36))
	pve.AddNodeDisk("pve2", prox.NodeDisk{DevPath: "/dev/sda", Vendor: "ATA", Model: "CT500MX500SSD1", Serial: "2117E5A1B2C3", Size: 465 * gib, Type: "ssd", Health: "PASSED", Wearout: "8", Used: "LVM"},
		PVESmartATA(0, 0, 27800, 39))
	pve.AddNodeDisk("pve2", prox.NodeDisk{DevPath: "/dev/sdb", Vendor: "ATA", Model: "ST4000VN008-2DR166", Serial: "ZGY8ABCD", Size: 3726 * gib, Type: "hdd", Health: "PASSED", Wearout: "N/A", Used: "ZFS"},
		PVESmartATA(0, 0, 15200, 35))

	// Updates: a newer release on a simulated GitHub, installed only in memory.
	gh := NewGitHub(NextMinor(c.Version))
	core.GitHubAPI, core.GitHubWeb = gh.URL, gh.URL
	c.InstallBinary = func(ctx context.Context, _ []byte, version string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
		c.Version = version
		return nil
	}
	// Configuration backups go into the simulated datastores.
	byServer := map[string]*PBS{"local": local, "buero": offsite}
	c.UploadSelfBackup = func(_ context.Context, srv core.PBSServer, st string, b []byte) error {
		p := byServer[srv.ID]
		if p == nil || !p.AddBlob(st, "host", "pk95proxbackup", time.Now().Unix(), "pk95proxbackup.conf.blob", b) {
			return fmt.Errorf("datastore %s nicht erreichbar", st)
		}
		return nil
	}

	Current = Servers{Local: local, Offsite: offsite, PVE: pve, GitHub: gh}
	tg := Telegram()
	notify.TelegramAPI = tg.URL
	ls, lfp := startTLS(local)
	os_, ofp := startTLS(offsite)
	ps, pfp := startTLS(pve)
	pve.storageFingerprints = map[string]string{"local": lfp}

	runner := NewRunner()
	runner.scans["/dev/sde1"] = "STORE\tusb-montag\t2\t1\n"
	c.LocalRunner = runner
	c.SelfUUID = func() string { return "6b0f6f6e-3c55-4e2b-9d0e-3a7c95b0f120" }

	host := func(s *httptest.Server) (string, int) {
		u, _ := url.Parse(s.URL)
		p, _ := strconv.Atoi(u.Port())
		return u.Hostname(), p
	}
	lh, lp := host(ls)
	oh, op := host(os_)
	ph, pp := host(ps)
	// The ports change on every start: always (re)write the connections.
	_ = c.St.Put(core.KindPBS, "local", core.PBSServer{ID: "local", Name: "Diese Appliance (pk95-backup)", Local: true, Host: lh, Port: lp,
		PublicHost: "192.168.10.20", AuthID: "root@pam!pk95proxbackup", Secret: "demo", Fingerprint: lfp})
	_ = c.St.Put(core.KindPBS, "buero", core.PBSServer{ID: "buero", Name: "PBS Büro (extern)", Host: oh, Port: op,
		PublicHost: "10.20.0.5", AuthID: "root@pam!pk95", Secret: "demo", Fingerprint: ofp, SSHEnabled: false})
	_ = c.St.Put(core.KindPVE, "cluster", core.PVECluster{ID: "cluster", Name: "Cluster Zuhause", Host: ph, Port: pp,
		TokenID: "root@pam!pk95", Secret: "demo", Fingerprint: pfp})
	pve.AddStorage(prox.Storage{Storage: "pbs-appliance", Type: "pbs", Content: "backup", Server: "192.168.10.20", Datastore: "backup", Username: "pk95-pve@pbs!pve-cluster", Fingerprint: lfp})
	pve.AddStorage(prox.Storage{Storage: "local", Type: "dir", Content: "iso,vztmpl,backup"})
	pve.AddStorage(prox.Storage{Storage: "local-lvm", Type: "lvmthin", Content: "images,rootdir"})
	pve.AddJob(prox.BackupJob{ID: "backup-nightly", Schedule: "21:00", Storage: "pbs-appliance", Mode: "snapshot", All: 1, Exclude: "112,120", Comment: "Jede Nacht alles", NotesTemplate: "{{guestname}}"})

	var x struct{}
	if c.St.Get(core.KindShare, "nas", &x) != nil {
		_ = c.St.Put(core.KindShare, "nas", shares.Share{ID: "nas", Name: "Synology NAS", ServerID: "local", Type: "nfs", Source: "192.168.10.5:/volume1/pbs", Store: "nas-backup"})
	}
	if c.St.Get(core.KindRotation, "demo", &x) != nil {
		day := now.AddDate(0, 0, -14).Unix()
		_ = c.St.Put(core.KindRotation, "demo", core.Rotation{ID: "demo", Name: "USB täglich", ServerID: "local", SourceServer: "local", SourceStore: "backup",
			Rhythm: "daily", Time: "22:30", RunOnMount: true, UnmountOnDone: true, KeepLast: 7, KeepWeekly: 4,
			Disks: []core.RotDisk{{Key: "a1", Store: "usb-a", Label: "Platte A (gerade Tage)", Added: day}, {Key: "b2", Store: "usb-b", Label: "Platte B (ungerade Tage)", Added: day}}})
	}
	// The simulated servers start fresh, so the remembered runs must match their seed.
	{
		_ = c.St.SetSetting("rt:demo/a1", map[string]any{"seen_upid": "UPID:pbs-appliance:seed:pk95r-demo-a1", "last_ok": now.Add(-26 * time.Hour).Unix(), "done_at": now.Add(-25 * time.Hour).Unix(), "last_gc": now.Add(-48 * time.Hour).Unix()})
		_ = c.St.SetSetting("rt:demo/b2", map[string]any{"seen_upid": "UPID:pbs-appliance:seed:pk95r-demo-b2", "last_ok": now.Add(-50 * time.Hour).Unix(), "done_at": now.Add(-49 * time.Hour).Unix()})
	}
	local.AddJob("sync", map[string]string{"id": "pk95r-demo-a1", "store": "usb-a", "remote-store": "backup", "run-on-mount": "1", "comment": "PK95 Wechselplatte Platte A"}, now.Add(-26*time.Hour).Unix(), "OK")
	local.AddJob("sync", map[string]string{"id": "pk95r-demo-b2", "store": "usb-b", "remote-store": "backup", "run-on-mount": "1", "comment": "PK95 Wechselplatte Platte B"}, now.Add(-50*time.Hour).Unix(), "OK")
	local.AddJob("prune", map[string]string{"id": "pk95-prune-backup", "store": "backup", "schedule": "daily", "keep-last": "3", "keep-daily": "14", "keep-weekly": "8", "keep-monthly": "12"}, now.Add(-10*time.Hour).Unix(), "OK")
	local.AddJob("verify", map[string]string{"id": "pk95-verify-backup", "store": "backup", "schedule": "sat 04:00"}, now.Add(-72*time.Hour).Unix(), "OK")
	local.AddJob("sync", map[string]string{"id": "pk95-sync-offsite", "store": "backup", "remote-store": "offsite", "remote": "pk95-buero", "schedule": "*-*-* 03:00", "comment": "Kopie ins Büro (Push über Pull)"}, now.Add(-20*time.Hour).Unix(), "OK")
	offsite.AddJob("sync", map[string]string{"id": "pk95-sync-home", "store": "offsite", "remote-store": "backup", "remote": "pk95-local", "schedule": "*-*-* 03:00", "comment": "Kopie von Zuhause"}, now.Add(-20*time.Hour).Unix(), "OK")
	local.updateDatastore("backup", url.Values{"gc-schedule": {"daily"}, "comment": {"Hauptspeicher (2 TB virtuelle Disk)"}})
	local.updateDatastore("nas-backup", url.Values{"gc-schedule": {"sun 06:00"}, "comment": {"Synology per NFS"}})

	return func() { ls.Close(); os_.Close(); ps.Close(); tg.Close(); gh.Close() }, nil
}

// ---- runner ----

// Runner fakes shell commands for shares (systemctl, findmnt, mount).
type Runner struct {
	mu      sync.Mutex
	mounted map[string]bool
	files   map[string]string
	scans   map[string]string // device → output of the datastore scan
}

// NewRunner returns a runner where the demo share is mounted.
func NewRunner() *Runner {
	return &Runner{mounted: map[string]bool{shares.Root + "/nas": true}, files: map[string]string{}, scans: map[string]string{}}
}

// Run implements hostexec.Runner.
func (r *Runner) Run(_ context.Context, name string, args ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch name {
	case "findmnt":
		mp := args[len(args)-1]
		if r.mounted[mp] {
			return fmt.Sprintf("%d %d %d\n", 4000*gib, 1310*gib, 2690*gib), nil
		}
		return "", fmt.Errorf("findmnt: exit status 1")
	case "systemctl":
		if len(args) >= 2 {
			unit := args[len(args)-1]
			mp := shares.Root + "/" + strings.TrimSuffix(strings.TrimPrefix(unit, "mnt-pk95-"), ".mount")
			switch args[0] {
			case "start":
				r.mounted[mp] = true
			case "enable":
				r.mounted[mp] = true
			case "stop", "disable":
				r.mounted[mp] = false
			case "is-failed":
				return "inactive", nil
			}
		}
		return "", nil
	case "sh":
		// datastore scan of a partition: sh -c <script> sh <device>
		if len(args) == 4 && strings.Contains(args[1], ".chunks") {
			out, ok := r.scans[args[3]]
			if !ok {
				return "mount: wrong fs type", fmt.Errorf("sh: exit status 2")
			}
			return out, nil
		}
	case "proxmox-backup-manager":
		return "proxmox-backup-server 4.0.18-1 running version: 4.0.18", nil
	}
	return "", nil
}

// WriteFile implements hostexec.Runner.
func (r *Runner) WriteFile(_ context.Context, path string, data []byte, _ os.FileMode) error {
	r.mu.Lock()
	r.files[path] = string(data)
	r.mu.Unlock()
	return nil
}

// RemoveFile implements hostexec.Runner.
func (r *Runner) RemoveFile(_ context.Context, path string) error {
	r.mu.Lock()
	delete(r.files, path)
	r.mu.Unlock()
	return nil
}

// Describe implements hostexec.Runner.
func (r *Runner) Describe() string { return "Demo" }

var _ = sort.Strings
