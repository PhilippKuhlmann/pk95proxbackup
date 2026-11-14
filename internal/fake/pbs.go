package fake

import (
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/prox"
)

// SyncDuration and VerifyDuration are how long simulated copies and checks run (tests shorten them).
var (
	SyncDuration   = 20 * time.Second
	VerifyDuration = 15 * time.Second
)

type store struct {
	cfg     prox.DatastoreConfig
	total   int64
	mounted bool
	snaps   []prox.Snapshot
	// corrupt: files on the disk read back as garbage (copy and check fail).
	corrupt bool
	// badEnclosure: what is written only reads back wrong after mounting again.
	badEnclosure bool
	mountedOnce  bool
	// growth is how many bytes a day the store grew over the last year (usage history).
	growth int64
}

type job struct {
	kind   string
	params map[string]string
	groups []string // group-filter "group:vm/101" (one-off pulls)
	upid   string
	end    int64
	state  string
}

var (
	aclMu sync.Mutex
	acls  = map[string][]string{}
)

// ACLRoles returns the roles the simulated servers granted on path to authID.
func ACLRoles(path, authID string) []string {
	aclMu.Lock()
	defer aclMu.Unlock()
	return append([]string(nil), acls[path+"|"+authID]...)
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
	// foreign holds datastores on disks of other servers: "<uuid>/<path>" → snapshots.
	foreign map[string][]prox.Snapshot
	smart   map[string]map[string]any // disk → answer of /disks/smart
	blobs   map[string][]byte         // store/type/id/time/file → content
	// badDisks are disks in a broken USB enclosure (datastores made on them go bad).
	badDisks map[string]bool
	// busy are datastores the proxy holds open: unmount fails until the proxy is reloaded.
	busy     map[string]bool
	reloads  int
	apt      aptState
	remotePw map[string]string
}

type aptState struct {
	running, installed, candidate string
	kernel                        string   // running, e.g. "7.0.14-17"
	kernels                       []string // installed
	updates                       []prox.AptPackage
}

// SetPackages sets the simulated PBS version (running, installed, available), the running
// and installed kernels and the available updates.
func (p *PBS) SetPackages(running, installed, candidate, kernel string, kernels []string, updates []prox.AptPackage) {
	p.mu.Lock()
	p.apt = aptState{running: running, installed: installed, candidate: candidate, kernel: kernel, kernels: kernels, updates: updates}
	p.mu.Unlock()
}

// Reloads counts the reloads of proxmox-backup-proxy.
func (p *PBS) Reloads() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reloads
}

// PullDisk removes a disk as if it was pulled out; its datastore stays mounted (stale).
func (p *PBS) PullDisk(name string) (prox.Disk, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, d := range p.disks {
		if d.Name == name {
			p.disks = append(p.disks[:i], p.disks[i+1:]...)
			return d, true
		}
	}
	return prox.Disk{}, false
}

// PlugDisk puts a pulled disk back.
func (p *PBS) PlugDisk(d prox.Disk) {
	p.mu.Lock()
	p.disks = append(p.disks, d)
	p.mu.Unlock()
}

// SetBusy makes unmounting a datastore fail with "target is busy" until the proxy reloads.
func (p *PBS) SetBusy(st string, v bool) {
	p.mu.Lock()
	p.busy[st] = v
	p.mu.Unlock()
}

// SetCorrupt makes the files of a datastore unreadable (as after a broken write).
func (p *PBS) SetCorrupt(st string, v bool) {
	p.mu.Lock()
	if s := p.stores[st]; s != nil {
		s.corrupt = v
	}
	p.mu.Unlock()
}

// SetBadEnclosure puts a disk into a USB enclosure that corrupts what is written.
func (p *PBS) SetBadEnclosure(disk string, v bool) {
	p.mu.Lock()
	p.badDisks[disk] = v
	p.mu.Unlock()
}

