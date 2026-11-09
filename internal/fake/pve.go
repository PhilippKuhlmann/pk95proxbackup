package fake

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/prox"
)

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
	jobN                int
}

// NewPVE creates a cluster with the given nodes.
func NewPVE(nodes []string) *PVE {
	return &PVE{nodes: nodes, smbios: map[int]string{}, usbCfg: map[int]map[string]string{}, usb: map[string][]prox.USBDevice{}}
}

// AddGuest adds a VM (qemu) or container (lxc).
func (p *PVE) AddGuest(id int, name, node, typ, status string) {
	p.guests = append(p.guests, prox.Guest{VMID: id, Name: name, Node: node, Type: typ, Status: status, MaxDisk: 64 * gib, MaxMem: 4 * gib})
}

// SetSMBIOS sets the SMBIOS UUID of a VM.
func (p *PVE) SetSMBIOS(id int, uuid string) { p.smbios[id] = uuid }

// AddStorage adds a storage definition.
func (p *PVE) AddStorage(s prox.Storage) { p.storages = append(p.storages, s) }

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
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case path == "/version":
		data(w, map[string]string{"version": "9.0.10", "release": "9.0"})
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
		p.storages = append(p.storages, prox.Storage{Storage: f.Get("storage"), Type: f.Get("type"), Content: f.Get("content"), Server: f.Get("server"), Port: port,
			Datastore: f.Get("datastore"), Username: f.Get("username"), Fingerprint: f.Get("fingerprint"), Namespace: f.Get("namespace")})
		data(w, nil)
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
		lines := []string{"INFO: starting new backup job: vzdump " + strings.Join(ids, " ") + " --storage " + f.Get("storage")}
		for _, id := range ids {
			lines = append(lines, "INFO: Starting Backup of VM "+id, "INFO: Finished Backup of VM "+id)
		}
		data(w, p.tasks.start(seg[1], "vzdump", f.Get("vmid"), 8*time.Second, lines, nil))
	case len(seg) == 3 && seg[0] == "nodes" && (seg[2] == "qemu" || seg[2] == "lxc") && m == "POST":
		id := atoi(f.Get("vmid"))
		if p.guest(id) != nil {
			apiErr(w, 500, "VM "+strconv.Itoa(id)+" already exists")
			return
		}
		typ := "qmrestore"
		if seg[2] == "lxc" {
			typ = "vzrestore"
		}
		node := seg[1]
		upid := p.tasks.start(node, typ, f.Get("vmid"), 10*time.Second, []string{"restore " + f.Get("archive") + f.Get("ostemplate")}, func() (string, []string) {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.guests = append(p.guests, prox.Guest{VMID: id, Name: "restored-" + strconv.Itoa(id), Node: node, Type: seg[2], Status: "stopped"})
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
	case len(seg) == 5 && seg[2] == "qemu" && seg[4] == "config":
		id := atoi(seg[3])
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
		keys := []string{}
		for k := range cfg {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out[k] = cfg[k]
		}
		data(w, out)
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
