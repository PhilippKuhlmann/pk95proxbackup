package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
)

// Damaged rotation disks: when a copy fails because files on the disk can no longer be read
// (owner files with garbage, checksum or I/O errors), the disk is marked damaged. "Neu
// aufsetzen" formats it, creates the removable datastore again under the same name, copies
// everything anew and then reads it all back after unmounting and mounting again, so that a
// broken USB enclosure shows up. Sets that keep only the newest backup on the disk can do this
// on their own.

var damageRe = regexp.MustCompile(`(?i)did not contain valid utf-8|not a valid user id|structure needs cleaning|input/output error|bad message|efsbadcrc|checksum|corrupt|read-only file system`)

// damageOf returns the first log line that points to a damaged filesystem ("" = none).
func damageOf(lines []prox.TaskLine) string {
	for _, l := range lines {
		if damageRe.MatchString(l.T) {
			t := l.T
			// "2026-11-12T22:30:23+01:00: [vm/101]: …" → without the time stamp
			if i := strings.Index(t, ": "); i > 0 && i < 30 && strings.Contains(t[:i], "T") {
				t = t[i+2:]
			}
			return truncateText(t, 300)
		}
	}
	return ""
}

// SingleCopy reports whether the disks keep only the newest backup of each guest, so that
// formatting one loses nothing that is not on the source anyway.
func (r Rotation) SingleCopy() bool {
	return r.TransferLast == 1 && r.KeepLast == 1 && r.KeepDaily == 0 && r.KeepWeekly == 0 && r.KeepMonthly == 0
}

const autoRepairPause = 12 * time.Hour

// noteDamage marks a disk damaged after a failed copy and, if the set allows it, starts the
// repair. Returns the text for the notification.
func (c *Core) noteDamage(r Rotation, d RotDisk, rt *diskRuntime, info string) string {
	key := rtKey(r, d)
	c.mu.Lock()
	rt.Damaged = info
	failedBefore := rt.RepairFailed
	recent := time.Since(time.Unix(rt.RepairAt, 0)) < autoRepairPause
	c.mu.Unlock()
	c.saveRuntime(key, rt)
	msg := "Die Daten auf der Festplatte „" + d.Label + "“ lassen sich nicht mehr richtig lesen – das Dateisystem auf der Platte ist beschädigt. "
	switch {
	case failedBefore:
		return msg + "Schon nach dem letzten Neuaufsetzen war die Platte fehlerhaft: Bitte USB-Gehäuse, Kabel oder Platte tauschen."
	case r.AutoRepair && r.SingleCopy() && !recent:
		go func() {
			if err := c.RebuildDisk(context.Background(), r, d, true); err != nil {
				c.EventDetails("critical", rotationSource, "Festplatte „"+d.Label+"“ konnte nicht neu aufgesetzt werden", err.Error(),
					&notify.Details{Status: "error", Link: "/wechselplatten"})
			}
		}()
		return msg + "Die Appliance setzt die Platte jetzt automatisch neu auf und wiederholt die Sicherung. Bitte die Platte angeschlossen lassen."
	case r.AutoRepair && r.SingleCopy():
		return msg + "Sie wurde vor kurzem schon automatisch neu aufgesetzt; bitte USB-Gehäuse, Kabel oder Platte prüfen."
	}
	return msg + "Empfehlung: unter Wechselplatten „Neu aufsetzen“ wählen. Die Platte wird neu formatiert und neu befüllt; die Sicherungen auf dem Backup-Server bleiben erhalten."
}

// RebuildDisk formats a rotation disk and fills it anew. It returns once the copy has been
// started; the check afterwards runs in the aftercare.
func (c *Core) RebuildDisk(ctx context.Context, r Rotation, d RotDisk, auto bool) error {
	key := rtKey(r, d)
	if !c.tryBusy(key) {
		return errors.New("Für diese Platte läuft gerade etwas.")
	}
	rt := c.runtime(key)
	err := c.rebuildDisk(ctx, r, d, rt, auto)
	c.setPhase(key, rt, "")
	c.doneBusy(key)
	if err != nil {
		return err
	}
	// Copy now (mounting starts the job when "beim Anstecken kopieren" is on).
	if err := c.RunRotationDisk(ctx, r, d); err != nil {
		return fmt.Errorf("Kopie starten: %w", err)
	}
	return nil
}

