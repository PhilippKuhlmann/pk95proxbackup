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

type store struct {
	cfg     prox.DatastoreConfig
	total   int64
	mounted bool
	snaps   []prox.Snapshot
}

type job struct {
	kind   string
	params map[string]string
	upid   string
	end    int64
	state  string
}

// PBS is a simulated Proxmox Backup Server.
type PBS struct {
	mu        sync.Mutex
	node      string
	root      int64
	stores    map[string]*store
	disks     []prox.Disk
	jobs      map[string]*job
	remotes   map[string]prox.Remote
	tasks     tasks
	usedExtra map[string]int64
	users     map[string]bool
}

// NewPBS creates an empty server.
func NewPBS(node string, root int64) *PBS {
	return &PBS{node: node, root: root, stores: map[string]*store{}, jobs: map[string]*job{}, remotes: map[string]prox.Remote{}, usedExtra: map[string]int64{}, users: map[string]bool{}}
}

// AddStore adds a datastore (removable when uuid is set).
func (p *PBS) AddStore(name, path, uuid string, total int64, _ int64) {
	p.stores[name] = &store{cfg: prox.DatastoreConfig{Name: name, Path: path, BackingDevice: uuid}, total: total, mounted: uuid == ""}
}

// SetMounted sets the mount state of a removable store.
func (p *PBS) SetMounted(name string, m bool) { p.stores[name].mounted = m }

// AddDisk adds a disk with one partition (uuid) when used.
func (p *PBS) AddDisk(name, vendor, model, typ string, size int64, used, uuid string) {
	d := prox.Disk{Name: name, DevPath: "/dev/" + name, Size: size, Vendor: vendor, Model: model, DiskType: typ, Used: used, GPT: used != "unused", Serial: strings.ToUpper(name) + "-0001"}
	if used != "unused" {
		d.Partitions = []prox.Partition{{Name: name + "1", DevPath: "/dev/" + name + "1", Size: size, Filesystem: "ext4", UUID: uuid, Used: "filesystem"}}
		d.Used = "partitions"
	}
	p.disks = append(p.disks, d)
}

// AddSnapshot adds a backup snapshot.
func (p *PBS) AddSnapshot(st, typ, id string, t, size int64) {
	s := p.stores[st]
	s.snaps = append(s.snaps, prox.Snapshot{Type: typ, ID: id, Time: t, Size: size, Owner: "pk95-pve@pbs!pve-cluster", Verification: &struct {
		State string `json:"state"`
	}{State: "ok"}})
}

func (p *PBS) snapshotsOf(st string) []prox.Snapshot {
	return append([]prox.Snapshot(nil), p.stores[st].snaps...)
}

// AddJob adds a job with a past run.
func (p *PBS) AddJob(kind string, params map[string]string, end int64, state string) {
	p.jobs[kind+"/"+params["id"]] = &job{kind: kind, params: params, end: end, state: state, upid: "UPID:" + p.node + ":seed:" + params["id"]}
}

func (p *PBS) updateDatastore(name string, f url.Values) {
	s := p.stores[name]
	if s == nil {
		return
	}
	for _, d := range f["delete"] {
		for _, k := range strings.Split(d, ",") {
			switch k {
			case "gc-schedule":
				s.cfg.GCSchedule = ""
			case "comment":
				s.cfg.Comment = ""
			case "maintenance-mode":
				s.cfg.Maintenance = ""
			}
		}
	}
	if v, ok := f["gc-schedule"]; ok && v[0] != "" {
		s.cfg.GCSchedule = v[0]
	}
	if v, ok := f["comment"]; ok && v[0] != "" {
		s.cfg.Comment = v[0]
	}
	if v, ok := f["maintenance-mode"]; ok {
		s.cfg.Maintenance = v[0]
	}
}

