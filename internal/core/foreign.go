package core

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/hostexec"
	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
)

// Foreign disks and stale mounts on Backup Servers with rotation sets.
//
// A USB disk can be plugged in whose filesystem belongs to no datastore – a new disk, a disk
// from elsewhere, or a disk of a set whose datastore no longer matches it (reformatted
// outside, swapped enclosure). The Backup Server then ignores it and the set shows the disk as
// "not plugged in". The appliance lists such disks so they can be added or taken over as a
// disk of the set (formatted and filled anew).
//
// A disk pulled while mounted can leave its mount behind even after the Backup Server reports
// the datastore as not mounted; the next disk for that datastore then cannot be mounted
// there. Such mounts (device gone) are released.

// ForeignDisk is a disk that belongs to no datastore.
type ForeignDisk struct {
	Name   string `json:"name"` // sde
	Vendor string `json:"vendor"`
	Model  string `json:"model"`
	Serial string `json:"serial"`
	Size   int64  `json:"size"`
	UUID   string `json:"uuid,omitempty"` // filesystem on it
	Blank  bool   `json:"blank"`          // no filesystem: a new disk
}

type foreignRuntime struct {
	mu      sync.Mutex
	disks   map[string][]ForeignDisk // server → foreign disks
	sig     map[string]string        // server → disk list at the last look (wakes the monitor)
	staleAt map[string]time.Time     // server → last look for stale mounts
}

// usableUsed are the "used" states of disks that may be foreign; the others are in use by
// the system (LVM, ZFS, mounted, boot).
var usableUsed = map[string]bool{"unused": true, "filesystem": true, "partitions": true}