// AddForeign puts a datastore of another server onto a partition (read in with reuse-datastore).
func (p *PBS) AddForeign(uuid, path string, snaps []prox.Snapshot) {
	p.foreign[strings.ToLower(uuid)+"/"+path] = snaps
}

// NewPBS creates an empty server.
func NewPBS(node string, root int64) *PBS {
	return &PBS{node: node, root: root, stores: map[string]*store{}, jobs: map[string]*job{}, remotes: map[string]prox.Remote{}, usedExtra: map[string]int64{}, users: map[string]bool{}, foreign: map[string][]prox.Snapshot{},
		smart: map[string]map[string]any{}, blobs: map[string][]byte{}, badDisks: map[string]bool{}, busy: map[string]bool{}, remotePw: map[string]string{}}
}

// AddStore adds a datastore (removable when uuid is set).
func (p *PBS) AddStore(name, path, uuid string, total int64, _ int64) {
	p.stores[name] = &store{cfg: prox.DatastoreConfig{Name: name, Path: path, BackingDevice: uuid}, total: total, mounted: uuid == ""}
}

// SetMounted sets the mount state of a removable store.
func (p *PBS) SetMounted(name string, m bool) { p.stores[name].mounted = m }

// AddDisk adds a disk with one partition (uuid) when used.
func (p *PBS) AddDisk(name, vendor, model, typ string, size int64, used, uuid string) {
	serial := strings.ToUpper(name) + "-0001"
	if vendor == "QEMU" { // Proxmox VE names virtual disks after their drive: drive-scsi1
		serial = "drive-scsi" + strconv.Itoa(int(name[len(name)-1]-'a'))
	}
	d := prox.Disk{Name: name, DevPath: "/dev/" + name, Size: size, Vendor: vendor, Model: model, DiskType: typ, Used: used, GPT: used != "unused", Serial: serial}
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

// SetGrowth sets how fast a datastore grew over the last year (usage history, bytes a day).
func (p *PBS) SetGrowth(st string, perDay int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stores[st].growth = perDay
}

// SetSnapshotKey marks the snapshots of a datastore as encrypted with the key fp (for tests).
func (p *PBS) SetSnapshotKey(st, fp string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.stores[st].snaps {
		p.stores[st].snaps[i].Fingerprint = fp
	}
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
	typ := "syncjob"
	if j.params["id"] == "" {
		typ = "sync"
	}
	return p.tasks.start(p.node, typ, remote+":"+j.params["remote-store"]+":"+target+"::"+j.params["id"], SyncDuration,
		[]string{"Starting datastore sync job '" + j.params["id"] + "'", "sync datastore '" + target + "' from '" + j.params["remote-store"] + "'"},
		func() (string, []string) {
			p.mu.Lock()
			defer p.mu.Unlock()
			dst := p.stores[target]
			if dst == nil || !dst.mounted {
				j.end, j.state = time.Now().Unix(), "datastore '"+target+"' is not mounted"
				return j.state, nil
			}
			if dst.corrupt {
				j.end, j.state = time.Now().Unix(), "sync failed with some errors."
				var lines []string
				for _, g := range []string{"vm/100", "vm/101", "ct/110"} {
					lines = append(lines, time.Now().Format(time.RFC3339)+": ["+g+"]: sync group "+g+" failed - group lock failed: unable to read \"/mnt/datastore/"+target+"/"+g+"/owner\" - stream did not contain valid UTF-8",
						time.Now().Format(time.RFC3339)+": ["+g+"]: create_locked_backup_group failed")
				}
				return j.state, lines
			}
			n := 0
			var bytes int64
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
				only := map[string]bool{}
				for _, g := range j.groups {
					only[strings.TrimPrefix(g, "group:")] = true
				}
				for _, s := range src.snaps {
					if len(keep) > 0 && !keep[s.Type+"/"+s.ID+strconv.FormatInt(s.Time, 10)] {
						continue
					}
					if len(only) > 0 && !only[s.Type+"/"+s.ID] {
						continue
					}
					if !have[s.Type+s.ID+strconv.FormatInt(s.Time, 10)] {
						dst.snaps = append(dst.snaps, s)
						n++
						bytes += s.Size / 3
					}
				}
			}
			j.end, j.state = time.Now().Unix(), "OK"
			summary := "Summary: sync job found no new data to pull"
			if n > 0 {
				summary = "Summary: sync job pulled " + strconv.FormatFloat(float64(bytes)/float64(gib), 'f', 2, 64) + " GiB in " + strconv.Itoa(n*4096) + " chunks (average rate: 96.106 MiB/s)"
			}
			return "OK", []string{"sync group … done", "Finished syncing " + strconv.Itoa(n) + " snapshots", summary}
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
		data(w, map[string]any{"current-kernel": map[string]string{"release": p.apt.kernel + "-pve"}, "cpu": 0.07, "uptime": 864000, "memory": map[string]int64{"total": 8 * gib, "used": 3 * gib}, "root": map[string]int64{"total": p.root, "used": 9 * gib}})
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
		st := &store{cfg: prox.DatastoreConfig{Name: name, Path: f.Get("path"), Comment: f.Get("comment"), GCSchedule: f.Get("gc-schedule"), BackingDevice: f.Get("backing-device")}, total: 4000 * gib, mounted: f.Get("backing-device") == ""}
		if boolv(f.Get("reuse-datastore")) {
			snaps, ok := p.foreign[strings.ToLower(f.Get("backing-device"))+"/"+f.Get("path")]
			if !ok {
				apiErr(w, 400, "datastore path '"+f.Get("path")+"' is not a valid datastore (missing .chunks)")
				return
			}
			st.snaps, st.total = append([]prox.Snapshot(nil), snaps...), 932*gib
		}
		p.stores[name] = st
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
			if s.badEnclosure && s.mountedOnce {
				s.corrupt = true
			}
			s.mountedOnce = true
			upid := p.tasks.start(p.node, "mount-device", seg[2], time.Second, nil, nil)
			for _, j := range p.jobs {
				if j.kind == "sync" && j.params["store"] == seg[2] && boolv(j.params["run-on-mount"]) {
					j.upid, j.end, j.state = p.runSync(j), 0, ""
				}
			}
			data(w, upid)
		case "unmount":
			if strings.Contains(s.cfg.Maintenance, "unmount") {
				apiErr(w, 400, "datastore is already being unmounted")
				return
			}
			if p.busy[seg[2]] {
				// Like PBS: the maintenance mode stays behind when umount fails.
				s.cfg.Maintenance = "unmount"
				data(w, p.tasks.start(p.node, "unmount-device", seg[2], time.Second, nil, func() (string, []string) {
					return `command "umount" "/mnt/datastore/` + seg[2] + `" failed - status code: 32 - umount: /mnt/datastore/` + seg[2] + `: target is busy.`, nil
				}))
				return
			}
			s.mounted = s.cfg.BackingDevice == ""
			data(w, p.tasks.start(p.node, "unmount-device", seg[2], 2*time.Second, nil, nil))
		case "rrd":
			// a year of history, a value every six hours: grown by s.growth a day up to now
			out := []map[string]any{}
			if s.mounted && s.cfg.BackingDevice == "" {
				used := p.used(s) + p.usedExtra[s.cfg.Name]
				g := s.growth
				if g == 0 {
					g = s.total / 1500
				}
				now := time.Now().Unix() / 21600 * 21600
				for i := 1439; i >= 0; i-- {
					t := now - int64(i)*21600
					u := used - g*int64(i)/4 - (int64(i)%28)*g/8 // grows, freed again each week by the GC
					if i > 1200 {
						out = append(out, map[string]any{"time": t})
						continue
					}
					out = append(out, map[string]any{"time": t, "total": float64(s.total), "used": float64(max(u, s.total/20))})
				}
			}
			data(w, out)
		case "gc":
			if m == "GET" {
				used := p.used(s) + p.usedExtra[s.cfg.Name]
				data(w, map[string]any{"index-data-bytes": used * 4, "disk-bytes": used, "pending-bytes": used / 40, "removed-bytes": used / 100})
				return
			}
			data(w, p.tasks.start(p.node, "garbage_collection", seg[2], 12*time.Second, []string{"starting garbage collection on store " + seg[2], "Removed garbage: 1.2 GiB"}, nil))
		case "verify":
			bad := s.corrupt
			data(w, p.tasks.start(p.node, "verify", seg[2], VerifyDuration, []string{"verify datastore " + seg[2]}, func() (string, []string) {
				if bad {
					return "verification failed - please check the log for details", []string{"can't verify chunk, load failed - Bad message (os error 74)", "corrupt chunk renamed"}
				}
				return "OK", nil
			}))
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
		case "protected":
			t, _ := strconv.ParseInt(f.Get("backup-time"), 10, 64)
			for i := range s.snaps {
				if sn := s.snaps[i]; sn.Type == f.Get("backup-type") && sn.ID == f.Get("backup-id") && sn.Time == t {
					s.snaps[i].Protected = boolv(f.Get("protected"))
					data(w, nil)
					return
				}
			}
			apiErr(w, 400, "backup snapshot does not exist")
		case "snapshots":
			if m == "DELETE" {
				t, _ := strconv.ParseInt(f.Get("backup-time"), 10, 64)
				keep := s.snaps[:0]
				for _, sn := range s.snaps {
					if sn.Type == f.Get("backup-type") && sn.ID == f.Get("backup-id") && sn.Time == t && sn.Protected {
						apiErr(w, 400, "cannot remove protected snapshot")
						return
					}
				}
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
		case "download-decoded":
			if b, ok := p.blob(seg[2], f.Get("backup-type"), f.Get("backup-id"), f.Get("backup-time"), f.Get("file-name")); ok {
				w.Header().Set("Content-Type", "application/octet-stream")
				_, _ = w.Write(b)
				return
			}
			// the guest configuration stored with a backup
			name := map[string]string{"250": "alt-fileserver", "251": "alter-mailserver", "200": "erp", "201": "mailserver", "210": "webshop"}[f.Get("backup-id")]
			if name == "" {
				name = "gast-" + f.Get("backup-id")
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			switch f.Get("file-name") {
			case "qemu-server.conf.blob":
				_, _ = w.Write([]byte("boot: order=scsi0\ncores: 2\nmemory: 4096\nname: " + name + "\nscsi0: local-lvm:vm-" + f.Get("backup-id") + "-disk-0,size=32G\n\n[vor-update]\nname: alt\n"))
			case "pct.conf.blob":
				_, _ = w.Write([]byte("arch: amd64\ncores: 1\nhostname: " + name + "\nmemory: 512\n"))
			default:
				apiErr(w, 400, "no such file")
			}
		case "groups":
			if !s.mounted {
				apiErr(w, 400, "datastore '"+seg[2]+"' is not mounted")
				return
			}
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
	case path == "/pull" && m == "POST":
		// One-off sync without a job (PBS names the worker like a job without id).
		params := map[string]string{}
		for k := range f {
			params[k] = f.Get(k)
		}
		if p.stores[params["store"]] == nil {
			apiErr(w, 400, "no such datastore '"+params["store"]+"'")
			return
		}
		j := &job{kind: "sync", params: params, groups: f["group-filter"]}
		data(w, p.runSync(j))
	case path == "/config/remote" && m == "GET":
		out := []prox.Remote{}
		for _, r := range p.remotes {
			out = append(out, r)
		}
		data(w, out)
	case path == "/config/remote" && m == "POST":
		p.remotes[f.Get("name")] = prox.Remote{Name: f.Get("name"), Host: f.Get("host"), Port: atoi(f.Get("port")), AuthID: f.Get("auth-id"), Fingerprint: f.Get("fingerprint"), Comment: f.Get("comment")}
		p.remotePw[f.Get("name")] = f.Get("password")
		data(w, nil)
	case len(seg) == 3 && seg[0] == "config" && seg[1] == "remote" && m == "PUT":
		if _, ok := p.remotes[seg[2]]; !ok {
			apiErr(w, 404, "remote does not exist")
			return
		}
		p.remotes[seg[2]] = prox.Remote{Name: seg[2], Host: f.Get("host"), Port: atoi(f.Get("port")), AuthID: f.Get("auth-id"), Fingerprint: f.Get("fingerprint"), Comment: f.Get("comment")}
		p.remotePw[seg[2]] = f.Get("password")
		data(w, nil)
	case len(seg) == 4 && seg[0] == "config" && seg[1] == "remote" && seg[3] == "scan":
		// Like PBS: this server connects to the remote and lists its datastores.
		rm, ok := p.remotes[seg[2]]
		if !ok {
			apiErr(w, 404, "remote does not exist")
			return
		}
		stores, err := scanRemote(rm, p.remotePw[seg[2]])
		if err != nil {
			apiErr(w, 400, err.Error())
			return
		}
		data(w, stores)
	case len(seg) == 5 && seg[0] == "nodes" && seg[2] == "services" && seg[4] == "reload" && m == "POST":
		if seg[3] == "proxmox-backup-proxy" {
			p.reloads++
			p.busy = map[string]bool{}
		}
		if p.apt.installed != "" {
			p.apt.running = strings.TrimSuffix(p.apt.installed, "-1")
		}
		data(w, p.tasks.start(p.node, "srvreload", seg[3], time.Second, nil, nil))
	case path == "/nodes/localhost/apt/versions":
		inst, cand := p.apt.installed, p.apt.candidate
		if cand == "" {
			cand = inst
		}
		out := []prox.AptPackage{
			{Package: "proxmox-backup", Version: "4.2.0", OldVersion: "4.2.0", ExtraInfo: "running kernel: " + p.apt.kernel},
			{Package: "proxmox-backup-server", Version: cand, OldVersion: inst, ExtraInfo: "running version: " + p.apt.running},
		}
		for _, k := range p.apt.kernels {
			out = append(out, prox.AptPackage{Package: "proxmox-kernel-" + k + "-pve-signed", Version: k, OldVersion: k})
		}
		data(w, out)
	case path == "/nodes/localhost/apt/update":
		data(w, p.apt.updates)
	case path == "/nodes/localhost/disks/smart":
		p.serveSmart(w, f.Get("disk"))
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
			p.stores[name] = &store{cfg: prox.DatastoreConfig{Name: name, Path: "/mnt/datastore/" + name, BackingDevice: dev}, total: 1800 * gib, mounted: dev == "", badEnclosure: p.badDisks[disk]}
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
	case path == "/access/acl" && m == "GET":
		aclMu.Lock()
		out := []prox.ACL{}
		for k, roles := range acls {
			pth, id, _ := strings.Cut(k, "|")
			for _, r := range roles {
				out = append(out, prox.ACL{Path: pth, UGID: id, UGIDType: "user", Role: r, Propagate: true})
			}
		}
		aclMu.Unlock()
		data(w, out)
	case path == "/access/acl":
		aclMu.Lock()
		k := f.Get("path") + "|" + f.Get("auth-id")
		if boolv(f.Get("delete")) {
			keep := []string{}
			for _, r := range acls[k] {
				if r != f.Get("role") {
					keep = append(keep, r)
				}
			}
			acls[k] = keep
		} else if !slices.Contains(acls[k], f.Get("role")) {
			acls[k] = append(acls[k], f.Get("role"))
		}
		aclMu.Unlock()
		data(w, nil)
	default:
		apiErr(w, 404, "Path '"+path+"' not found (demo)")
	}
}
