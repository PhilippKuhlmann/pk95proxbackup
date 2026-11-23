package core

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Deleting all backups of a guest (a VM or container that is gone or not wanted any more): where
// they lie on all Backup Servers and datastores, and removing them there. Protected backups stay
// (their protection is lifted on purpose, one by one); USB disks that are not plugged in are
// listed but can only be cleaned when they are.

// GuestBackupPlace is a datastore that holds backups of a guest.
type GuestBackupPlace struct {
	Server     string `json:"server"`
	ServerName string `json:"server_name"`
	Store      string `json:"store"`
	Kind       string `json:"kind"` // fixed, cloud, usb, archive
	Label      string `json:"label,omitempty"`
	Count      int    `json:"count"`
	Protected  int    `json:"protected"`
	Last       int64  `json:"last"`
	Mounted    bool   `json:"mounted"` // false: a disk that is not plugged in (known from its last read)
}

// GuestBackups is what the appliance knows about the backups of a guest.
type GuestBackups struct {
	Type   string             `json:"type"`
	ID     string             `json:"bid"`
	Name   string             `json:"name"`
	Places []GuestBackupPlace `json:"places"`
	InJob  []string           `json:"in_job"` // clusters whose backup jobs still save the guest
	Errors []string           `json:"errors"`
}

// checkGuestID accepts "vm" or "ct" and a plain VMID.
func checkGuestID(typ, id string) error {
	if typ != "vm" && typ != "ct" {
		return fmt.Errorf("unbekannter Typ „%s“", typ)
	}
	if n, err := strconv.Atoi(id); err != nil || n < 1 || strconv.Itoa(n) != id {
		return fmt.Errorf("„%s“ ist keine VM- oder Container-ID", id)
	}
	return nil
}

