package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
	"pk95proxbackup/internal/store"
)

// KindArchive is the store kind of one-off USB archive copies.
const KindArchive = "archive"

// Archive is a one-off copy of backups onto a USB disk that is taken away or put on the
// shelf for years (in contrast to a rotation set, which is overwritten again and again).
// The disk becomes a removable datastore; its configuration stays on the Backup Server so
// that it is recognised and can be restored from when it is plugged in again.
type Archive struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"` // e.g. "Jahresarchiv 2026"
	ServerID     string   `json:"server_id"`
	Store        string   `json:"store"`
	SourceServer string   `json:"source_server"`
	SourceStore  string   `json:"source_store"`
	SourceNS     string   `json:"source_ns,omitempty"`
	Groups       []string `json:"groups"` // "vm/101", empty = all
	Latest       bool     `json:"latest"` // only the newest backup of each VM/CT
	Verify       bool     `json:"verify"` // read everything back after the copy
	Location     string   `json:"location,omitempty"`
	Note         string   `json:"note,omitempty"`
	Created      int64    `json:"created"`
	Imported     bool     `json:"imported,omitempty"` // read in from another Backup Server

	// Disk to prepare on the first run ("" = Store is an existing removable datastore).
	Disk string `json:"disk,omitempty"`
	FS   string `json:"fs,omitempty"`

	// State of the copy.
	Status     string        `json:"status"` // running, ok, warning, error
	Phase      string        `json:"phase,omitempty"`
	PhaseSince int64         `json:"phase_since,omitempty"`
	UPID       string        `json:"upid,omitempty"` // PBS task of the current phase
	Start      int64         `json:"start"`
	End        int64         `json:"end"`
	CopySecs   int64         `json:"copy_secs"`
	Copied     string        `json:"copied,omitempty"` // "487,49 GiB mit durchschnittlich 96 MiB/s"
	VerifySecs int64         `json:"verify_secs"`
	Error      string        `json:"error,omitempty"`
	Problems   []string      `json:"problems,omitempty"`
	Contents   []ArchiveItem `json:"contents"`
	Used       int64         `json:"used"`
	Total      int64         `json:"total"`
	Checks     []ArchiveRun  `json:"checks"` // later checks of the disk (newest last)
}

// ArchiveItem is one VM/CT on an archive disk.
type ArchiveItem struct {
	Type  string `json:"type"` // vm, ct, host
	ID    string `json:"id"`
	Name  string `json:"name"`
	Time  int64  `json:"time"` // newest backup
	Count int    `json:"count"`
	Size  int64  `json:"size"`
}

// ArchiveRun is the result of a later check of an archive disk.
type ArchiveRun struct {
	At    int64  `json:"at"`
	OK    bool   `json:"ok"`
	Secs  int64  `json:"secs"`
	Error string `json:"error,omitempty"`
}

// ArchiveState is an archive with what the Backup Server says about it right now.
type ArchiveState struct {
	Archive
	ServerName string `json:"server_name"`
	SourceName string `json:"source_name"`
	Present    bool   `json:"present"`
	Mounted    bool   `json:"mounted"`
	Busy       bool   `json:"busy"`
	Missing    bool   `json:"missing"` // datastore no longer configured
	// LastSeen is when the disk was last plugged in, copied or checked.
	LastSeen int64        `json:"last_seen"`
	Health   *HealthBrief `json:"health,omitempty"`
}

var archivePhaseLabel = map[string]string{
	"preparing": "Platte wird vorbereitet", "mounting": "wird eingehängt", "copying": "Sicherungen werden kopiert",
	"verifying": "Kopie wird geprüft", "reading": "Inhalt wird gelesen", "unmounting": "wird ausgehängt",
}

// Archives lists the archive disks, newest first.
func (c *Core) Archives() ([]Archive, error) {
	list, err := store.List[Archive](c.St, KindArchive)
	sort.SliceStable(list, func(i, j int) bool { return list[i].Created > list[j].Created })
	return list, err
}

// GetArchive loads one archive.
func (c *Core) GetArchive(id string) (Archive, error) {
	var a Archive
	err := c.St.Get(KindArchive, id, &a)
	return a, err
}