// foreignCandidates lists disks that are not used by the system and whose filesystems belong
// to no datastore.
func foreignCandidates(disks []prox.Disk, cfgs []prox.DatastoreConfig) []ForeignDisk {
	known := map[string]bool{}
	for _, c := range cfgs {
		if c.BackingDevice != "" {
			known[strings.ToLower(c.BackingDevice)] = true
		}
	}
	out := []ForeignDisk{}
	for _, d := range disks {
		if !usableUsed[d.Used] {
			continue
		}
		fd := ForeignDisk{Name: d.Name, Vendor: strings.TrimSpace(d.Vendor), Model: strings.TrimSpace(d.Model), Serial: d.Serial, Size: d.Size, Blank: true}
		skip := false
		for _, p := range d.Partitions {
			if p.Mounted || known[strings.ToLower(p.UUID)] || (p.Used != "" && !usableUsed[p.Used]) {
				skip = true
			}
			if p.UUID != "" || p.Filesystem != "" {
				fd.Blank = false
				if fd.UUID == "" {
					fd.UUID = p.UUID
				}
			}
		}
		if !skip {
			out = append(out, fd)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// diskSig sums up a disk list: names and filesystems.
func diskSig(disks []prox.Disk) string {
	var parts []string
	for _, d := range disks {
		parts = append(parts, d.Name)
		for _, p := range d.Partitions {
			parts = append(parts, p.Name+"="+p.UUID)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// noteDiskList wakes the monitor when the disks of a server changed since the last look
// (a disk plugged in that belongs to no datastore changes no rotation disk).
func (c *Core) noteDiskList(serverID, sig string) {
	c.foreign.mu.Lock()
	if c.foreign.sig == nil {
		c.foreign.sig = map[string]string{}
	}
	before, known := c.foreign.sig[serverID]
	c.foreign.sig[serverID] = sig
	c.foreign.mu.Unlock()
	if known && before != sig {
		c.RequestRefresh()
	}
}

// ForeignDisks returns the foreign disks last seen on a server.
func (c *Core) ForeignDisks(serverID string) []ForeignDisk {
	c.foreign.mu.Lock()
	defer c.foreign.mu.Unlock()
	if l := c.foreign.disks[serverID]; l != nil {
		return l
	}
	return []ForeignDisk{}
}

// usbOnly keeps the disks attached over USB (a spare internal disk is no rotation disk). It
// needs the command line of the server; without it all candidates stay.
func usbOnly(ctx context.Context, run hostexec.Runner, cand []ForeignDisk) []ForeignDisk {
	if run == nil || len(cand) == 0 {
		return cand
	}
	args := []string{"-c", `for d in "$@"; do echo "$d $(readlink -f /sys/block/$d)"; done`, "sh"}
	for _, d := range cand {
		args = append(args, d.Name)
	}
	out, err := run.Run(ctx, "sh", args...)
	if err != nil {
		return cand
	}
	usb := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if name, p, ok := strings.Cut(strings.TrimSpace(l), " "); ok && strings.Contains(p, "/usb") {
			usb[name] = true
		}
	}
	keep := []ForeignDisk{}
	for _, d := range cand {
		if usb[d.Name] {
			keep = append(keep, d)
		}
	}
	return keep
}

// staleEvery is how often stale mounts are looked for when nothing changed.
const staleEvery = 10 * time.Minute

// scanForeignDisks looks at the servers with rotation sets or archive disks: foreign disks,
// and (with command line access) stale mounts of their datastores.
func (c *Core) scanForeignDisks(ctx context.Context, now time.Time) {
	stores := map[string]map[string]bool{} // server → datastores of rotation/archive disks
	add := func(srv, store string) {
		if stores[srv] == nil {
			stores[srv] = map[string]bool{}
		}
		if store != "" {
			stores[srv][store] = true
		}
	}
	rots, _ := c.Rotations()
	for _, r := range rots {
		add(r.ServerID, "")
		for _, d := range r.Disks {
			add(r.ServerID, d.Store)
		}
	}
	if arch, err := c.Archives(); err == nil {
		for _, a := range arch {
			add(a.ServerID, a.Store)
		}
	}
	for id, st := range stores {
		pbs, srv, err := c.PBS(id)
		if err != nil {
			continue
		}
		disks, err := pbs.Disks(ctx)
		if err != nil {
			continue
		}
		cfgs, err := pbs.Datastores(ctx)
		if err != nil {
			continue
		}
		run := c.runnerOrNil(srv)
		list := usbOnly(ctx, run, foreignCandidates(disks, cfgs))
		sig := diskSig(disks)
		c.foreign.mu.Lock()
		if c.foreign.disks == nil {
			c.foreign.disks, c.foreign.staleAt = map[string][]ForeignDisk{}, map[string]time.Time{}
		}
		if c.foreign.sig == nil {
			c.foreign.sig = map[string]string{}
		}
		c.foreign.disks[id] = list
		due := c.foreign.sig[id] != sig || now.Sub(c.foreign.staleAt[id]) >= staleEvery
		if due {
			c.foreign.staleAt[id] = now
		}
		c.foreign.sig[id] = sig
		c.foreign.mu.Unlock()
		if run != nil && due {
			c.releaseStaleMounts(ctx, srv, pbs, run, st)
		}
	}
}

// staleMountScript lists the mounts below /mnt/datastore in mount order: target and source.
const staleMountScript = `findmnt -rn -o TARGET,SOURCE | while read -r t s; do case "$t" in /mnt/datastore/*) echo "$t $s";; esac; done`

// releaseStaleMounts unmounts datastore mounts whose device is gone. Only datastores of
// rotation and archive disks that the Backup Server reports as not mounted are touched, and
// only the top mount of their path (a live mount over a stale one stays until it is
// unmounted itself).
func (c *Core) releaseStaleMounts(ctx context.Context, srv PBSServer, pbs *prox.PBS, run hostexec.Runner, stores map[string]bool) []string {
	us, err := pbs.Usage(ctx)
	if err != nil {
		return nil // without the server's view nothing is touched
	}
	candidates := map[string]bool{}
	for _, u := range us {
		if stores[u.Store] && u.MountStatus != "mounted" {
			candidates[u.Store] = true
		}
	}
	stores = candidates
	out, err := run.Run(ctx, "sh", "-c", staleMountScript)
	if err != nil {
		return nil
	}
	top := map[string]string{} // target → device of the last (top) mount
	var order []string
	for _, l := range strings.Split(out, "\n") {
		t, s, ok := strings.Cut(strings.TrimSpace(l), " ")
		if !ok {
			continue
		}
		if i := strings.Index(s, "["); i > 0 {
			s = s[:i] // /dev/sdc1[/usb-usb1]
		}
		if _, seen := top[t]; !seen {
			order = append(order, t)
		}
		top[t] = s
	}
	var devs []string
	for _, t := range order {
		if stores[path.Base(t)] && strings.HasPrefix(top[t], "/dev/") {
			devs = append(devs, top[t])
		}
	}
	if len(devs) == 0 {
		return nil
	}
	ex, err := run.Run(ctx, "sh", append([]string{"-c", `for d in "$@"; do [ -b "$d" ] && echo "$d"; done; true`, "sh"}, devs...)...)
	if err != nil {
		return nil
	}
	exists := map[string]bool{}
	for _, l := range strings.Split(ex, "\n") {
		exists[strings.TrimSpace(l)] = true
	}
	var released []string
	for _, t := range order {
		store, dev := path.Base(t), top[t]
		if !stores[store] || !strings.HasPrefix(dev, "/dev/") || exists[dev] {
			continue
		}
		if _, err := run.Run(ctx, "umount", "-l", t); err != nil {
			continue
		}
		released = append(released, store)
		c.EventDetails("info", srv.Name, "Verwaisten Mount von „"+store+"“ gelöst",
			"Die Platte ("+dev+") wurde abgezogen, ihr Mount unter "+t+" war aber geblieben. Er ist jetzt gelöst, damit die nächste Platte für diesen Datastore wieder eingehängt werden kann.",
			&notify.Details{Status: "ok", Link: "/wechselplatten"})
	}
	return released
}

// AdoptDisk formats a foreign disk as the given disk of a set (its old disk is not plugged
// in) and fills it anew – for a disk whose datastore no longer matches it, or a replacement.
func (c *Core) AdoptDisk(ctx context.Context, r Rotation, d RotDisk, devName string) error {
	key := rtKey(r, d)
	if !c.tryBusy(key) {
		return errors.New("Für diese Platte läuft gerade etwas.")
	}
	rt := c.runtime(key)
	err := c.adoptDisk(ctx, r, d, rt, devName)
	c.setPhase(key, rt, "")
	c.doneBusy(key)
	if err != nil {
		return err
	}
	if err := c.RunRotationDisk(ctx, r, d); err != nil {
		return fmt.Errorf("Kopie starten: %w", err)
	}
	return nil
}

// CheckAdopt tells whether a disk can be taken over as d right now ("" = yes).
func (c *Core) CheckAdopt(ctx context.Context, r Rotation, d RotDisk, devName string) error {
	pbs, srv, err := c.PBS(r.ServerID)
	if err != nil {
		return err
	}
	_, _, err = adoptTarget(ctx, pbs, c.runnerOrNil(srv), d, devName)
	return err
}

func (c *Core) runnerOrNil(srv PBSServer) hostexec.Runner {
	run, err := c.Runner(srv)
	if err != nil {
		return nil
	}
	return run
}

// adoptTarget finds the foreign disk devName and makes sure the set's own disk is not there.
func adoptTarget(ctx context.Context, pbs *prox.PBS, run hostexec.Runner, d RotDisk, devName string) (ForeignDisk, []prox.DatastoreConfig, error) {
	disks, err := pbs.Disks(ctx)
	if err != nil {
		return ForeignDisk{}, nil, err
	}
	cfgs, err := pbs.Datastores(ctx)
	if err != nil {
		return ForeignDisk{}, nil, err
	}
	var disk *ForeignDisk
	for _, fd := range usbOnly(ctx, run, foreignCandidates(disks, cfgs)) {
		if fd.Name == devName {
			disk = &fd
		}
	}
	if disk == nil {
		return ForeignDisk{}, nil, fmt.Errorf("/dev/%s ist nicht mehr frei – inzwischen abgezogen, eingehängt oder einem Datastore zugeordnet", devName)
	}
	uuid := ""
	for _, cfg := range cfgs {
		if cfg.Name == d.Store {
			uuid = strings.ToLower(cfg.BackingDevice)
		}
	}
	for _, dk := range disks {
		for _, p := range dk.Partitions {
			if uuid != "" && strings.ToLower(p.UUID) == uuid {
				return ForeignDisk{}, nil, fmt.Errorf("„%s“ steckt gerade (/dev/%s) – dann dort „Neu aufsetzen“ wählen", d.Label, dk.Name)
			}
		}
	}
	return *disk, cfgs, nil
}

func (c *Core) adoptDisk(ctx context.Context, r Rotation, d RotDisk, rt *diskRuntime, devName string) error {
	pbs, srv, err := c.PBS(r.ServerID)
	if err != nil {
		return err
	}
	disk, cfgs, err := adoptTarget(ctx, pbs, c.runnerOrNil(srv), d, devName)
	if err != nil {
		return err
	}
	key := rtKey(r, d)
	c.setPhase(key, rt, "rebuilding")
	// The old disk's mount can still be there: free the path for the new one.
	if run := c.runnerOrNil(srv); run != nil {
		c.releaseStaleMounts(ctx, srv, pbs, run, map[string]bool{d.Store: true})
	}
	if storeExists(cfgs, d.Store) {
		if us, err := pbs.Usage(ctx); err == nil {
			for _, u := range us {
				if u.Store == d.Store && u.MountStatus == "mounted" {
					if err := c.unmountStore(ctx, pbs, d.Store); err != nil {
						return fmt.Errorf("Aushängen: %w", err)
					}
				}
			}
		}
		c.clearUnmountMode(ctx, pbs, d.Store)
		if err := pbs.DeleteDatastore(ctx, d.Store, false); err != nil {
			return fmt.Errorf("Datastore aus der Konfiguration nehmen: %w", err)
		}
	}
	if err := c.PrepareDisk(ctx, r.ServerID, devName, d.Store, "ext4", true); err != nil {
		return err
	}
	if err := c.ApplyRotation(ctx, r); err != nil {
		return fmt.Errorf("Kopierjob anlegen: %w", err)
	}
	c.mu.Lock()
	rt.Damaged, rt.RepairAt, rt.VerifyNext, rt.VerifyWhy, rt.DoneAt = "", time.Now().Unix(), true, "rebuild", 0
	rt.LastFail, rt.LastError = 0, ""
	rt.DamageSeen = rt.SeenUPID
	c.mu.Unlock()
	c.saveRuntime(key, rt)
	dev := strings.TrimSpace(disk.Vendor+" "+disk.Model) + " (/dev/" + devName + ")"
	c.EventDetails("info", rotationSource, "Festplatte „"+d.Label+"“ neu aufgesetzt",
		"Die angesteckte Platte gehörte zu keinem Datastore. Sie wurde als „"+d.Label+"“ formatiert, wird jetzt neu befüllt und danach vollständig zurückgelesen.",
		&notify.Details{Status: "ok", Link: "/wechselplatten", Facts: []notify.Fact{diskFact(d), {Label: "Gerät", Value: dev}}})
	return nil
}

func storeExists(cfgs []prox.DatastoreConfig, name string) bool {
	for _, c := range cfgs {
		if c.Name == name {
			return true
		}
	}
	return false
}
