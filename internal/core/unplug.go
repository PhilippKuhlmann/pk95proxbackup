package core

import (
	"context"
	"sync"
	"time"

	"pk95proxbackup/internal/notify"
)

// Disks pulled without unmounting: the datastore stays mounted on the Backup Server while the
// disk is gone (the kernel shuts the filesystem down; data written last can be lost). The
// appliance reports it, removes the stale mount, and after the next copy onto that disk reads
// everything back to see whether it took damage.

type unplugRuntime struct {
	mu   sync.Mutex
	seen map[string]bool // disk key → was an orphan at the last look
}

func (c *Core) checkUnplugged(ctx context.Context, r Rotation, st RotationState, now time.Time) {
	for _, d := range st.Disks {
		key := rtKey(r, d.RotDisk)
		c.unplug.mu.Lock()
		if c.unplug.seen == nil {
			c.unplug.seen = map[string]bool{}
		}
		was := c.unplug.seen[key]
		c.unplug.seen[key] = d.Orphan
		c.unplug.mu.Unlock()
		if !d.Orphan || was {
			continue
		}
		rt := c.runtime(key)
		c.mu.Lock()
		rt.VerifyNext, rt.VerifyWhy, rt.UnpluggedAt = true, "unplugged", now.Unix()
		c.mu.Unlock()
		c.saveRuntime(key, rt)
		body := "Die Festplatte „" + d.Label + "“ wurde vom Backup-Server getrennt, während sie noch eingehängt war. Dabei können zuletzt geschriebene Daten verloren gehen. " +
			"Beim nächsten Anstecken liest die Appliance nach der Kopie alles zur Kontrolle von der Platte zurück. " +
			"Bitte vor dem Abziehen „Aushängen“ wählen"
		if !r.UnmountOnDone {
			body += " – oder beim Satz „Nach der Kopie aushängen“ einschalten, dann ist die Platte nach jeder Kopie schon getrennt"
		}
		body += "."
		c.EventDetails("warning", rotationSource, "Festplatte „"+d.Label+"“ wurde abgezogen, ohne ausgehängt zu werden", body,
			&notify.Details{Status: "warning", Link: "/wechselplatten", Facts: []notify.Fact{{Label: "Festplatte", Value: "„" + d.Label + "“"}, {Label: "Satz", Value: r.Name}, {Label: "Bemerkt", Value: fmtWhen(now.Unix())}}})
		// Remove the stale mount so that the disk mounts again when it is plugged back in.
		if c.tryBusy(key) {
			go func(store string) {
				defer c.doneBusy(key)
				uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Minute)
				defer cancel()
				if pbs, _, err := c.PBS(r.ServerID); err == nil {
					_ = c.unmountStore(uctx, pbs, store)
				}
				c.RequestRefresh()
			}(d.Store)
		}
	}
}

// checkArchivesUnplugged reports archive disks pulled while mounted and removes the stale mount.
func (c *Core) checkArchivesUnplugged(ctx context.Context, now time.Time) {
	list, err := c.Archives()
	if err != nil || len(list) == 0 {
		return
	}
	for _, a := range c.ArchiveStates(ctx) {
		key := "archive/" + a.ID
		c.unplug.mu.Lock()
		if c.unplug.seen == nil {
			c.unplug.seen = map[string]bool{}
		}
		was := c.unplug.seen[key]
		c.unplug.seen[key] = a.Orphan
		c.unplug.mu.Unlock()
		if !a.Orphan || was || a.Busy {
			continue
		}
		c.updateArchive(a.ID, func(x *Archive) {
			x.Problems = append(x.Problems, "Am "+fmtWhen(now.Unix())+" abgezogen, ohne ausgehängt zu werden – beim nächsten Anstecken „Prüfen“ wählen.")
		})
		c.EventDetails("warning", archiveSource, "Archiv-Platte „"+a.Name+"“ wurde abgezogen, ohne ausgehängt zu werden",
			"Dabei können zuletzt geschriebene Daten verloren gehen. Bitte die Platte beim nächsten Mal anstecken und „Prüfen“ wählen; vor dem Abziehen künftig „Aushängen“.",
			&notify.Details{Status: "warning", Link: "/wechselplatten"})
		go func(serverID, store string) {
			uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Minute)
			defer cancel()
			if pbs, _, err := c.PBS(serverID); err == nil {
				_ = c.unmountStore(uctx, pbs, store)
			}
			c.RequestRefresh()
		}(a.ServerID, a.Store)
	}
}