func archiveKey(id string) string { return "archive:" + id }

// ArchiveBusy tells whether a copy or check of the archive is in progress.
func (c *Core) ArchiveBusy(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rotBusy[archiveKey(id)]
}

// ArchiveStates adds the live state (plugged in, mounted) to the archives.
func (c *Core) ArchiveStates(ctx context.Context) []ArchiveState {
	list, _ := c.Archives()
	names := map[string]string{}
	if servers, err := c.Servers(); err == nil {
		for _, s := range servers {
			names[s.ID] = s.Name
		}
	}
	factsBy := map[string]pbsFacts{}
	hidx := c.healthIndex()
	out := []ArchiveState{}
	for _, a := range list {
		st := ArchiveState{Archive: a, ServerName: names[a.ServerID], SourceName: names[a.SourceServer], Busy: c.ArchiveBusy(a.ID),
			Health: hidx.archive[a.ID], LastSeen: c.ArchiveLastSeen(a)}
		if st.Status == "running" && !st.Busy {
			// The appliance was restarted during the copy.
			st.Status, st.Phase, st.Error = "error", "", "Abgebrochen: Die Appliance wurde während der Sicherung neu gestartet."
		}
		f, ok := factsBy[a.ServerID]
		if !ok {
			if pbs, _, err := c.PBS(a.ServerID); err == nil {
				f = c.facts(ctx, pbs)
			} else {
				f.err = err
			}
			factsBy[a.ServerID] = f
		}
		if f.err == nil && a.Store != "" && a.Disk == "" {
			cfg, known := f.cfg[a.Store]
			u := f.usage[a.Store]
			st.Missing = !known
			st.Mounted = u.MountStatus == "mounted"
			st.Present = st.Mounted || (cfg.BackingDevice != "" && f.uuids[strings.ToLower(cfg.BackingDevice)])
		}
		out = append(out, st)
	}
	return out
}

// ValidateArchive checks a new archive before it is saved.
func (c *Core) ValidateArchive(a *Archive) map[string]string {
	errs := map[string]string{}
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		errs["name"] = "Bitte einen Namen angeben, z. B. „Jahresarchiv 2026“ – er gehört auch außen auf die Platte."
	}
	if a.ServerID == "" {
		errs["server_id"] = "Backup-Server wählen, an dem die Platte steckt."
	}
	if a.SourceServer == "" || a.SourceStore == "" {
		errs["source_store"] = "Quelle (Datastore mit den Sicherungen) wählen."
	}
	if a.ServerID == a.SourceServer && a.Store == a.SourceStore {
		errs["store"] = "Ziel und Quelle sind derselbe Datastore."
	}
	if rots, err := c.Rotations(); err == nil {
		for _, r := range rots {
			for _, d := range r.Disks {
				if r.ServerID == a.ServerID && d.Store == a.Store {
					errs["store"] = "Dieser Datastore gehört zum Wechselplatten-Satz „" + r.Name + "“ und wird dort regelmäßig überschrieben."
				}
			}
		}
	}
	if list, err := c.Archives(); err == nil {
		for _, x := range list {
			if x.ServerID == a.ServerID && x.Store == a.Store && x.ID != a.ID {
				errs["store"] = "Diese Platte ist schon als Archiv „" + x.Name + "“ eingetragen."
			}
		}
	}
	for i, g := range a.Groups {
		g = strings.TrimSpace(g)
		t, id, ok := strings.Cut(g, "/")
		if !ok || (t != "vm" && t != "ct" && t != "host") || id == "" || strings.ContainsAny(id, " /") {
			errs["groups"] = "Ungültige Auswahl: " + g
		}
		a.Groups[i] = g
	}
	return errs
}

// StartArchive saves a new archive and copies onto it in the background.
func (c *Core) StartArchive(a Archive) (Archive, error) {
	a.ID = NewID(6)
	a.Created = time.Now().Unix()
	a.Status, a.Start = "running", a.Created
	if a.Groups == nil {
		a.Groups = []string{}
	}
	a.Contents, a.Checks = []ArchiveItem{}, []ArchiveRun{}
	key := archiveKey(a.ID)
	if !c.tryBusy(key) {
		return a, errors.New("läuft bereits")
	}
	if err := c.St.Put(KindArchive, a.ID, a); err != nil {
		c.doneBusy(key)
		return a, err
	}
	go func() {
		defer c.doneBusy(key)
		c.runArchive(context.Background(), a.ID, false)
	}()
	c.RequestRefresh()
	return a, nil
}