func (p *PBS) used(s *store) int64 {
	// Deduplication: the newest snapshot of each group counts fully, older ones 4 %.
	newest := map[string]int64{}
	var sum int64
	for _, sn := range s.snaps {
		k := sn.Type + "/" + sn.ID
		if sn.Time > newest[k] {
			newest[k] = sn.Time
		}
	}
	for _, sn := range s.snaps {
		if newest[sn.Type+"/"+sn.ID] == sn.Time {
			sum += sn.Size * 55 / 100
		} else {
			sum += sn.Size * 3 / 100
		}
	}
	return sum + 2*gib
}

func (p *PBS) runSync(j *job) string {
	target := j.params["store"]
	remote := j.params["remote"]
	if remote == "" {
		remote = `\x2d`
	}
	return p.tasks.start(p.node, "syncjob", remote+":"+j.params["remote-store"]+":"+target+"::"+j.params["id"], 20*time.Second,
		[]string{"Starting datastore sync job '" + j.params["id"] + "'", "sync datastore '" + target + "' from '" + j.params["remote-store"] + "'"},
		func() (string, []string) {
			p.mu.Lock()
			defer p.mu.Unlock()
			dst := p.stores[target]
			if dst == nil || !dst.mounted {
				j.end, j.state = time.Now().Unix(), "datastore '"+target+"' is not mounted"
				return j.state, nil
			}
			n := 0
			if src := p.stores[j.params["remote-store"]]; src != nil {
				have := map[string]bool{}
				for _, s := range dst.snaps {
					have[s.Type+s.ID+strconv.FormatInt(s.Time, 10)] = true
				}
				// transfer-last: only the newest N snapshots of each group
				keep := map[string]bool{}
				if n := atoi(j.params["transfer-last"]); n > 0 {
					byGroup := map[string][]int64{}
					for _, s := range src.snaps {
						byGroup[s.Type+"/"+s.ID] = append(byGroup[s.Type+"/"+s.ID], s.Time)
					}
					for g, ts := range byGroup {
						sort.Slice(ts, func(a, b int) bool { return ts[a] > ts[b] })
						for i := 0; i < n && i < len(ts); i++ {
							keep[g+strconv.FormatInt(ts[i], 10)] = true
						}
					}
				}
				for _, s := range src.snaps {
					if len(keep) > 0 && !keep[s.Type+"/"+s.ID+strconv.FormatInt(s.Time, 10)] {
						continue
					}
					if !have[s.Type+s.ID+strconv.FormatInt(s.Time, 10)] {
						dst.snaps = append(dst.snaps, s)
						n++
					}
				}
			}
			j.end, j.state = time.Now().Unix(), "OK"
			return "OK", []string{"sync group … done", "Finished syncing " + strconv.Itoa(n) + " snapshots"}
		})
}

