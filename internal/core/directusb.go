package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
)

// Direct USB backups: Proxmox VE backs up straight onto the USB disk of a set that is plugged
// in, without a datastore in between – for small sites with one or two VMs. Each disk is a
// removable datastore with its own storage in Proxmox VE. The set's backup job always points at
// a disk that is plugged in; the storages of the disks that are away are switched off. After a
// backup the appliance tidies the disk up and unmounts it, as after a copy. A few hours before
// the backup it warns when no disk of the set is plugged in.

const (
	directPrefix = "pk95u-"
	// directWarnBefore: how long before the backup a missing disk is reported.
	directWarnBefore = 3 * time.Hour
	// directMountBefore: a disk that is still plugged in but was unmounted after its last
	// backup is mounted again this long before the next one.
	directMountBefore = 20 * time.Minute
)

// Direct tells whether Proxmox VE backs up straight onto the disks of the set.
func (r Rotation) Direct() bool { return r.Mode == "direct" }

// BackupJobID is the Proxmox VE backup job of a direct set.
func (r Rotation) BackupJobID() string { return directPrefix + r.ID }

var guestListRe = regexp.MustCompile(`^\d+(,\d+)*$`)

// validateDirect checks the fields of a direct set (the copy fields do not apply).
func (r *Rotation) validateDirect(errs map[string]string) {
	delete(errs, "source_store")
	delete(errs, "auto_repair")
	r.SourceServer, r.SourceStore, r.SourceNS = "", "", ""
	r.RunOnMount, r.TransferLast, r.AutoRepair = false, 0, false
	if r.Cluster == "" {
		errs["cluster"] = "Proxmox VE wählen, das sichert."
	}
	if r.Time == "" {
		errs["time"] = "Uhrzeit der Sicherung angeben, z. B. 21:00."
	}
	r.Guests = strings.Join(strings.Fields(strings.ReplaceAll(r.Guests, ",", " ")), ",")
	if r.Guests != "" && !guestListRe.MatchString(r.Guests) {
		errs["guests"] = "IDs mit Komma getrennt, z. B. 100,101 – leer = alle VMs und Container."
	}
}

func directJobParams(r Rotation, storage string) prox.Params {
	p := prox.Params{"schedule": r.Time, "storage": storage, "mode": "snapshot", "enabled": 1,
		"comment": "PK95 USB direkt: " + r.Name, "notes-template": "{{guestname}}"}
	if r.Guests == "" {
		p["all"] = 1
	} else {
		p["all"], p["vmid"] = 0, r.Guests
	}
	return p
}

// owns tells whether a Proxmox VE storage belongs to a disk of the set.
func (r Rotation) owns(storage string) bool {
	return storage != "" && slices.ContainsFunc(r.Disks, func(d RotDisk) bool { return d.Storage == storage })
}

// applyDirect creates or updates the backup job of a direct set. Without a disk there is no
// job yet: it comes with the first disk.
func (c *Core) applyDirect(ctx context.Context, r Rotation) error {
	pve, _, err := c.PVE(r.Cluster)
	if err != nil {
		return err
	}
	jobs, err := pve.BackupJobs(ctx)
	if err != nil {
		return err
	}
	var cur *prox.BackupJob
	for i := range jobs {
		if jobs[i].ID == r.BackupJobID() {
			cur = &jobs[i]
		}
	}
	storage := ""
	if cur != nil && r.owns(cur.Storage) {
		storage = cur.Storage
	}
	for _, d := range r.Disks {
		if storage == "" && d.Storage != "" {
			storage = d.Storage
		}
	}
	if storage == "" {
		return nil
	}
	p := directJobParams(r, storage)
	if cur != nil {
		if r.Guests == "" && cur.VMID != "" {
			p["delete"] = "vmid"
		}
		err = pve.UpdateBackupJob(ctx, r.BackupJobID(), p)
	} else {
		p["id"] = r.BackupJobID()
		err = pve.CreateBackupJob(ctx, p)
	}
	if err != nil {
		return fmt.Errorf("Sicherungsjob in Proxmox VE: %w", err)
	}
	return nil
}

// RemoveDirect deletes the backup job of a direct set. The storages stay (for restoring from
// the disks).
func (c *Core) RemoveDirect(ctx context.Context, r Rotation) error {
	pve, _, err := c.PVE(r.Cluster)
	if err != nil {
		return err
	}
	if err := pve.DeleteBackupJob(ctx, r.BackupJobID()); err != nil && !prox.IsStatus(err, 404) && !strings.Contains(err.Error(), "does not exist") {
		return err
	}
	return nil
}

