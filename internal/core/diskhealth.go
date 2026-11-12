package core

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
	"pk95proxbackup/internal/store"
)

// Disk health: the SMART values of the disks of every Backup Server and Proxmox VE node are
// read once a day and right after a disk is plugged in. Defective sectors, a failing self
// assessment or a worn-out SSD raise an event. USB disks that sit in a drawer for years are
// recognised by their partition UUID, so their last check shows up on the archive and
// rotation pages.

// KindDiskHealth stores one record per disk.
const KindDiskHealth = "diskhealth"

const (
	healthListEvery  = 5 * time.Minute
	healthCheckEvery = 24 * time.Hour
	// ArchiveCheckAfter is how long an archive disk may lie unplugged before it should be checked.
	ArchiveCheckAfter = 365 * 24 * time.Hour
)

// DiskHealth is the last known health of a disk.
type DiskHealth struct {
	ID        string           `json:"id"`     // source + "/" + node + "/" + serial (or device)
	Source    string           `json:"source"` // pbs:<id> / pve:<id>
	Host      string           `json:"host"`   // name of the server or node
	Node      string           `json:"node,omitempty"`
	Disk      string           `json:"disk"` // sdc, /dev/nvme0n1
	Serial    string           `json:"serial"`
	Vendor    string           `json:"vendor"`
	Model     string           `json:"model"`
	Type      string           `json:"type"` // hdd, ssd, nvme, usb
	Size      int64            `json:"size"`
	UUIDs     []string         `json:"uuids"`
	Use       string           `json:"use"`                // what the disk holds (datastore, rotation disk, archive)
	Archive   string           `json:"archive,omitempty"`  // archive id
	Rotation  string           `json:"rotation,omitempty"` // rotation id/disk key
	Virtual   bool             `json:"virtual"`
	FirstSeen int64            `json:"first_seen"`
	LastSeen  int64            `json:"last_seen"`
	LastCheck int64            `json:"last_check"`
	Smart     prox.SmartReport `json:"smart"`
	Status    string           `json:"status"` // ok, warning, error, unknown
	Problems  []string         `json:"problems"`
	Note      string           `json:"note"`
}

// DiskHealthState adds whether the disk is plugged in now.
type DiskHealthState struct {
	DiskHealth
	Label   string `json:"label"`
	Present bool   `json:"present"`
}

type healthRuntime struct {
	mu     sync.Mutex
	busy   bool
	listed map[string]int64 // source → time of the last successful listing
}

// DiskHealths lists all known disks, present ones first, problems first.
func (c *Core) DiskHealths() []DiskHealthState {
	list, _ := store.List[DiskHealth](c.St, KindDiskHealth)
	c.health.mu.Lock()
	listed := map[string]int64{}
	for k, v := range c.health.listed {
		listed[k] = v
	}
	c.health.mu.Unlock()
	out := make([]DiskHealthState, 0, len(list))
	for _, h := range list {
		if h.Problems == nil {
			h.Problems = []string{}
		}
		out = append(out, DiskHealthState{DiskHealth: h, Label: DiskLabel(h), Present: listed[h.Source] > 0 && h.LastSeen >= listed[h.Source]})
	}
	rank := map[string]int{"error": 0, "warning": 1, "ok": 2, "unknown": 3}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Present != b.Present {
			return a.Present
		}
		if rank[a.Status] != rank[b.Status] {
			return rank[a.Status] < rank[b.Status]
		}
		if a.Host != b.Host {
			return a.Host < b.Host
		}
		return a.Disk < b.Disk
	})
	return out
}

// HealthByUUID returns the record of the disk holding a partition (datastore backing device).
func (c *Core) HealthByUUID(uuid string) (DiskHealth, bool) {
	if uuid == "" {
		return DiskHealth{}, false
	}
	list, _ := store.List[DiskHealth](c.St, KindDiskHealth)
	for _, h := range list {
		for _, u := range h.UUIDs {
			if strings.EqualFold(u, uuid) {
				return h, true
			}
		}
	}
	return DiskHealth{}, false
}

