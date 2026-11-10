package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/prox"
)

// restoreSource is a datastore to restore from.
type restoreSource struct {
	Server     string `json:"server"`
	ServerName string `json:"server_name"`
	Store      string `json:"store"`
	Kind       string `json:"kind"` // datastore, usb, share
	Label      string `json:"label"`
	Comment    string `json:"comment,omitempty"`
	Mounted    bool   `json:"mounted"`
	Present    bool   `json:"present"` // usb: plugged in
	Total      int64  `json:"total"`
	Used       int64  `json:"used"`
	Error      string `json:"error,omitempty"`
}

// restoreSources lists every datastore of every Backup Server, USB disks with their labels.
func (s *Server) restoreSources(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	servers, _ := s.C.Servers()
	label := map[string]string{} // server/store → rotation disk label
	if rots, err := s.C.Rotations(); err == nil {
		for _, x := range rots {
			for _, d := range x.Disks {
				label[x.ServerID+"/"+d.Store] = d.Label
			}
		}
	}
	share := map[string]string{}
	if sh, err := s.C.Shares(); err == nil {
		for _, x := range sh {
			if x.Store != "" {
				share[x.ServerID+"/"+x.Store] = x.Name
			}
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	out := []restoreSource{}
	var errs []string
	for _, srv := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pbs := s.C.PBSClient(srv)
			cfgs, err := pbs.Datastores(ctx)
			if err != nil {
				mu.Lock()
				errs = append(errs, srv.Name+": "+err.Error())
				mu.Unlock()
				return
			}
			usage := map[string]prox.DatastoreUsage{}
			if us, err := pbs.Usage(ctx); err == nil {
				for _, u := range us {
					usage[u.Store] = u
				}
			}
			uuids := map[string]bool{}
			if disks, err := pbs.Disks(ctx); err == nil {
				for _, d := range disks {
					for _, p := range d.Partitions {
						if p.UUID != "" {
							uuids[strings.ToLower(p.UUID)] = true
						}
					}
				}
			}
			for _, c := range cfgs {
				u := usage[c.Name]
				src := restoreSource{Server: srv.ID, ServerName: srv.Name, Store: c.Name, Kind: "datastore", Label: c.Name, Comment: c.Comment,
					Mounted: u.MountStatus != "notmounted", Total: u.Total, Used: u.Used, Error: u.Error}
				src.Present = src.Mounted
				if c.BackingDevice != "" {
					src.Kind = "usb"
					src.Present = src.Mounted || uuids[strings.ToLower(c.BackingDevice)]
					if l := label[srv.ID+"/"+c.Name]; l != "" {
						src.Label = l
					}
				} else if n := share[srv.ID+"/"+c.Name]; n != "" {
					src.Kind, src.Label = "share", n
				}
				mu.Lock()
				out = append(out, src)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	order := map[string]int{"datastore": 0, "share": 1, "usb": 2}
	sort.Slice(out, func(i, j int) bool {
		if order[out[i].Kind] != order[out[j].Kind] {
			return order[out[i].Kind] < order[out[j].Kind]
		}
		if out[i].ServerName != out[j].ServerName {
			return out[i].ServerName < out[j].ServerName
		}
		return out[i].Label < out[j].Label
	})
	writeJSON(w, http.StatusOK, map[string]any{"sources": out, "errors": errs})
}

// sourceGuest is a VM or container with backups on one source.
type sourceGuest struct {
	Type        string `json:"type"` // qemu, lxc (like the guest list)
	VMID        int    `json:"vmid"`
	Name        string `json:"name"`
	Last        int64  `json:"last_backup"`
	Count       int    `json:"count"`
	Cluster     string `json:"cluster"`
	ClusterName string `json:"cluster_name"`
	Node        string `json:"node"`
	Status      string `json:"status"`
	Exists      bool   `json:"exists"` // still exists in Proxmox VE
}

// restoreSource lists the guests backed up on one datastore.
func (s *Server) restoreSourceGuests(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pbs, _, err := s.C.PBS(q.Get("pbs"))
	if err != nil {
		fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	groups, err := pbs.Groups(ctx, q.Get("store"))
	if err != nil {
		fail(w, err)
		return
	}
	// The guests of all clusters, to name them and to know where to restore to.
	clusters, _ := s.C.Clusters()
	type known struct {
		g  prox.Guest
		cl core.PVECluster
	}
	byKey := map[string]known{}
	for _, cl := range clusters {
		list, err := s.C.PVEClient(cl).Guests(ctx)
		if err != nil {
			continue
		}
		for _, g := range list {
			t := "vm"
			if g.Type == "lxc" {
				t = "ct"
			}
			k := t + "/" + strconv.Itoa(g.VMID)
			if _, ok := byKey[k]; !ok {
				byKey[k] = known{g, cl}
			}
		}
	}
	out := []sourceGuest{}
	for _, g := range groups {
		if g.Type != "vm" && g.Type != "ct" {
			continue
		}
		id, _ := strconv.Atoi(g.ID)
		sg := sourceGuest{Type: "qemu", VMID: id, Name: g.Comment, Last: g.LastBackup, Count: g.Count}
		if g.Type == "ct" {
			sg.Type = "lxc"
		}
		if k, ok := byKey[g.Type+"/"+g.ID]; ok {
			sg.Exists, sg.Name, sg.Cluster, sg.ClusterName, sg.Node, sg.Status = true, k.g.Name, k.cl.ID, k.cl.Name, k.g.Node, k.g.Status
		} else if len(clusters) > 0 {
			// Deleted meanwhile: restore into the first cluster.
			sg.Cluster, sg.ClusterName, sg.Status = clusters[0].ID, clusters[0].Name, "gelöscht"
		}
		out = append(out, sg)
	}
	s.nameFromBackups(ctx, pbs, q.Get("store"), out)
	sort.Slice(out, func(i, j int) bool { return out[i].VMID < out[j].VMID })
	writeJSON(w, http.StatusOK, map[string]any{"guests": out})
}

var (
	backupNameMu sync.Mutex
	backupNames  = map[string]string{} // store/type/id/time → name from the stored config
)

// nameFromBackups fills in the names of guests no connected Proxmox VE knows: from the notes
// of the newest snapshot, or from the guest configuration stored in it.
func (s *Server) nameFromBackups(ctx context.Context, pbs *prox.PBS, store string, guests []sourceGuest) {
	missing := 0
	for _, g := range guests {
		if g.Name == "" || !g.Exists {
			missing++
		}
	}
	if missing == 0 {
		return
	}
	snaps, err := pbs.Snapshots(ctx, store, "")
	if err != nil {
		return
	}
	newest := map[string]prox.Snapshot{}
	for _, sn := range snaps {
		k := sn.Type + "/" + sn.ID
		if sn.Time > newest[k].Time {
			newest[k] = sn
		}
	}
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i := range guests {
		g := &guests[i]
		if g.Exists && g.Name != "" {
			continue
		}
		typ, file := "vm", "qemu-server.conf.blob"
		if g.Type == "lxc" {
			typ, file = "ct", "pct.conf.blob"
		}
		sn, ok := newest[typ+"/"+strconv.Itoa(g.VMID)]
		if !ok {
			continue
		}
		if c := strings.TrimSpace(strings.SplitN(sn.Comment, "\n", 2)[0]); c != "" && g.Name == "" {
			g.Name = c
		}
		key := store + "/" + typ + "/" + sn.ID + "/" + strconv.FormatInt(sn.Time, 10)
		backupNameMu.Lock()
		name, cached := backupNames[key]
		backupNameMu.Unlock()
		if cached {
			if name != "" {
				g.Name = name
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			conf, err := pbs.SnapshotFile(ctx, store, "", typ, sn.ID, sn.Time, file)
			if err != nil {
				return
			}
			name := prox.GuestNameFromConfig(conf)
			backupNameMu.Lock()
			backupNames[key] = name
			backupNameMu.Unlock()
			if name != "" {
				g.Name = name
			}
		}()
	}
	wg.Wait()
}