// directTarget picks the disk the next backup goes to among the plugged-in ones: the one due
// today by the swap plan, a mounted one before an unmounted, else the one whose last backup is
// oldest.
func directTarget(st RotationState) (DiskState, bool) {
	var best DiskState
	found := false
	rank := func(d DiskState) int {
		n := 0
		if d.Mounted {
			n += 2
		}
		if d.Today {
			n += 4
		}
		return n
	}
	for _, d := range st.Disks {
		if !d.Present || d.Storage == "" {
			continue
		}
		if !found || rank(d) > rank(best) || rank(d) == rank(best) && d.LastOK < best.LastOK {
			best, found = d, true
		}
	}
	return best, found
}

// scheduleDirect keeps the direct sets in line with the plugged-in disks (called every minute).
func (c *Core) scheduleDirect(ctx context.Context, ov *Overview, now time.Time) {
	running := map[string]bool{} // cluster → a backup is running
	for _, t := range ov.Tasks {
		if t.Type == "vzdump" && t.EndTime == 0 {
			running[strings.TrimPrefix(t.Source, "pve:")] = true
		}
	}
	busy := map[string]bool{}
	for _, st := range ov.Rotations {
		r := st.Rotation
		if !r.Direct() || st.Error != "" {
			continue
		}
		pve, _, err := c.PVE(r.Cluster)
		if err != nil {
			continue
		}
		jobs, err := pve.BackupJobs(ctx)
		if err != nil {
			continue
		}
		var job *prox.BackupJob
		for i := range jobs {
			if jobs[i].ID == r.BackupJobID() {
				job = &jobs[i]
			}
		}
		if job == nil {
			// Removed in Proxmox VE by hand: set it up again.
			if err := c.applyDirect(ctx, r); err != nil {
				c.directProblem(r, "Sicherungsjob fehlt", err)
			}
			continue
		}
		// The storages of the disks that are away are switched off, the others on.
		storages, serr := pve.Storages(ctx)
		if pbs, _, err := c.PBS(r.ServerID); err == nil && serr == nil {
			for _, d := range st.Disks {
				if d.Mounted && d.Storage != "" && d.Phase == "" && !running[r.Cluster] {
					if err := c.handOver(ctx, pbs, r, d.RotDisk, storageOwner(storages, d.Storage)); err != nil {
						c.directProblem(r, "Sicherungen auf „"+d.Label+"“ nicht übergeben", err)
					}
				}
			}
		}
		if serr == nil {
			off := map[string]bool{}
			for _, s := range storages {
				off[s.Storage] = s.Disable != 0
			}
			for _, d := range st.Disks {
				if _, known := off[d.Storage]; !known || d.Storage == "" {
					continue
				}
				if want := !d.Present; off[d.Storage] != want {
					_ = pve.SetStorageDisabled(ctx, d.Storage, want)
				}
			}
		}
		target, ok := directTarget(st)
		if running[r.Cluster] {
			for _, d := range st.Disks {
				if d.Storage != "" && d.Storage == job.Storage {
					busy[rtKey(r, d.RotDisk)] = true
				}
			}
			continue // never change the target during a backup
		}
		if ok && job.Storage != target.Storage {
			if err := pve.UpdateBackupJob(ctx, r.BackupJobID(), prox.Params{"storage": target.Storage}); err != nil {
				c.directProblem(r, "Ziel der Sicherung nicht umgestellt", err)
			}
		}
		next := directNext(job.Schedule, now)
		if next.IsZero() || !job.IsEnabled() {
			continue
		}
		// A disk still plugged in after its last backup (and unmounted then) is mounted again.
		if ok && !target.Mounted && !target.Orphan && target.Phase == "" && next.Sub(now) <= directMountBefore {
			key := rtKey(r, target.RotDisk)
			if c.tryBusy(key) {
				if pbs, _, err := c.PBS(r.ServerID); err == nil {
					if err := c.mountStore(ctx, pbs, target.Store); err != nil {
						c.directProblem(r, "Festplatte „"+target.Label+"“ nicht eingehängt", err)
					}
				}
				c.doneBusy(key)
			}
		}
		if !ok && next.Sub(now) <= directWarnBefore {
			c.warnNoDisk(r, st, next, now)
		}
	}
	c.mu.Lock()
	c.directBusy = busy
	c.mu.Unlock()
}

// copyOwner owns what the copy jobs of the sets write (PBS default of a sync job).
const copyOwner = "root@pam"