// CheckArchive plugs an old archive disk in again: mount, read everything back, unmount.
func (c *Core) CheckArchive(id string) error {
	if _, err := c.GetArchive(id); err != nil {
		return err
	}
	key := archiveKey(id)
	if !c.tryBusy(key) {
		return errors.New("Für diese Platte läuft schon etwas.")
	}
	go func() {
		defer c.doneBusy(key)
		c.runArchive(context.Background(), id, true)
	}()
	c.RequestRefresh()
	return nil
}

// updateArchive changes the stored archive under the lock (the UI reads it while it runs).
func (c *Core) updateArchive(id string, fn func(a *Archive)) Archive {
	c.mu.Lock()
	defer c.mu.Unlock()
	var a Archive
	if err := c.St.Get(KindArchive, id, &a); err != nil {
		return a
	}
	fn(&a)
	_ = c.St.Put(KindArchive, id, a)
	return a
}

func (c *Core) setArchivePhase(id, phase, upid string) {
	c.updateArchive(id, func(a *Archive) {
		if a.Phase != phase {
			a.PhaseSince = time.Now().Unix()
		}
		a.Phase, a.UPID = phase, upid
	})
	c.RequestRefresh()
}

// runArchive does the copy (check=false) or a later check of the disk (check=true).
func (c *Core) runArchive(ctx context.Context, id string, check bool) {
	start := time.Now()
	a, err := c.GetArchive(id)
	if err != nil {
		return
	}
	if !check {
		c.updateArchive(id, func(x *Archive) { x.Status, x.Start, x.End, x.Error, x.Problems = "running", start.Unix(), 0, "", nil })
	}
	var problems []string
	failed := func(step string, err error) {
		msg := step + ": " + err.Error()
		c.setArchivePhase(id, "", "")
		c.updateArchive(id, func(x *Archive) {
			if check {
				x.Checks = append(x.Checks, ArchiveRun{At: start.Unix(), Secs: int64(time.Since(start).Seconds()), Error: msg})
				return
			}
			x.Status, x.End, x.Error, x.Problems = "error", time.Now().Unix(), msg, problems
		})
		c.archiveEvent(id, check, "error", msg, problems)
	}
	pbs, srv, err := c.PBS(a.ServerID)
	if err != nil {
		failed("Backup-Server", err)
		return
	}
	// 1. Prepare a new disk (wipe, format, removable datastore).
	if a.Disk != "" && !check {
		c.setArchivePhase(id, "preparing", "")
		if err := c.PrepareDisk(ctx, a.ServerID, a.Disk, a.Store, a.FS, true); err != nil {
			failed("Platte vorbereiten", err)
			return
		}
		c.updateArchive(id, func(x *Archive) { x.Disk = "" })
	}
	// 2. Mount.
	c.setArchivePhase(id, "mounting", "")
	if err := c.mountStore(ctx, pbs, a.Store); err != nil {
		failed("Platte einhängen", err)
		return
	}
	unmount := func() {
		c.setArchivePhase(id, "unmounting", "")
		if err := c.unmountStore(ctx, pbs, a.Store); err != nil {
			problems = append(problems, "Aushängen: "+err.Error())
		}
	}
	// 3. Copy (a one-off pull: no sync job is left behind on the server).
	if !check {
		remote, err := c.RemoteFor(ctx, pbs, srv, a.SourceServer)
		if err != nil {
			unmount()
			failed("Verbindung zur Quelle", err)
			return
		}
		p := prox.Params{"store": a.Store, "remote-store": a.SourceStore, "remove-vanished": false}
		if remote != "" {
			p["remote"] = remote
		}
		if a.SourceNS != "" {
			p["remote-ns"] = a.SourceNS
		}
		if a.Latest {
			p["transfer-last"] = 1
		}
		if len(a.Groups) > 0 {
			var gf []string
			for _, g := range a.Groups {
				gf = append(gf, "group:"+g)
			}
			// The configuration of the appliance always goes along.
			gf = append(gf, "group:"+selfBackupType+"/"+selfBackupID)
			p["group-filter"] = gf
		}
		upid, err := pbs.StartTask(ctx, "/pull", p)
		if err != nil {
			unmount()
			failed("Kopie starten", err)
			return
		}
		c.setArchivePhase(id, "copying", upid)
		t0 := time.Now()
		if _, err := pbs.WaitTask(ctx, upid, 72*time.Hour); err != nil {
			if lines, lerr := pbs.TaskLog(ctx, upid); lerr == nil {
				problems = append(problems, taskLogExcerpt(lines)...)
			}
			unmount()
			failed("Kopie", err)
			return
		}
		copied := ""
		if lines, err := pbs.TaskLog(ctx, upid); err == nil {
			if f, ok := copiedFact(prox.ParseSyncSummary(lines)); ok {
				copied = f.Value
			}
		}
		c.updateArchive(id, func(x *Archive) { x.CopySecs, x.Copied = int64(time.Since(t0).Seconds()), copied })
	}
	// 4. Read everything back.
	var verifySecs int64
	if a.Verify || check {
		upid, err := pbs.StartTask(ctx, "/admin/datastore/"+url.PathEscape(a.Store)+"/verify", prox.Params{"ignore-verified": false})
		if err == nil {
			c.setArchivePhase(id, "verifying", upid)
			t0 := time.Now()
			_, err = pbs.WaitTask(ctx, upid, 72*time.Hour)
			verifySecs = int64(time.Since(t0).Seconds())
		}
		if err != nil {
			if check {
				unmount()
				failed("Prüfung", err)
				return
			}
			problems = append(problems, "Prüfung: "+err.Error())
		}
	}
	// 5. What is on the disk. Stored, because the guests may be long gone when it is needed.
	c.setArchivePhase(id, "reading", "")
	items, rerr := c.archiveContents(ctx, pbs, a.Store)
	if rerr != nil {
		problems = append(problems, "Inhalt lesen: "+rerr.Error())
	} else if len(items) == 0 {
		problems = append(problems, "Auf der Platte ist keine Sicherung – stimmen Quelle und Auswahl?")
	}
	var used, total int64
	if us, err := pbs.Usage(ctx); err == nil {
		for _, u := range us {
			if u.Store == a.Store {
				used, total = u.Used, u.Total
			}
		}
	}
	unmount()
	c.setArchivePhase(id, "", "")
	status := "ok"
	if len(problems) > 0 {
		status = "warning"
	}
	c.updateArchive(id, func(x *Archive) {
		if rerr == nil {
			x.Contents = items
		}
		if total > 0 {
			x.Used, x.Total = used, total
		}
		if check {
			x.Checks = append(x.Checks, ArchiveRun{At: start.Unix(), OK: len(problems) == 0, Secs: int64(time.Since(start).Seconds()), Error: strings.Join(problems, "; ")})
			return
		}
		x.Status, x.End, x.Problems, x.VerifySecs = status, time.Now().Unix(), problems, verifySecs
	})
	c.archiveEvent(id, check, status, "", problems)
}