func (c *Core) rebuildDisk(ctx context.Context, r Rotation, d RotDisk, rt *diskRuntime, auto bool) error {
	key := rtKey(r, d)
	pbs, _, err := c.PBS(r.ServerID)
	if err != nil {
		return err
	}
	c.setPhase(key, rt, "rebuilding")
	cfgs, err := pbs.Datastores(ctx)
	if err != nil {
		return err
	}
	uuid := ""
	for _, cfg := range cfgs {
		if cfg.Name == d.Store {
			uuid = cfg.BackingDevice
		}
	}
	if uuid == "" {
		return fmt.Errorf("%s ist kein Wechsel-Datastore auf dem Backup-Server", d.Store)
	}
	disks, err := pbs.Disks(ctx)
	if err != nil {
		return err
	}
	var disk prox.Disk
	fs := "ext4"
	for _, dk := range disks {
		for _, p := range dk.Partitions {
			if strings.EqualFold(p.UUID, uuid) {
				disk = dk
				if p.Filesystem == "xfs" {
					fs = "xfs"
				}
			}
		}
	}
	if disk.Name == "" {
		return fmt.Errorf("Die Festplatte „%s“ ist nicht angeschlossen.", d.Label)
	}
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
	if err := c.PrepareDisk(ctx, r.ServerID, disk.Name, d.Store, fs, true); err != nil {
		return err
	}
	if err := c.ApplyRotation(ctx, r); err != nil {
		return fmt.Errorf("Kopierjob anlegen: %w", err)
	}
	c.mu.Lock()
	rt.Damaged, rt.RepairAt, rt.VerifyNext, rt.DoneAt = "", time.Now().Unix(), true, 0
	// A new disk: failures of the old one are history.
	rt.LastFail, rt.LastError = 0, ""
	rt.DamageSeen = rt.SeenUPID // the old failed run is dealt with
	c.mu.Unlock()
	c.saveRuntime(key, rt)
	how := "Auf Wunsch"
	if auto {
		how = "Automatisch"
	}
	c.EventDetails("info", rotationSource, "Festplatte „"+d.Label+"“ neu aufgesetzt",
		how+" wurde die Platte neu formatiert. Jetzt wird sie neu befüllt und danach vollständig zurückgelesen, um zu prüfen, ob sie zuverlässig speichert.",
		&notify.Details{Status: "ok", Link: "/wechselplatten", Facts: []notify.Fact{{Label: "Festplatte", Value: "„" + d.Label + "“"},
			{Label: "Gerät", Value: strings.TrimSpace(disk.Vendor+" "+disk.Model) + " (" + disk.Name + ")"}}})
	return nil
}

// checkRebuilt reads a freshly filled disk back from the disk itself: unmount, mount (without
// starting the copy job again), verify everything. Returns "" or the problem.
func (c *Core) checkRebuilt(ctx context.Context, pbs *prox.PBS, r Rotation, d RotDisk, rt *diskRuntime) string {
	key := rtKey(r, d)
	c.setPhase(key, rt, "verifying")
	job := r.SyncJobID(d)
	if r.RunOnMount {
		_ = pbs.UpdateJob(ctx, "sync", job, prox.Params{"run-on-mount": false})
		defer func() { _ = c.ApplyRotation(context.WithoutCancel(ctx), r) }()
	}
	if err := c.unmountStore(ctx, pbs, d.Store); err != nil {
		return "Aushängen vor dem Zurücklesen: " + err.Error()
	}
	upid, err := pbs.Mount(ctx, d.Store)
	if err != nil {
		return "Wieder einhängen: " + err.Error()
	}
	if _, err := pbs.WaitTask(ctx, upid, 5*time.Minute); err != nil {
		return "Wieder einhängen: " + err.Error()
	}
	upid, err = pbs.StartTask(ctx, "/admin/datastore/"+url.PathEscape(d.Store)+"/verify", prox.Params{"ignore-verified": false})
	if err != nil {
		return "Prüfung starten: " + err.Error()
	}
	st, err := pbs.WaitTask(ctx, upid, 24*time.Hour)
	if err == nil {
		return ""
	}
	if lines, lerr := pbs.TaskLog(ctx, upid); lerr == nil {
		if info := damageOf(lines); info != "" {
			return info
		}
	}
	if err != nil {
		return err.Error()
	}
	return st.ExitStatus
}