// handOver gives the backup groups on a mounted disk to owner: only the owner of a group may add
// backups to it. When a set switches between copying and backing up directly, the groups on its
// disks still belong to the previous writer. It runs once per disk and owner.
func (c *Core) handOver(ctx context.Context, pbs *prox.PBS, r Rotation, d RotDisk, owner string) error {
	key := rtKey(r, d)
	rt := c.runtime(key)
	c.mu.Lock()
	done := rt.Owner == owner
	c.mu.Unlock()
	if done || owner == "" {
		return nil
	}
	groups, err := pbs.Groups(ctx, d.Store)
	if err != nil {
		return err
	}
	moved := 0
	for _, g := range groups {
		if g.Owner == owner {
			continue
		}
		if err := pbs.ChangeOwner(ctx, d.Store, g.Type, g.ID, owner); err != nil {
			return fmt.Errorf("Sicherungen von %s/%s übergeben: %w", g.Type, g.ID, err)
		}
		moved++
	}
	c.mu.Lock()
	rt.Owner = owner
	c.mu.Unlock()
	c.saveRuntime(key, rt)
	if moved > 0 {
		c.Event("info", rotationSource, fmt.Sprintf("Sicherungen auf „%s“ übergeben", d.Label),
			fmt.Sprintf("%d Server-Gruppen auf der Platte gehören jetzt %s, damit dort weiter gesichert werden kann. Die vorhandenen Stände bleiben erhalten.", moved, owner))
	}
	return nil
}

// handOverForCopy gives the groups on a disk back to the copy job when the set backed up
// directly before (a disk that never left the copy set is not touched).
func (c *Core) handOverForCopy(ctx context.Context, pbs *prox.PBS, r Rotation, d RotDisk) {
	rt := c.runtime(rtKey(r, d))
	c.mu.Lock()
	pending := rt.Owner != "" && rt.Owner != copyOwner
	c.mu.Unlock()
	if pending {
		if err := c.handOver(ctx, pbs, r, d, copyOwner); err != nil {
			c.directProblem(r, "Sicherungen auf „"+d.Label+"“ nicht übergeben", err)
		}
	}
}

// storageOwner is the PBS user or token Proxmox VE writes with through a storage.
func storageOwner(storages []prox.Storage, name string) string {
	for _, s := range storages {
		if s.Storage == name {
			return s.Username
		}
	}
	return ""
}

// directNext is the next start of a backup job (zero when the schedule is not understood).
func directNext(schedule string, now time.Time) time.Time {
	cal, err := ParseCalendar(schedule)
	if err != nil {
		return time.Time{}
	}
	if t := cal.Between(now, now.Add(8*24*time.Hour), 1); len(t) > 0 {
		return t[0]
	}
	return time.Time{}
}

// warnNoDisk reports once per backup that no disk of the set is plugged in.
func (c *Core) warnNoDisk(r Rotation, st RotationState, next, now time.Time) {
	key := "directwarn:" + r.ID
	var last int64
	_ = c.St.GetSetting(key, &last)
	if last == next.Unix() {
		return
	}
	_ = c.St.SetSetting(key, next.Unix())
	var labels []string
	for _, d := range st.Disks {
		labels = append(labels, "„"+d.Label+"“")
	}
	when := next.Format("15:04")
	day := "heute"
	if next.Format("2006-01-02") != now.Format("2006-01-02") {
		day = "morgen"
	}
	msg := "Um " + when + " Uhr werden die Server direkt auf eine USB-Festplatte gesichert. Zurzeit ist keine davon angeschlossen"
	if len(labels) > 0 {
		msg += " (" + strings.Join(labels, ", ") + ")"
	}
	msg += ". Bitte schließen Sie eine an – sonst fällt die Sicherung aus."
	c.EventDetails("warning", rotationSource, "Keine USB-Festplatte angeschlossen – Sicherung "+day+" um "+when+" Uhr", msg,
		&notify.Details{Status: "warning", Link: "/wechselplatten", Facts: []notify.Fact{{Label: "Satz", Value: r.Name}, {Label: "Sicherung", Value: day + " um " + when + " Uhr"}}})
}

// directProblem reports a failed step of a direct set once a day.
func (c *Core) directProblem(r Rotation, what string, err error) {
	key := "directproblem:" + r.ID + ":" + what
	day := time.Now().Format("2006-01-02")
	var last string
	_ = c.St.GetSetting(key, &last)
	if last == day {
		return
	}
	_ = c.St.SetSetting(key, day)
	c.EventDetails("warning", rotationSource, r.Name+": "+what, "Die direkte USB-Sicherung braucht hier Hilfe. Bitte leiten Sie diese Nachricht an Ihren IT-Betreuer weiter.",
		&notify.Details{Status: "warning", Link: "/wechselplatten", Log: []string{err.Error()}})
}