// FindGuestBackups lists the datastores with backups of a guest.
func (c *Core) FindGuestBackups(ctx context.Context, typ, id string) (GuestBackups, error) {
	if err := checkGuestID(typ, id); err != nil {
		return GuestBackups{}, err
	}
	out := GuestBackups{Type: typ, ID: id, Places: []GuestBackupPlace{}, InJob: []string{}, Errors: []string{}}
	rots, _ := c.Rotations()
	archives, _ := c.Archives()
	label := map[string][2]string{} // server|store → kind, label
	for _, r := range rots {
		for _, d := range r.Disks {
			label[usageKey(r.ServerID, d.Store)] = [2]string{"usb", r.Name + " · " + d.Label}
		}
	}
	for _, a := range archives {
		label[usageKey(a.ServerID, a.Store)] = [2]string{"archive", a.Name}
	}
	servers, _ := c.Servers()
	var mu sync.Mutex
	var wg sync.WaitGroup
	group := typ + "/" + id
	var newest int64
	for _, srv := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pbs := c.PBSClient(srv)
			usage, err := pbs.Usage(ctx)
			if err != nil {
				mu.Lock()
				out.Errors = append(out.Errors, srv.Name+": "+err.Error())
				mu.Unlock()
				return
			}
			cloud := map[string]bool{}
			if cfgs, err := pbs.Datastores(ctx); err == nil {
				for _, d := range cfgs {
					cloud[d.Name] = strings.Contains(d.Backend, "type=s3")
				}
			}
			for _, u := range usage {
				pl := GuestBackupPlace{Server: srv.ID, ServerName: srv.Name, Store: u.Store, Kind: "fixed", Mounted: u.MountStatus != "notmounted"}
				if l, ok := label[usageKey(srv.ID, u.Store)]; ok {
					pl.Kind, pl.Label = l[0], l[1]
				} else if cloud[u.Store] {
					pl.Kind = "cloud"
				}
				if !pl.Mounted {
					if g, ok := c.StoreContentCached(srv.ID, u.Store).Groups[group]; ok {
						pl.Count, pl.Last = g.Count, g.Last
					}
				} else {
					snaps, err := pbs.Snapshots(ctx, u.Store, "")
					if err != nil {
						mu.Lock()
						out.Errors = append(out.Errors, srv.Name+" / "+u.Store+": "+err.Error())
						mu.Unlock()
						continue
					}
					for _, sn := range snaps {
						if sn.Type != typ || sn.ID != id {
							continue
						}
						pl.Count++
						if sn.Protected {
							pl.Protected++
						}
						if sn.Time > pl.Last {
							pl.Last = sn.Time
						}
						mu.Lock()
						if sn.Comment != "" && sn.Time > newest {
							newest, out.Name = sn.Time, sn.Comment
						}
						mu.Unlock()
					}
				}
				if pl.Count > 0 {
					mu.Lock()
					out.Places = append(out.Places, pl)
					mu.Unlock()
				}
			}
		}()
	}
	// still in a backup job of a cluster?
	clusters, _ := c.Clusters()
	liveName := ""
	for _, cl := range clusters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pve := c.PVEClient(cl)
			guests, err := pve.Guests(ctx)
			if err != nil {
				return
			}
			for _, g := range guests {
				if strconv.Itoa(g.VMID) != id || (g.Type == "lxc") != (typ == "ct") {
					continue
				}
				nb, err := pve.NotBackedUp(ctx)
				if err != nil {
					return
				}
				in := true
				for _, x := range nb {
					if x.VMID == g.VMID {
						in = false
					}
				}
				mu.Lock()
				liveName = g.Name
				if in {
					out.InJob = append(out.InJob, cl.Name)
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if liveName != "" {
		out.Name = liveName
	}
	sort.Slice(out.Places, func(i, j int) bool {
		a, b := out.Places[i], out.Places[j]
		if a.ServerName != b.ServerName {
			return a.ServerName < b.ServerName
		}
		return a.Store < b.Store
	})
	sort.Strings(out.InJob)
	return out, nil
}

// GuestDeleteResult says what deleting the backups of a guest did.
type GuestDeleteResult struct {
	Deleted int      `json:"deleted"`
	Kept    []string `json:"kept"`   // protected backups left, per datastore
	Failed  []string `json:"failed"` // datastores that could not be cleaned
}

// DeleteGuestBackups removes the backups of a guest from the given datastores ("server/store").
// Protected backups stay.
func (c *Core) DeleteGuestBackups(ctx context.Context, typ, id string, places []string) (GuestDeleteResult, error) {
	res := GuestDeleteResult{Kept: []string{}, Failed: []string{}}
	if err := checkGuestID(typ, id); err != nil {
		return res, err
	}
	for _, p := range places {
		srvID, store, ok := strings.Cut(p, "/")
		if !ok || store == "" {
			res.Failed = append(res.Failed, p+": unbekannter Datastore")
			continue
		}
		pbs, srv, err := c.PBS(srvID)
		if err != nil {
			res.Failed = append(res.Failed, p+": "+err.Error())
			continue
		}
		where := srv.Name + " / " + store
		snaps, err := pbs.Snapshots(ctx, store, "")
		if err != nil {
			res.Failed = append(res.Failed, where+": "+err.Error())
			continue
		}
		var mine []int64
		protected := 0
		for _, sn := range snaps {
			if sn.Type == typ && sn.ID == id {
				if sn.Protected {
					protected++
				} else {
					mine = append(mine, sn.Time)
				}
			}
		}
		deleted := 0
		if protected == 0 && len(mine) > 0 {
			// the whole group at once (owner and notes go too)
			if err := pbs.DeleteGroup(ctx, store, "", typ, id); err != nil {
				res.Failed = append(res.Failed, where+": "+err.Error())
				continue
			}
			deleted = len(mine)
		} else {
			for _, t := range mine {
				if err := pbs.DeleteSnapshot(ctx, store, "", typ, id, t); err != nil {
					res.Failed = append(res.Failed, where+": "+err.Error())
					break
				}
				deleted++
			}
		}
		if protected > 0 {
			res.Kept = append(res.Kept, fmt.Sprintf("%s: %d geschützt", where, protected))
		}
		res.Deleted += deleted
		c.forgetSnapshots(srv.ID, store, typ, id, deleted)
		if groups, err := pbs.Groups(ctx, store); err == nil {
			c.SaveContent(srv.ID, store, groups)
		}
	}
	return res, nil
}