func (p *PBS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api2/json")
	if !strings.HasPrefix(r.Header.Get("Authorization"), "PBSAPIToken=") {
		apiErr(w, 401, "authentication failed")
		return
	}
	seg := strings.Split(strings.Trim(path, "/"), "/")
	f := form(r)
	p.mu.Lock()
	defer p.mu.Unlock()
	m := r.Method
	switch {
	case path == "/version":
		data(w, map[string]string{"version": "4.0", "release": "18", "repoid": "demo"})
	case path == "/nodes/localhost/status":
		data(w, map[string]any{"cpu": 0.07, "uptime": 864000, "memory": map[string]int64{"total": 8 * gib, "used": 3 * gib}, "root": map[string]int64{"total": p.root, "used": 9 * gib}})
	case path == "/config/datastore" && m == "GET":
		out := []prox.DatastoreConfig{}
		for _, s := range p.stores {
			out = append(out, s.cfg)
		}
		data(w, out)
	case path == "/config/datastore" && m == "POST":
		name := f.Get("name")
		if p.stores[name] != nil {
			apiErr(w, 400, "datastore '"+name+"' already exists.")
			return
		}
		p.stores[name] = &store{cfg: prox.DatastoreConfig{Name: name, Path: f.Get("path"), Comment: f.Get("comment"), GCSchedule: f.Get("gc-schedule")}, total: 4000 * gib, mounted: true}
		data(w, nil)
	case len(seg) == 3 && seg[0] == "config" && seg[1] == "datastore":
		if p.stores[seg[2]] == nil {
			apiErr(w, 404, "no such datastore")
			return
		}
		if m == "DELETE" {
			delete(p.stores, seg[2])
			data(w, p.tasks.start(p.node, "delete-datastore", seg[2], time.Second, nil, nil))
			return
		}
		p.updateDatastore(seg[2], f)
		data(w, nil)
	case path == "/status/datastore-usage":
		out := []prox.DatastoreUsage{}
		for _, s := range p.stores {
			u := prox.DatastoreUsage{Store: s.cfg.Name, MountStatus: "nonremovable"}
			if s.cfg.BackingDevice != "" {
				u.MountStatus = "notmounted"
				if s.mounted {
					u.MountStatus = "mounted"
				}
			}
			if s.mounted {
				u.Total, u.Used = s.total, p.used(s)+p.usedExtra[s.cfg.Name]
				u.Avail = u.Total - u.Used
			}
			out = append(out, u)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Store < out[j].Store })
		data(w, out)
	case len(seg) >= 4 && seg[0] == "admin" && seg[1] == "datastore":
		s := p.stores[seg[2]]
		if s == nil {
			apiErr(w, 404, "no such datastore '"+seg[2]+"'")
			return
		}
		switch seg[3] {
		case "mount":
			uuidPresent := false
			for _, d := range p.disks {
				for _, pt := range d.Partitions {
					if strings.EqualFold(pt.UUID, s.cfg.BackingDevice) {
						uuidPresent = true
					}
				}
			}
			if !uuidPresent {
				apiErr(w, 400, "device for datastore '"+seg[2]+"' is not present")
				return
			}
			s.mounted = true
			upid := p.tasks.start(p.node, "mount-device", seg[2], time.Second, nil, nil)
			for _, j := range p.jobs {
				if j.kind == "sync" && j.params["store"] == seg[2] && boolv(j.params["run-on-mount"]) {
					j.upid, j.end, j.state = p.runSync(j), 0, ""
				}
			}
			data(w, upid)
		case "unmount":
			s.mounted = s.cfg.BackingDevice == ""
			data(w, p.tasks.start(p.node, "unmount-device", seg[2], 2*time.Second, nil, nil))
		case "gc":
			if m == "GET" {
				data(w, map[string]any{})
				return
			}
			data(w, p.tasks.start(p.node, "garbage_collection", seg[2], 12*time.Second, []string{"starting garbage collection on store " + seg[2], "Removed garbage: 1.2 GiB"}, nil))
		case "verify":
			data(w, p.tasks.start(p.node, "verify", seg[2], 15*time.Second, []string{"verify datastore " + seg[2]}, nil))
		case "prune-datastore":
			// keep-last only (enough for the demo and the tests)
			if n := atoi(f.Get("keep-last")); n > 0 && f.Get("keep-daily")+f.Get("keep-weekly")+f.Get("keep-monthly") == "" {
				byGroup := map[string][]prox.Snapshot{}
				for _, sn := range s.snaps {
					byGroup[sn.Type+"/"+sn.ID] = append(byGroup[sn.Type+"/"+sn.ID], sn)
				}
				s.snaps = nil
				for _, list := range byGroup {
					sort.Slice(list, func(a, b int) bool { return list[a].Time > list[b].Time })
					if len(list) > n {
						list = list[:n]
					}
					s.snaps = append(s.snaps, list...)
				}
			}
			data(w, p.tasks.start(p.node, "prune", seg[2], 2*time.Second, []string{"prune datastore " + seg[2]}, nil))
		case "snapshots":
			if m == "DELETE" {
				t, _ := strconv.ParseInt(f.Get("backup-time"), 10, 64)
				keep := s.snaps[:0]
				for _, sn := range s.snaps {
					if !(sn.Type == f.Get("backup-type") && sn.ID == f.Get("backup-id") && sn.Time == t) {
						keep = append(keep, sn)
					}
				}
				s.snaps = keep
				data(w, nil)
				return
			}
			if !s.mounted {
				apiErr(w, 400, "datastore is not mounted")
				return
			}
			data(w, s.snaps)
		case "groups":
			groups := map[string]*prox.Group{}
			for _, sn := range s.snaps {
				k := sn.Type + "/" + sn.ID
				g := groups[k]
				if g == nil {
					g = &prox.Group{Type: sn.Type, ID: sn.ID, Owner: sn.Owner}
					groups[k] = g
				}
				g.Count++
				if sn.Time > g.LastBackup {
					g.LastBackup = sn.Time
				}
			}
			out := []prox.Group{}
			for _, g := range groups {
				out = append(out, *g)
			}
			data(w, out)
		default:
			apiErr(w, 404, "not implemented")
		}
	case len(seg) == 2 && seg[0] == "admin" && (seg[1] == "sync" || seg[1] == "prune" || seg[1] == "verify"):
		out := []map[string]any{}
		for _, j := range p.jobs {
			if j.kind != seg[1] {
				continue
			}
			o := map[string]any{}
			for k, v := range j.params {
				if n, err := strconv.Atoi(v); err == nil && (strings.HasPrefix(k, "keep-") || k == "transfer-last") {
					o[k] = n
				} else if k == "run-on-mount" || k == "remove-vanished" {
					o[k] = boolv(v)
				} else {
					o[k] = v
				}
			}
			if j.upid != "" {
				o["last-run-upid"] = j.upid
			}
			if j.end != 0 {
				o["last-run-endtime"] = j.end
				o["last-run-state"] = j.state
			}
			if j.params["schedule"] != "" {
				o["next-run"] = time.Now().Add(6 * time.Hour).Unix()
			}
			out = append(out, o)
		}
		sort.Slice(out, func(i, k int) bool { return out[i]["id"].(string) < out[k]["id"].(string) })
		data(w, out)
	case len(seg) == 4 && seg[0] == "admin" && seg[3] == "run":
		j := p.jobs[seg[1]+"/"+seg[2]]
		if j == nil {
			apiErr(w, 404, "no such job")
			return
		}
		if seg[1] == "sync" {
			j.upid, j.end, j.state = p.runSync(j), 0, ""
		} else {
			j.upid = p.tasks.start(p.node, seg[1]+"job", seg[2], 3*time.Second, nil, nil)
			j.end, j.state = time.Now().Add(3*time.Second).Unix(), "OK"
		}
		data(w, nil)
	case len(seg) == 2 && seg[0] == "config" && (seg[1] == "sync" || seg[1] == "prune" || seg[1] == "verify") && m == "POST":
		id := f.Get("id")
		if p.jobs[seg[1]+"/"+id] != nil {
			apiErr(w, 400, "job '"+id+"' already exists")
			return
		}
		params := map[string]string{}
		for k := range f {
			params[k] = f.Get(k)
		}
		p.jobs[seg[1]+"/"+id] = &job{kind: seg[1], params: params}
		data(w, nil)
	case len(seg) == 3 && seg[0] == "config" && (seg[1] == "sync" || seg[1] == "prune" || seg[1] == "verify"):
		j := p.jobs[seg[1]+"/"+seg[2]]
		if j == nil {
			apiErr(w, 404, "job '"+seg[2]+"' does not exist")
			return
		}
		if m == "DELETE" {
			delete(p.jobs, seg[1]+"/"+seg[2])
			data(w, nil)
			return
		}
		for _, d := range f["delete"] {
			for _, k := range strings.Split(d, ",") {
				delete(j.params, k)
			}
		}
		for k := range f {
			if k != "delete" {
				j.params[k] = f.Get(k)
			}
		}
		data(w, nil)
	case path == "/config/remote" && m == "GET":
		out := []prox.Remote{}
		for _, r := range p.remotes {
			out = append(out, r)
		}
		data(w, out)
	case path == "/config/remote" && m == "POST":
		p.remotes[f.Get("name")] = prox.Remote{Name: f.Get("name"), Host: f.Get("host"), AuthID: f.Get("auth-id"), Fingerprint: f.Get("fingerprint"), Comment: f.Get("comment")}
		data(w, nil)
	case len(seg) == 3 && seg[0] == "config" && seg[1] == "remote" && m == "PUT":
		if _, ok := p.remotes[seg[2]]; !ok {
			apiErr(w, 404, "remote does not exist")
			return
		}
		p.remotes[seg[2]] = prox.Remote{Name: seg[2], Host: f.Get("host"), AuthID: f.Get("auth-id"), Fingerprint: f.Get("fingerprint"), Comment: f.Get("comment")}
		data(w, nil)
	case path == "/nodes/localhost/disks/list":
		data(w, p.disks)
	case path == "/nodes/localhost/disks/wipedisk":
		for i := range p.disks {
			if p.disks[i].Name == f.Get("disk") {
				p.disks[i].Used, p.disks[i].Partitions, p.disks[i].GPT = "unused", nil, false
			}
		}
		data(w, p.tasks.start(p.node, "wipedisk", f.Get("disk"), time.Second, nil, nil))
	case path == "/nodes/localhost/disks/directory" && m == "POST":
		disk, name := f.Get("disk"), f.Get("name")
		uuid := "a1b2c3d4-0000-4000-8000-" + strconv.FormatInt(time.Now().UnixNano()%1e12, 10)
		for len(uuid) < 36 {
			uuid += "0"
		}
		for i := range p.disks {
			if p.disks[i].Name == disk {
				p.disks[i].Used = "partitions"
				p.disks[i].Partitions = []prox.Partition{{Name: disk + "1", DevPath: "/dev/" + disk + "1", Size: p.disks[i].Size, Filesystem: f.Get("filesystem"), UUID: uuid, Used: "filesystem"}}
			}
		}
		if boolv(f.Get("add-datastore")) {
			dev := ""
			if boolv(f.Get("removable-datastore")) {
				dev = uuid
			}
			p.stores[name] = &store{cfg: prox.DatastoreConfig{Name: name, Path: "/mnt/datastore/" + name, BackingDevice: dev}, total: 1800 * gib, mounted: dev == ""}
		}
		data(w, p.tasks.start(p.node, "dircreate", name, 3*time.Second, []string{"create datastore '" + name + "' on disk " + disk}, nil))
	case path == "/nodes/localhost/tasks":
		out := []map[string]any{}
		for _, t := range p.tasks.recent(max(atoi(f.Get("limit")), 50)) {
			if boolv(f.Get("running")) && t.End != 0 {
				continue
			}
			o := map[string]any{"upid": t.UPID, "node": t.Node, "worker_type": t.Type, "worker_id": t.ID, "user": t.User, "starttime": t.Start}
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
	case path == "/access/users" && m == "POST":
		if p.users[f.Get("userid")] {
			apiErr(w, 400, "user already exists")
			return
		}
		p.users[f.Get("userid")] = true
		data(w, nil)
	case len(seg) == 3 && seg[0] == "access" && seg[1] == "users":
		data(w, map[string]any{"userid": seg[2]})
	case len(seg) == 5 && seg[0] == "access" && seg[3] == "token":
		if m == "DELETE" {
			data(w, nil)
			return
		}
		data(w, map[string]string{"tokenid": seg[2] + "!" + seg[4], "value": "00000000-demo-secret"})
	case path == "/access/acl":
		data(w, nil)
	default:
		apiErr(w, 404, "Path '"+path+"' not found (demo)")
	}
}
