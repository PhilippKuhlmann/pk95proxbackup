package fake

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/enckey"
	"pk95proxbackup/internal/prox"
)

// RestoreDuration is how long a simulated restore runs (tests shorten it).
var RestoreDuration = 20 * time.Second

// PVENoPrivs makes the next Setup simulate an API token without rights.
var PVENoPrivs bool

// VzdumpDuration is how long a simulated backup runs (tests shorten it).
var VzdumpDuration = 25 * time.Second

// PVE is a simulated Proxmox VE cluster.
type PVE struct {
	mu                  sync.Mutex
	nodes               []string
	guests              []prox.Guest
	smbios              map[int]string
	usbCfg              map[int]map[string]string
	usb                 map[string][]prox.USBDevice
	storages            []prox.Storage
	jobs                []prox.BackupJob
	tasks               tasks
	storageFingerprints map[string]string
	cfg                 map[int]map[string]string
	started             map[int]time.Time
	jobN                int
	// NoPrivs simulates a token without rights (privilege separation, no ACL).
	NoPrivs bool
	disks   map[string][]NodeDisk
}

// NewPVE creates a cluster with the given nodes.
func NewPVE(nodes []string) *PVE {
	return &PVE{nodes: nodes, smbios: map[int]string{}, usbCfg: map[int]map[string]string{}, usb: map[string][]prox.USBDevice{},
		cfg: map[int]map[string]string{}, started: map[int]time.Time{}}
}

// AddGuest adds a VM (qemu) or container (lxc).
func (p *PVE) AddGuest(id int, name, node, typ, status string) {
	p.guests = append(p.guests, prox.Guest{VMID: id, Name: name, Node: node, Type: typ, Status: status, MaxDisk: 64 * gib, MaxMem: 4 * gib})
}

// SetSMBIOS sets the SMBIOS UUID of a VM.
func (p *PVE) SetSMBIOS(id int, uuid string) { p.smbios[id] = uuid }

// AddStorage adds a storage definition.
func (p *PVE) AddStorage(s prox.Storage) { p.storages = append(p.storages, s) }

// StorageList returns the storage definitions (for tests).
func (p *PVE) StorageList() []prox.Storage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]prox.Storage(nil), p.storages...)
}

// AddJob adds a backup job.
func (p *PVE) AddJob(j prox.BackupJob) { p.jobs = append(p.jobs, j) }

func (p *PVE) guest(id int) *prox.Guest {
	for i := range p.guests {
		if p.guests[i].VMID == id {
			return &p.guests[i]
		}
	}
	return nil
}

func (p *PVE) jobParams(j *prox.BackupJob, f url.Values) {
	for _, d := range strings.Split(f.Get("delete"), ",") {
		switch d {
		case "vmid":
			j.VMID = ""
		case "exclude":
			j.Exclude = ""
		case "all":
			j.All = 0
		case "node":
			j.Node = ""
		case "comment":
			j.Comment = ""
		case "notes-template":
			j.NotesTemplate = ""
		case "prune-backups":
			j.PruneBackups = ""
		case "mailto":
			j.MailTo = ""
		case "notification-mode":
			j.NotificationMode = ""
		}
	}
	set := func(k string, dst *string) {
		if v, ok := f[k]; ok {
			*dst = v[0]
		}
	}
	set("schedule", &j.Schedule)
	set("storage", &j.Storage)
	set("mode", &j.Mode)
	set("vmid", &j.VMID)
	set("exclude", &j.Exclude)
	set("node", &j.Node)
	set("comment", &j.Comment)
	set("notes-template", &j.NotesTemplate)
	set("prune-backups", &j.PruneBackups)
	set("mailto", &j.MailTo)
	set("notification-mode", &j.NotificationMode)
	if v, ok := f["all"]; ok {
		j.All = atoi(v[0])
	}
	if v, ok := f["enabled"]; ok {
		e := atoi(v[0])
		j.Enabled = &e
	}
}