// mountStore mounts a removable datastore unless it is mounted already.
func (c *Core) mountStore(ctx context.Context, pbs *prox.PBS, name string) error {
	usage, err := pbs.Usage(ctx)
	if err != nil {
		return err
	}
	for _, u := range usage {
		if u.Store == name && (u.MountStatus == "mounted" || u.MountStatus == "nonremovable") {
			return nil
		}
	}
	upid, err := pbs.Mount(ctx, name)
	if err != nil {
		return fmt.Errorf("%w (steckt die Platte?)", err)
	}
	_, err = pbs.WaitTask(ctx, upid, 5*time.Minute)
	return err
}

// storeMounted tells whether a datastore is removable and whether it is mounted.
func (c *Core) storeMounted(ctx context.Context, pbs *prox.PBS, name string) (mounted, removable bool) {
	usage, err := pbs.Usage(ctx)
	if err != nil {
		return false, false
	}
	for _, u := range usage {
		if u.Store == name {
			return u.MountStatus == "mounted", u.MountStatus == "mounted" || u.MountStatus == "notmounted"
		}
	}
	return false, false
}

func (c *Core) archiveContents(ctx context.Context, pbs *prox.PBS, st string) ([]ArchiveItem, error) {
	snaps, err := pbs.Snapshots(ctx, st, "")
	if err != nil {
		return nil, err
	}
	names := c.guestNames(ctx)
	by := map[string]*ArchiveItem{}
	for _, s := range snaps {
		k := s.Type + "/" + s.ID
		it := by[k]
		if it == nil {
			it = &ArchiveItem{Type: s.Type, ID: s.ID}
			by[k] = it
		}
		it.Count++
		if s.Time > it.Time {
			it.Time, it.Size = s.Time, s.Size
			if n := names[k]; n != "" {
				it.Name = n
			} else if s.Comment != "" {
				it.Name = s.Comment
			}
		}
	}
	out := []ArchiveItem{}
	for _, it := range by {
		// Guests no Proxmox VE knows any more: the name from the configuration in the backup.
		if it.Name == "" && (it.Type == "vm" || it.Type == "ct") {
			file := "qemu-server.conf.blob"
			if it.Type == "ct" {
				file = "pct.conf.blob"
			}
			if conf, err := pbs.SnapshotFile(ctx, st, "", it.Type, it.ID, it.Time, file); err == nil {
				it.Name = prox.GuestNameFromConfig(conf)
			}
		}
		out = append(out, *it)
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := strconv.Atoi(out[i].ID)
		b, _ := strconv.Atoi(out[j].ID)
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// archiveEvent reports the end of a copy or check (customers read it).
func (c *Core) archiveEvent(id string, check bool, status, errMsg string, problems []string) {
	a, err := c.GetArchive(id)
	if err != nil {
		return
	}
	det := &notify.Details{Status: status, Link: "/wechselplatten"}
	det.Facts = []notify.Fact{{Label: "Festplatte", Value: "„" + a.Name + "“"}}
	if a.Location != "" {
		det.Facts = append(det.Facts, notify.Fact{Label: "Aufbewahrungsort", Value: a.Location})
	}
	if status == "error" {
		det.Facts = append(det.Facts, notify.Fact{Label: "Fehlermeldung", Value: errMsg})
		det.Log = problems
		if check {
			c.EventDetails("critical", archiveSource, "Archiv-Festplatte „"+a.Name+"“: Prüfung fehlgeschlagen",
				"Die Archiv-Festplatte „"+a.Name+"“ konnte nicht vollständig gelesen werden. Bitte bewahren Sie sie trotzdem weiter auf und leiten Sie diese Nachricht an Ihren IT-Betreuer weiter.", det)
		} else {
			c.EventDetails("critical", archiveSource, "Archiv-Sicherung „"+a.Name+"“ fehlgeschlagen",
				"Die Archiv-Sicherung auf die Festplatte „"+a.Name+"“ hat nicht geklappt. Bitte lassen Sie die Festplatte angeschlossen und leiten Sie diese Nachricht an Ihren IT-Betreuer weiter.", det)
		}
		return
	}
	for _, it := range a.Contents {
		kind := strings.ToUpper(it.Type)
		if kind == "HOST" {
			kind = "Host"
		}
		det.Rows = append(det.Rows, notify.Row{Kind: kind, ID: it.ID, Name: it.Name, Status: "ok", Time: it.Time, Size: fmtBytes(it.Size)})
	}
	det.RowsTitle = "Diese Server sind auf der Archiv-Festplatte"
	det.Facts = append(det.Facts, notify.Fact{Label: "Enthält", Value: fmt.Sprintf("%d Server", len(a.Contents))})
	if a.Total > 0 {
		det.Facts = append(det.Facts, notify.Fact{Label: "Festplatte belegt", Value: fmt.Sprintf("%d %% (%s von %s)", a.Used*100/a.Total, fmtBytes(a.Used), fmtBytes(a.Total))})
	}
	if !check {
		det.Facts = append(det.Facts, notify.Fact{Label: "Beginn", Value: fmtWhen(a.Start)}, notify.Fact{Label: "Fertig", Value: fmtWhen(a.End)},
			notify.Fact{Label: "Dauer", Value: durationText(a.End-a.Start, a.CopySecs)})
		if a.Copied != "" {
			det.Facts = append(det.Facts, notify.Fact{Label: "Kopiert", Value: a.Copied})
		}
		if a.Verify && status == "ok" {
			det.Facts = append(det.Facts, notify.Fact{Label: "Prüfung", Value: "alle Daten wurden zurückgelesen und sind lesbar"})
		}
	}
	det.Log = problems
	sev := "info"
	if status == "warning" {
		sev = "warning"
	}
	var title, msg string
	switch {
	case check && status == "ok":
		title = "Archiv-Festplatte „" + a.Name + "“ geprüft – alles lesbar"
		msg = "Die Archiv-Festplatte „" + a.Name + "“ wurde vollständig gelesen, alle Sicherungen darauf sind in Ordnung. " +
			"Sie ist ausgehängt und kann abgezogen und wieder eingelagert werden."
	case check:
		title = "Archiv-Festplatte „" + a.Name + "“ geprüft – mit Hinweisen"
		msg = "Die Archiv-Festplatte „" + a.Name + "“ wurde gelesen, dabei gab es Auffälligkeiten. Bitte leiten Sie diese Nachricht an Ihren IT-Betreuer weiter."
	case status == "ok":
		checked := ""
		if a.Verify {
			checked = " und vollständig geprüft"
		}
		title = "Archiv-Sicherung fertig – Festplatte „" + a.Name + "“ kann abgezogen werden"
		msg = "Die Sicherungen wurden auf die Festplatte „" + a.Name + "“ kopiert" + checked + ". " +
			"Sie ist ausgehängt und kann jetzt abgezogen werden. Bitte beschriften Sie die Festplatte mit „" + a.Name + "“ und dem heutigen Datum " +
			"und bewahren Sie sie trocken und sicher auf, am besten außer Haus."
	default:
		title = "Archiv-Sicherung auf „" + a.Name + "“ fertig – mit Hinweisen"
		msg = "Die Sicherungen wurden auf die Festplatte „" + a.Name + "“ kopiert, dabei gab es aber Auffälligkeiten. " +
			"Bitte leiten Sie diese Nachricht an Ihren IT-Betreuer weiter, bevor Sie die Festplatte einlagern."
	}
	c.EventDetails(sev, archiveSource, title, msg, det)
}

// archiveSource names the archive copies in events and mails.
const archiveSource = "Archiv-Sicherung"

// ForgetArchive removes the archive from the list. With forgetStore the removable datastore
// is also taken out of the Backup Server's configuration (the data on the disk stays).
func (c *Core) ForgetArchive(ctx context.Context, id string, forgetStore bool) error {
	a, err := c.GetArchive(id)
	if err != nil {
		return err
	}
	if c.ArchiveBusy(id) {
		return errors.New("Für diese Platte läuft gerade etwas.")
	}
	if forgetStore && a.Store != "" && a.Disk == "" {
		pbs, _, err := c.PBS(a.ServerID)
		if err != nil {
			return err
		}
		if err := pbs.DeleteDatastore(ctx, a.Store, false); err != nil && !prox.IsStatus(err, 404) && !strings.Contains(err.Error(), "no such datastore") {
			return fmt.Errorf("Datastore aus der Konfiguration nehmen: %w", err)
		}
	}
	return c.St.Delete(KindArchive, id)
}

// UpdateArchiveInfo changes the descriptive fields (name, storage location, note).
func (c *Core) UpdateArchiveInfo(id, name, location, note string) (Archive, error) {
	if _, err := c.GetArchive(id); err != nil {
		return Archive{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Archive{}, errors.New("Bitte einen Namen angeben.")
	}
	return c.updateArchive(id, func(a *Archive) {
		a.Name, a.Location, a.Note = name, strings.TrimSpace(location), strings.TrimSpace(note)
	}), nil
}

// busyArchives returns the archives with work in progress (for the task bar).
func (c *Core) busyArchives() []Archive {
	list, _ := c.Archives()
	var out []Archive
	for _, a := range list {
		if a.Phase != "" && c.ArchiveBusy(a.ID) {
			out = append(out, a)
		}
	}
	return out
}