// directRun is what the backup tasks of one run wrote onto one storage.
type directRun struct {
	start, end int64
	ok, failed int
	bytes      int64
	failedIDs  []string
	log        []string
}

func (dr *directRun) add(t TaskInfo, guests []prox.GuestBackup, lines []prox.TaskLine) {
	if dr.start == 0 || t.StartTime < dr.start {
		dr.start = t.StartTime
	}
	dr.end = max(dr.end, t.EndTime)
	for _, g := range guests {
		if g.OK {
			dr.ok++
			dr.bytes += prox.ParseSize(g.Transferred)
		} else {
			dr.failed++
			dr.failedIDs = append(dr.failedIDs, strings.TrimSpace(g.VMID+" "+g.Name))
		}
	}
	dr.log = append(dr.log, taskLogExcerpt(lines)...)
}

func (dr *directRun) saved() string {
	s := strconv.Itoa(dr.ok) + " Server"
	if dr.bytes > 0 {
		s += ", " + fmtBytes(dr.bytes) + " übertragen"
	}
	return s
}

// noteDirectRuns takes the finished backups of a run that went onto disks of direct sets: it
// remembers them and starts the aftercare (tidy up, unmount, report).
func (c *Core) noteDirectRuns(ctx context.Context, clusterID string, runs map[string]*directRun) {
	if len(runs) == 0 {
		return
	}
	rots, _ := c.Rotations()
	for _, r := range rots {
		if !r.Direct() || r.Cluster != clusterID {
			continue
		}
		for _, d := range r.Disks {
			dr := runs[d.Storage]
			if d.Storage == "" || dr == nil {
				continue
			}
			key := rtKey(r, d)
			rt := c.runtime(key)
			c.mu.Lock()
			rt.CopyStart, rt.CopyUPID, rt.Saved = dr.start, "", dr.saved()
			if dr.ok > 0 && dr.end > rt.LastOK {
				rt.LastOK, rt.LastError = dr.end, ""
			}
			if dr.failed > 0 || dr.ok == 0 {
				rt.LastFail = dr.end
				rt.LastError = "nicht gesichert: " + strings.Join(dr.failedIDs, ", ")
				if dr.failed == 0 {
					rt.LastError = "Sicherung fehlgeschlagen"
				}
			}
			c.mu.Unlock()
			c.saveRuntime(key, rt)
			// Old runs (first look after an update, or before the disk joined): only remember.
			if time.Since(time.Unix(dr.end, 0)) > 2*time.Hour || dr.end < d.Added {
				continue
			}
			if dr.ok == 0 {
				c.EventDetails("critical", rotationSource, "USB-Sicherung auf „"+d.Label+"“ fehlgeschlagen",
					"Die Server konnten nicht auf die Festplatte „"+d.Label+"“ gesichert werden. Bitte lassen Sie die Festplatte angeschlossen und leiten Sie diese Nachricht an Ihren IT-Betreuer weiter.",
					&notify.Details{Status: "error", Link: "/wechselplatten", Log: dr.log, Facts: []notify.Fact{
						{Label: "Festplatte", Value: "„" + d.Label + "“"}, {Label: "Zeitpunkt", Value: fmtWhen(dr.end)}}})
				continue
			}
			if dr.failed > 0 {
				c.EventDetails("warning", rotationSource, "Nicht alle Server auf „"+d.Label+"“ gesichert",
					"Auf die Festplatte „"+d.Label+"“ wurden "+dr.saved()+" gesichert, aber nicht: "+strings.Join(dr.failedIDs, ", ")+". Bitte leiten Sie diese Nachricht an Ihren IT-Betreuer weiter.",
					&notify.Details{Status: "warning", Link: "/wechselplatten", Log: dr.log})
			}
			pbs, _, err := c.PBS(r.ServerID)
			if err != nil {
				continue
			}
			go func() {
				for i := 0; !c.tryBusy(key); i++ {
					if i >= 1800 {
						return
					}
					time.Sleep(time.Second)
				}
				defer c.doneBusy(key)
				c.aftercare(context.WithoutCancel(ctx), pbs, r, d, rt)
			}()
		}
	}
}