// ForgetDiskHealth deletes the record of a disk that is gone for good.
func (c *Core) ForgetDiskHealth(id string) error { return c.St.Delete(KindDiskHealth, id) }

// CheckDisksNow reads the SMART values of all disks right away (button in the UI).
func (c *Core) CheckDisksNow(ctx context.Context) {
	c.scanDiskHealth(ctx, time.Now(), true)
}

// scheduleDiskHealth runs the scan in the background unless one is running.
func (c *Core) scheduleDiskHealth(now time.Time) {
	go c.scanDiskHealth(context.Background(), now, false)
}

func (c *Core) scanDiskHealth(ctx context.Context, now time.Time, force bool) {
	c.health.mu.Lock()
	if c.health.busy {
		c.health.mu.Unlock()
		return
	}
	c.health.busy = true
	if c.health.listed == nil {
		c.health.listed = map[string]int64{}
	}
	c.health.mu.Unlock()
	defer func() {
		c.health.mu.Lock()
		c.health.busy = false
		c.health.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	known := map[string]DiskHealth{}
	if list, err := store.List[DiskHealth](c.St, KindDiskHealth); err == nil {
		for _, h := range list {
			known[h.ID] = h
		}
	}
	var uses map[string]diskUse
	servers, _ := c.Servers()
	for _, s := range servers {
		src := "pbs:" + s.ID
		if !force && now.Unix()-c.listedAt(src) < int64(healthListEvery.Seconds()) {
			continue
		}
		if uses == nil {
			uses = c.diskUses(ctx)
		}
		pbs := c.PBSClient(s)
		disks, err := pbs.Disks(ctx)
		if err != nil {
			continue
		}
		prevList := c.listedAt(src)
		for _, d := range disks {
			h := DiskHealth{Source: src, Host: s.Name, Disk: d.Name, Serial: strings.TrimSpace(d.Serial), Vendor: strings.TrimSpace(d.Vendor),
				Model: strings.TrimSpace(d.Model), Type: d.DiskType, Size: d.Size, UUIDs: []string{}}
			for _, p := range d.Partitions {
				if p.UUID != "" {
					h.UUIDs = append(h.UUIDs, strings.ToLower(p.UUID))
				}
			}
			h.ID = diskHealthID(src, "", h.Serial, d.Name)
			h.Virtual = isVirtualDisk(h.Vendor, h.Model)
			name := d.Name
			c.updateHealth(known, h, uses, now, prevList, force, func() (prox.SmartReport, error) { return pbs.Smart(ctx, name) })
		}
		c.setListed(src, now.Unix())
	}
	clusters, _ := c.Clusters()
	for _, cl := range clusters {
		src := "pve:" + cl.ID
		if !force && now.Unix()-c.listedAt(src) < int64(healthListEvery.Seconds()) {
			continue
		}
		if uses == nil {
			uses = c.diskUses(ctx)
		}
		pve := c.PVEClient(cl)
		nodes, err := pve.Nodes(ctx)
		if err != nil {
			continue
		}
		prevList := c.listedAt(src)
		listedAll := true
		for _, n := range nodes {
			if n.Status != "" && n.Status != "online" {
				listedAll = false
				continue
			}
			disks, err := pve.NodeDisks(ctx, n.Node)
			if err != nil {
				listedAll = false
				continue
			}
			for _, d := range disks {
				h := DiskHealth{Source: src, Host: n.Node, Node: n.Node, Disk: d.DevPath, Serial: strings.TrimSpace(d.Serial), Vendor: strings.TrimSpace(d.Vendor),
					Model: strings.TrimSpace(d.Model), Type: d.Type, Size: d.Size, UUIDs: []string{}}
				h.ID = diskHealthID(src, n.Node, h.Serial, d.DevPath)
				h.Virtual = isVirtualDisk(h.Vendor, h.Model)
				node, disk := n.Node, d
				c.updateHealth(known, h, uses, now, prevList, force, func() (prox.SmartReport, error) { return pve.NodeSmart(ctx, node, disk) })
			}
		}
		if listedAll {
			c.setListed(src, now.Unix())
		}
	}
}

func (c *Core) listedAt(src string) int64 {
	c.health.mu.Lock()
	defer c.health.mu.Unlock()
	return c.health.listed[src]
}

func (c *Core) setListed(src string, t int64) {
	c.health.mu.Lock()
	if c.health.listed == nil {
		c.health.listed = map[string]int64{}
	}
	c.health.listed[src] = t
	c.health.mu.Unlock()
}

func diskHealthID(src, node, serial, dev string) string {
	key := serial
	if key == "" {
		key = "dev-" + strings.TrimPrefix(dev, "/dev/")
	}
	if node != "" {
		return src + "/" + node + "/" + key
	}
	return src + "/" + key
}

func isVirtualDisk(vendor, model string) bool {
	s := strings.ToUpper(vendor + " " + model)
	for _, v := range []string{"QEMU", "VIRTUAL", "VBOX", "VMWARE", "MSFT", "RED HAT", "VIRTIO"} {
		if strings.Contains(s, v) {
			return true
		}
	}
	return false
}

// diskUse tells what a partition (by UUID) holds.
type diskUse struct{ use, archive, rotation string }

// diskUses maps partition UUIDs of removable datastores to what they hold.
func (c *Core) diskUses(ctx context.Context) map[string]diskUse {
	out := map[string]diskUse{}
	storeUUID := map[string]string{} // server/store → uuid
	servers, _ := c.Servers()
	for _, s := range servers {
		cfgs, err := c.PBSClient(s).Datastores(ctx)
		if err != nil {
			continue
		}
		for _, d := range cfgs {
			if d.BackingDevice == "" {
				continue
			}
			u := strings.ToLower(d.BackingDevice)
			storeUUID[s.ID+"/"+d.Name] = u
			out[u] = diskUse{use: "Datastore " + d.Name}
		}
	}
	if rots, err := c.Rotations(); err == nil {
		for _, r := range rots {
			for _, d := range r.Disks {
				if u := storeUUID[r.ServerID+"/"+d.Store]; u != "" {
					out[u] = diskUse{use: "Wechselplatte „" + d.Label + "“ (" + r.Name + ")", rotation: r.ID + "/" + d.Key}
				}
			}
		}
	}
	if archives, err := c.Archives(); err == nil {
		for _, a := range archives {
			if u := storeUUID[a.ServerID+"/"+a.Store]; u != "" {
				kind := "Archiv-Platte"
				if a.Imported {
					kind = "Eingelesene Platte"
				}
				out[u] = diskUse{use: kind + " „" + a.Name + "“", archive: a.ID}
			}
		}
	}
	return out
}

// updateHealth records a listed disk and reads its SMART values when due.
func (c *Core) updateHealth(known map[string]DiskHealth, h DiskHealth, uses map[string]diskUse,
	now time.Time, prevList int64, force bool, smart func() (prox.SmartReport, error)) {
	old, had := known[h.ID]
	h.FirstSeen, h.LastSeen = old.FirstSeen, now.Unix()
	if !had {
		h.FirstSeen = now.Unix()
	}
	for _, u := range h.UUIDs {
		if x, ok := uses[u]; ok {
			h.Use, h.Archive, h.Rotation = x.use, x.archive, x.rotation
		}
	}
	// Plugged in again (it was not there at the previous listing): check right away.
	replugged := had && prevList > 0 && old.LastSeen < prevList
	due := force || !had || old.LastCheck == 0 || replugged || now.Unix()-old.LastCheck >= int64(healthCheckEvery.Seconds())
	if !due || h.Virtual {
		h.LastCheck, h.Smart, h.Status, h.Problems, h.Note = old.LastCheck, old.Smart, old.Status, old.Problems, old.Note
		if h.Virtual {
			h.Smart, h.Status, h.Problems = prox.NewSmartReport(), "unknown", []string{}
			h.Note = "Virtuelle Platte: die echten Platten prüft die Appliance über Proxmox VE."
		}
		if h.Status == "" {
			h.Status = "unknown"
		}
		_ = c.St.Put(KindDiskHealth, h.ID, h)
		return
	}
	rep, err := smart()
	h.LastCheck = now.Unix()
	h.Smart = rep
	h.Status, h.Problems = EvaluateSmart(rep)
	h.Note = ""
	nothing := rep.Health == "unknown" && rep.Reallocated < 0 && rep.Pending < 0 && rep.MediaErrors < 0 && rep.Wearout < 0
	if (err != nil || nothing) && h.Status == "ok" {
		h.Status = "unknown"
		switch {
		case err != nil && strings.Contains(strings.ToLower(err.Error()), "permission"):
			h.Note = "Keine Berechtigung, SMART-Werte zu lesen (Sys.Audit)."
		case h.Type == "usb":
			h.Note = "Der USB-Adapter gibt keine SMART-Werte weiter."
		case err != nil:
			h.Note = "SMART-Werte nicht lesbar: " + err.Error()
		default:
			h.Note = "Die Platte liefert keine SMART-Werte."
		}
	}
	if err := c.St.Put(KindDiskHealth, h.ID, h); err != nil {
		log.Printf("diskhealth: %v", err)
		return
	}
	c.healthEvent(old, had, h)
}

// EvaluateSmart rates a SMART report: error (replace the disk), warning (watch it) or ok.
func EvaluateSmart(r prox.SmartReport) (string, []string) {
	status := "ok"
	problems := []string{}
	bad := func(s string, f string, a ...any) {
		problems = append(problems, fmt.Sprintf(f, a...))
		if s == "error" || status == "ok" {
			status = s
		}
	}
	if r.Health == "failed" {
		bad("error", "Die Platte meldet selbst, dass sie bald ausfällt (SMART-Selbsttest nicht bestanden).")
	}
	if r.Pending > 0 {
		bad("error", "%d %s nicht mehr sicher lesen (schwebende Sektoren).", r.Pending, plural(int(r.Pending), "Sektor lässt sich", "Sektoren lassen sich"))
	}
	if r.Uncorrectable > 0 {
		bad("error", "%d %s nicht lesbar.", r.Uncorrectable, plural(int(r.Uncorrectable), "Sektor ist", "Sektoren sind"))
	}
	if r.MediaErrors > 0 {
		bad("error", "%d Lesefehler im Flash-Speicher.", r.MediaErrors)
	}
	if r.Reallocated > 0 {
		bad("warning", "%d %s schon durch Reserve ersetzt.", r.Reallocated, plural(int(r.Reallocated), "defekter Sektor wurde", "defekte Sektoren wurden"))
	}
	if r.Wearout >= 0 && r.Wearout <= 10 {
		bad("warning", "Die SSD ist abgenutzt: noch %d %% Lebensdauer.", r.Wearout)
	}
	if r.Temperature >= 60 && r.Temperature < 120 {
		bad("warning", "Die Platte ist %d °C warm (über 60 °C).", r.Temperature)
	}
	return status, problems
}

func healthRank(s string) int {
	switch s {
	case "error":
		return 2
	case "warning":
		return 1
	}
	return 0
}

// healthEvent reports a disk that got worse: a worse status or more defective sectors.
func (c *Core) healthEvent(old DiskHealth, had bool, h DiskHealth) {
	worse := healthRank(h.Status) > healthRank(old.Status)
	grew := func(n, o int64) bool { return o >= 0 && n > o }
	more := had && healthRank(h.Status) > 0 && (grew(h.Smart.Reallocated, old.Smart.Reallocated) || grew(h.Smart.Pending, old.Smart.Pending) ||
		grew(h.Smart.Uncorrectable, old.Smart.Uncorrectable) || grew(h.Smart.MediaErrors, old.Smart.MediaErrors))
	if !worse && !more {
		return
	}
	sev, status, title := "warning", "warning", "Festplatte zeigt erste Fehler"
	if h.Status == "error" {
		sev, status, title = "critical", "error", "Festplatte bald tauschen"
	}
	if !worse {
		title = "Festplatte: weitere defekte Sektoren"
	}
	body := strings.Join(h.Problems, " ")
	if h.Status == "error" {
		body += " Die Daten auf dieser Platte sind in Gefahr: bitte bald ersetzen."
	}
	d := &notify.Details{Status: status, Link: "/festplatten", Facts: healthFacts(h)}
	c.EventDetails(sev, h.Host, title+": "+DiskLabel(h), body, d)
}

func healthFacts(h DiskHealth) []notify.Fact {
	f := []notify.Fact{{Label: "Server", Value: h.Host}, {Label: "Platte", Value: DiskLabel(h) + " (" + strings.TrimPrefix(h.Disk, "/dev/") + ")"}}
	if h.Serial != "" {
		f = append(f, notify.Fact{Label: "Seriennummer", Value: h.Serial})
	}
	if h.Use != "" {
		f = append(f, notify.Fact{Label: "Verwendung", Value: h.Use})
	}
	if h.Smart.PowerOnHours >= 0 {
		f = append(f, notify.Fact{Label: "Betriebsstunden", Value: fmt.Sprintf("%d (etwa %.1f Jahre)", h.Smart.PowerOnHours, float64(h.Smart.PowerOnHours)/8766)})
	}
	if h.Smart.Temperature >= 0 {
		f = append(f, notify.Fact{Label: "Temperatur", Value: fmt.Sprintf("%d °C", h.Smart.Temperature)})
	}
	return f
}

// DiskLabel names a disk by vendor and model ("WDC WD30EZRX-00D8PB0", "SanDisk Extreme Pro 500GB").
func DiskLabel(h DiskHealth) string {
	model := strings.Join(strings.Fields(strings.ReplaceAll(h.Model, "_", " ")), " ")
	vendor := strings.TrimSpace(h.Vendor)
	name := model
	if f := strings.Fields(strings.ToUpper(vendor)); len(f) > 0 && f[0] != "ATA" && f[0] != "UNKNOWN" && !strings.HasPrefix(strings.ToUpper(model), f[0]) {
		name = vendor + " " + model
	}
	if name == "" {
		name = strings.TrimPrefix(h.Disk, "/dev/")
	}
	return name
}

// ArchiveLastSeen is when an archive disk was last plugged in, copied or checked.
func (c *Core) ArchiveLastSeen(a Archive) int64 {
	last := a.End
	for _, ch := range a.Checks {
		if ch.At > last {
			last = ch.At
		}
	}
	if list, err := store.List[DiskHealth](c.St, KindDiskHealth); err == nil {
		for _, h := range list {
			if h.Archive == a.ID && h.LastSeen > last {
				last = h.LastSeen
			}
		}
	}
	return last
}

// HealthBrief is the health of a disk shown next to a rotation or archive disk.
type HealthBrief struct {
	Status    string   `json:"status"`
	Problems  []string `json:"problems"`
	Note      string   `json:"note,omitempty"`
	LastCheck int64    `json:"last_check"`
	Hours     int64    `json:"hours"`
}

type healthIdx struct{ rotation, archive map[string]*HealthBrief }

func (c *Core) healthIndex() healthIdx {
	idx := healthIdx{rotation: map[string]*HealthBrief{}, archive: map[string]*HealthBrief{}}
	list, _ := store.List[DiskHealth](c.St, KindDiskHealth)
	for _, h := range list {
		if h.LastCheck == 0 {
			continue
		}
		p := h.Problems
		if p == nil {
			p = []string{}
		}
		b := &HealthBrief{Status: h.Status, Problems: p, Note: h.Note, LastCheck: h.LastCheck, Hours: h.Smart.PowerOnHours}
		if h.Rotation != "" {
			idx.rotation[h.Rotation] = b
		}
		if h.Archive != "" {
			idx.archive[h.Archive] = b
		}
	}
	return idx
}