func (p *PVE) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api2/json")
	if !strings.HasPrefix(r.Header.Get("Authorization"), "PVEAPIToken=") {
		apiErr(w, 401, "authentication failure")
		return
	}
	seg := strings.Split(strings.Trim(path, "/"), "/")
	f := form(r)
	m := r.Method
	if len(seg) == 6 && seg[0] == "nodes" && seg[2] == "storage" && seg[4] == "file-restore" {
		switch seg[5] {
		case "list":
			p.fileList(w, f.Get("volume"), f.Get("filepath"))
		case "download":
			p.fileDownload(w, f.Get("filepath"))
		}
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.NoPrivs {
		// Like Proxmox VE: lists come back empty, the job list needs Sys.Audit.
		switch path {
		case "/cluster/resources", "/cluster/backup-info/not-backed-up", "/storage":
			data(w, []any{})
			return
		case "/cluster/backup":
			apiErr(w, 403, "Permission check failed (/, Sys.Audit)")
			return
		}
	}
	switch {
	case path == "/version":
		data(w, map[string]string{"version": "9.0.10", "release": "9.0"})
	case path == "/access/permissions":
		root := map[string]int{}
		if !p.NoPrivs {
			for _, pr := range prox.PVERequiredPrivs {
				root[pr] = 1
			}
		}
		data(w, map[string]map[string]int{"/": root})
	case path == "/nodes":
		out := []prox.Node{}
		for _, n := range p.nodes {
			out = append(out, prox.Node{Node: n, Status: "online", CPU: 0.12, MaxCPU: 16, Mem: 22 * gib, MaxMem: 64 * gib, Uptime: 1200000})
		}
		data(w, out)
	case path == "/cluster/resources":
		data(w, p.guests)
	case path == "/cluster/backup-info/not-backed-up":
		covered := map[int]bool{}
		for _, j := range p.jobs {
			ex, in := idSet(j.Exclude), idSet(j.VMID)
			for _, g := range p.guests {
				id := strconv.Itoa(g.VMID)
				if j.All != 0 && !ex[id] || in[id] {
					covered[g.VMID] = true
				}
			}
		}
		out := []prox.Guest{}
		for _, g := range p.guests {
			if !covered[g.VMID] {
				out = append(out, g)
			}
		}
		data(w, out)
	case path == "/cluster/nextid":
		max := 100
		for _, g := range p.guests {
			if g.VMID >= max {
				max = g.VMID + 1
			}
		}
		data(w, strconv.Itoa(max))
	case path == "/storage" && m == "GET":
		data(w, p.storages)
	case path == "/storage" && m == "POST":
		for _, s := range p.storages {
			if s.Storage == f.Get("storage") {
				apiErr(w, 500, "create storage failed: storage ID '"+s.Storage+"' already defined")
				return
			}
		}
		port := atoi(f.Get("port"))
		keyFP := ""
		if k := f.Get("encryption-key"); k != "" {
			_, fp, err := enckey.Parse(k)
			if err != nil {
				apiErr(w, 500, "create storage failed: Value does not seems like a valid, JSON formatted encryption key!")
				return
			}
			keyFP = fp
		}
		p.storages = append(p.storages, prox.Storage{Storage: f.Get("storage"), Type: f.Get("type"), Content: f.Get("content"), Server: f.Get("server"), Port: port,
			Datastore: f.Get("datastore"), Username: f.Get("username"), Fingerprint: f.Get("fingerprint"), Namespace: f.Get("namespace"), EncryptionKey: keyFP})
		data(w, nil)
	case len(seg) == 2 && seg[0] == "storage" && m == "PUT":
		for i := range p.storages {
			if p.storages[i].Storage != seg[1] {
				continue
			}
			if f.Get("delete") == "encryption-key" {
				p.storages[i].EncryptionKey = ""
			} else if k := f.Get("encryption-key"); k != "" {
				_, fp, err := enckey.Parse(k)
				if err != nil {
					apiErr(w, 500, "update storage failed: Value does not seems like a valid, JSON formatted encryption key!")
					return
				}
				p.storages[i].EncryptionKey = fp
			}
			data(w, nil)
			return
		}
		apiErr(w, 500, "update storage failed: storage '"+seg[1]+"' does not exist")
	case len(seg) == 2 && seg[0] == "storage" && m == "DELETE":
		keep := p.storages[:0]
		for _, s := range p.storages {
			if s.Storage != seg[1] {
				keep = append(keep, s)
			}
		}
		p.storages = keep
		data(w, nil)
	case len(seg) == 3 && seg[0] == "nodes" && seg[2] == "storage":
		out := []prox.NodeStorage{}
		for _, s := range p.storages {
			if c := f.Get("content"); c != "" && !strings.Contains(s.Content, c) {
				continue
			}
			out = append(out, prox.NodeStorage{Storage: s.Storage, Type: s.Type, Content: s.Content, Active: 1, Enabled: 1, Total: 900 * gib, Used: 300 * gib, Avail: 600 * gib})
		}
		data(w, out)
	case path == "/cluster/backup" && m == "GET":
		out := []prox.BackupJob{}
		for _, j := range p.jobs {
			j.NextRun = time.Now().Add(5 * time.Hour).Unix()
			out = append(out, j)
		}
		data(w, out)
	case path == "/cluster/backup" && m == "POST":
		p.jobN++
		j := prox.BackupJob{ID: "backup-" + strconv.Itoa(1000+p.jobN)}
		p.jobParams(&j, f)
		p.jobs = append(p.jobs, j)
		data(w, nil)
	case len(seg) == 3 && seg[0] == "cluster" && seg[1] == "backup":
		for i := range p.jobs {
			if p.jobs[i].ID != seg[2] {
				continue
			}
			if m == "DELETE" {
				p.jobs = append(p.jobs[:i], p.jobs[i+1:]...)
			} else {
				p.jobParams(&p.jobs[i], f)
			}
			data(w, nil)
			return
		}
		apiErr(w, 404, "job does not exist")
	case len(seg) == 3 && seg[0] == "nodes" && seg[2] == "vzdump":
		ids := strings.Split(f.Get("vmid"), ",")
		mode := f.Get("mode")
		if mode == "" {
			mode = "snapshot"
		}
		head := []string{"INFO: starting new backup job: vzdump " + strings.Join(ids, " ") + " --storage " + f.Get("storage") + " --mode " + mode}
		var rest []string
		failed := false
		for i, id := range ids {
			g := p.guest(atoi(id))
			typ, name := "qemu", "vm"+id
			if g != nil {
				typ, name = g.Type, g.Name
			}
			start := []string{"INFO: Starting Backup of VM " + id + " (" + typ + ")", "INFO: Backup started at " + time.Now().Format("2006-01-02 15:04:05")}
			if i == 0 {
				head = append(head, start...)
			} else {
				rest = append(rest, start...)
			}
			if id == "112" {
				rest = append(rest, "ERROR: Backup of VM 112 failed - unable to find configuration file for VM 112 on node '"+seg[1]+"'", "INFO: Failed at "+time.Now().Format("2006-01-02 15:04:05"))
				failed = true
				continue
			}
			label := "VM Name: "
			if typ == "lxc" {
				label = "CT Name: "
			}
			gb := 8 + atoi(id)%7*9
			rest = append(rest, "INFO: "+label+name,
				"INFO: creating Proxmox Backup Server archive '"+map[string]string{"qemu": "vm", "lxc": "ct"}[typ]+"/"+id+"/"+time.Now().UTC().Format("2006-01-02T15:04:05Z")+"'",
				"INFO: backup was done incrementally, reused "+strconv.Itoa(gb-1)+".40 GiB (9"+strconv.Itoa(atoi(id)%9)+"%)",
				"INFO: transferred "+strconv.Itoa(gb)+".00 GiB in 42 seconds (512.0 MiB/s)",
				"INFO: Finished Backup of VM "+id+" (00:00:"+strconv.Itoa(20+atoi(id)%40)+")")
		}
		status := "OK"
		if failed {
			rest = append(rest, "INFO: Backup job finished with errors")
			status = "job errors"
		} else {
			rest = append(rest, "INFO: Backup job finished successfully")
		}
		data(w, p.tasks.start(seg[1], "vzdump", f.Get("vmid"), VzdumpDuration, head, func() (string, []string) { return status, rest }))
	case len(seg) == 3 && seg[0] == "nodes" && (seg[2] == "qemu" || seg[2] == "lxc") && m == "POST":
		id := atoi(f.Get("vmid"))
		if g := p.guest(id); g != nil {
			if !boolv(f.Get("force")) {
				apiErr(w, 500, "VM "+strconv.Itoa(id)+" already exists")
				return
			}
			if g.Status == "running" {
				apiErr(w, 500, "VM "+strconv.Itoa(id)+" is running - destroy failed")
				return
			}
		}
		typ := "qmrestore"
		if seg[2] == "lxc" {
			typ = "vzrestore"
		}
		node := seg[1]
		archive := f.Get("archive") + f.Get("ostemplate")
		upid := p.tasks.start(node, typ, f.Get("vmid"), RestoreDuration, []string{"restore " + archive}, func() (string, []string) {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.cfg[id] = backupConfig(archive, seg[2], id)
			if g := p.guest(id); g == nil {
				p.guests = append(p.guests, prox.Guest{VMID: id, Name: p.cfg[id]["name"] + p.cfg[id]["hostname"], Node: node, Type: seg[2], Status: "stopped"})
			}
			return "OK", []string{"restore done"}
		})
		data(w, upid)
	case path == "/cluster/tasks":
		out := []map[string]any{}
		for _, t := range p.tasks.recent(100) {
			o := map[string]any{"upid": t.UPID, "node": t.Node, "type": t.Type, "id": t.ID, "user": t.User, "starttime": t.Start}
			if t.End != 0 {
				o["endtime"], o["status"] = t.End, t.Status
			}
			out = append(out, o)
		}
		data(w, out)
	case len(seg) == 5 && seg[2] == "tasks" && seg[4] == "status":
		upid, _ := url.PathUnescape(seg[3])
		taskStatus(w, p.tasks.get(upid))
	case len(seg) == 5 && seg[2] == "tasks" && seg[4] == "log":
		upid, _ := url.PathUnescape(seg[3])
		taskLog(w, p.tasks.get(upid))
	case len(seg) == 4 && seg[2] == "hardware" && seg[3] == "usb":
		data(w, p.usb[seg[1]])
	case len(seg) == 4 && seg[0] == "nodes" && seg[2] == "disks" && seg[3] == "list":
		p.serveDisks(w, seg[1])
	case len(seg) == 4 && seg[0] == "nodes" && seg[2] == "disks" && seg[3] == "smart":
		p.serveSmart(w, seg[1], f.Get("disk"))
	case len(seg) == 3 && seg[0] == "nodes" && seg[2] == "network":
		data(w, []map[string]string{{"iface": "vmbr0", "type": "bridge"}, {"iface": "vmbr1", "type": "bridge"}})
	case len(seg) == 3 && seg[0] == "nodes" && seg[2] == "status":
		data(w, map[string]any{"memory": map[string]int64{"total": 64 * gib, "used": 22 * gib, "free": 42 * gib}, "cpuinfo": map[string]int{"cpus": 16}})
	case len(seg) == 5 && seg[0] == "nodes" && seg[2] == "storage" && seg[4] == "status":
		if seg[3] == "voll" {
			data(w, map[string]any{"storage": seg[3], "total": 100 * gib, "used": 98 * gib, "avail": 2 * gib, "active": 1})
			return
		}
		data(w, map[string]any{"storage": seg[3], "total": 900 * gib, "used": 300 * gib, "avail": 600 * gib, "active": 1})
	case len(seg) == 4 && seg[0] == "nodes" && (seg[2] == "qemu" || seg[2] == "lxc") && m == "DELETE":
		id := atoi(seg[3])
		g := p.guest(id)
		if g == nil {
			apiErr(w, 500, "Configuration file does not exist")
			return
		}
		if g.Status == "running" {
			apiErr(w, 500, "VM "+seg[3]+" is running - destroy failed")
			return
		}
		keep := p.guests[:0]
		for _, x := range p.guests {
			if x.VMID != id {
				keep = append(keep, x)
			}
		}
		p.guests = keep
		delete(p.cfg, id)
		data(w, p.tasks.start(seg[1], "qmdestroy", seg[3], time.Second, nil, nil))
	case len(seg) >= 5 && seg[0] == "nodes" && (seg[2] == "qemu" || seg[2] == "lxc"):
		p.guestOp(w, m, seg, f)
	default:
		apiErr(w, 404, "Method '"+m+" "+path+"' not implemented (demo)")
	}
}

func idSet(list string) map[string]bool {
	m := map[string]bool{}
	for _, x := range strings.Split(list, ",") {
		if x != "" {
			m[x] = true
		}
	}
	return m
}