// directLabel names the disk of a direct set behind a storage ("" when none).
func (c *Core) directLabel(clusterID, storage string) string {
	if storage == "" {
		return ""
	}
	rots, _ := c.Rotations()
	for _, r := range rots {
		if r.Direct() && r.Cluster == clusterID {
			for _, d := range r.Disks {
				if d.Storage == storage {
					return d.Label
				}
			}
		}
	}
	return ""
}

// runDirect backs up onto a disk now: mount it, point the job at it and start the job.
func (c *Core) runDirect(ctx context.Context, r Rotation, d RotDisk) error {
	if d.Storage == "" {
		return errors.New("Die Platte ist in Proxmox VE noch nicht eingetragen – den Satz einmal speichern.")
	}
	pbs, _, err := c.PBS(r.ServerID)
	if err != nil {
		return err
	}
	if err := c.mountStore(ctx, pbs, d.Store); err != nil {
		return fmt.Errorf("Platte %s einhängen: %w (steckt sie?)", d.Label, err)
	}
	pve, _, err := c.PVE(r.Cluster)
	if err != nil {
		return err
	}
	_ = pve.SetStorageDisabled(ctx, d.Storage, false)
	if storages, err := pve.Storages(ctx); err == nil {
		if err := c.handOver(ctx, pbs, r, d, storageOwner(storages, d.Storage)); err != nil {
			return err
		}
	}
	if err := pve.UpdateBackupJob(ctx, r.BackupJobID(), prox.Params{"storage": d.Storage}); err != nil {
		// The job may be gone (removed in Proxmox VE): set it up again first.
		if err := c.applyDirect(ctx, r); err != nil {
			return err
		}
		if err := pve.UpdateBackupJob(ctx, r.BackupJobID(), prox.Params{"storage": d.Storage}); err != nil {
			return err
		}
	}
	rt := c.runtime(rtKey(r, d))
	c.mu.Lock()
	rt.DoneAt = 0
	c.mu.Unlock()
	_, errs, err := c.RunBackupJob(ctx, r.Cluster, r.BackupJobID())
	c.RequestRefresh()
	if err != nil {
		return err
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

var (
	// ErrJobNotFound: the backup job does not exist (any more).
	ErrJobNotFound = errors.New("Job nicht gefunden.")
	// ErrJobEmpty: the backup job covers no guest.
	ErrJobEmpty = errors.New("Der Job umfasst keine VM und keinen Container.")
)

// RunBackupJob starts a backup job of Proxmox VE now, on every node that has guests of it. It
// returns the started tasks and the nodes where starting failed.
func (c *Core) RunBackupJob(ctx context.Context, clusterID, jobID string) (upids, errs []string, err error) {
	pve, _, err := c.PVE(clusterID)
	if err != nil {
		return nil, nil, err
	}
	jobs, err := pve.BackupJobs(ctx)
	if err != nil {
		return nil, nil, err
	}
	var job *prox.BackupJob
	for i := range jobs {
		if jobs[i].ID == jobID {
			job = &jobs[i]
		}
	}
	if job == nil {
		return nil, nil, ErrJobNotFound
	}
	guests, err := pve.Guests(ctx)
	if err != nil {
		return nil, nil, err
	}
	include, exclude := idSet(job.VMID), idSet(job.Exclude)
	perNode := map[string][]string{}
	for _, g := range guests {
		if g.Template != 0 || (job.Node != "" && g.Node != job.Node) {
			continue
		}
		id := strconv.Itoa(g.VMID)
		if job.All != 0 && !exclude[id] || job.All == 0 && include[id] {
			perNode[g.Node] = append(perNode[g.Node], id)
		}
	}
	if len(perNode) == 0 {
		return nil, nil, ErrJobEmpty
	}
	for node, ids := range perNode {
		p := prox.Params{"vmid": strings.Join(ids, ","), "storage": job.Storage, "mode": job.Mode}
		if job.NotesTemplate != "" {
			p["notes-template"] = job.NotesTemplate
		}
		if job.PruneBackups != "" {
			p["prune-backups"] = job.PruneBackups
		}
		upid, err := pve.Vzdump(ctx, node, p)
		if err != nil {
			errs = append(errs, node+": "+err.Error())
			continue
		}
		upids = append(upids, upid)
	}
	if len(upids) == 0 {
		return nil, errs, errors.New(strings.Join(errs, "; "))
	}
	return upids, errs, nil
}

func idSet(list string) map[string]bool {
	m := map[string]bool{}
	for _, x := range strings.Split(list, ",") {
		if x = strings.TrimSpace(x); x != "" {
			m[x] = true
		}
	}
	return m
}
