package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
)

// Maintenance mode: for a while (a host is rebuilt, the company is closed) the appliance holds
// still. It switches off the backup jobs of Proxmox VE and the jobs of the Backup Servers that
// are on (and only those – they are switched on again at the end), starts nothing of its own
// (USB copies, restore tests, updates) and, if wished, sends no notifications: events are still
// written. At the set time it ends by itself and reports what happened in between.

const maintenanceMax = 14 * 24 * time.Hour

// PausedJob is a job the maintenance switched off.
type PausedJob struct {
	Kind   string `json:"kind"`   // pve, pbs
	Server string `json:"server"` // cluster or Backup Server
	Type   string `json:"type"`   // backup, sync, verify, prune
	ID     string `json:"id"`
	Name   string `json:"name"` // for people: "Cluster Zuhause: Jede Nacht alles"
}

// Maintenance is the state of the maintenance mode.
type Maintenance struct {
	Active    bool        `json:"active"`
	Start     int64       `json:"start"`
	Until     int64       `json:"until"`
	Reason    string      `json:"reason"`
	By        string      `json:"by"`
	PauseJobs bool        `json:"pause_jobs"`
	Mute      bool        `json:"mute"`
	Paused    []PausedJob `json:"paused"`
	Muted     int         `json:"muted"`    // notifications held back
	Problems  []string    `json:"problems"` // jobs that could not be switched off
}

// MaintenanceState returns the maintenance mode (inactive when none is set).
func (c *Core) MaintenanceState() Maintenance {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maint == nil {
		var m Maintenance
		_ = c.St.GetSetting("maintenance", &m)
		c.maint = &m
	}
	m := *c.maint
	m.Paused = append([]PausedJob{}, m.Paused...)
	m.Problems = append([]string{}, m.Problems...)
	return m
}

// updateMaintenance changes the state in memory and in the store, one change after the other,
// so that the store always holds the newest state.
func (c *Core) updateMaintenance(f func(m *Maintenance)) Maintenance {
	c.maintWrite.Lock()
	defer c.maintWrite.Unlock()
	_ = c.MaintenanceState() // loaded
	c.mu.Lock()
	m := *c.maint
	f(&m)
	c.maint = &m
	c.mu.Unlock()
	_ = c.St.SetSetting("maintenance", m)
	return m
}

// InMaintenance tells whether the appliance holds still (no scheduled work of its own).
func (c *Core) InMaintenance() bool {
	m := c.MaintenanceState()
	return m.Active && m.PauseJobs
}

// muteNotice reports whether a notification is held back, and counts it.
func (c *Core) muteNotice() bool {
	if m := c.MaintenanceState(); !m.Active || !m.Mute {
		return false
	}
	muted := false
	c.updateMaintenance(func(m *Maintenance) {
		if m.Active && m.Mute {
			m.Muted++
			muted = true
		}
	})
	return muted
}

// StartMaintenance begins (or, when one is on, changes the end of) the maintenance mode.
func (c *Core) StartMaintenance(ctx context.Context, until time.Time, reason, by string, pauseJobs, mute bool) (Maintenance, error) {
	m, _, err := c.startMaintenance(ctx, until, reason, by, pauseJobs, mute, false)
	return m, err
}

// startMaintenance does StartMaintenance; onlyNew leaves a running maintenance as it is
// (started false), as a planned window does.
func (c *Core) startMaintenance(ctx context.Context, until time.Time, reason, by string, pauseJobs, mute, onlyNew bool) (Maintenance, bool, error) {
	c.maintOp.Lock()
	defer c.maintOp.Unlock()
	now := time.Now()
	if !until.After(now) {
		return Maintenance{}, false, errors.New("Das Ende muss in der Zukunft liegen.")
	}
	if until.Sub(now) > maintenanceMax {
		return Maintenance{}, false, errors.New("Höchstens 14 Tage am Stück.")
	}
	if cur := c.MaintenanceState(); cur.Active {
		if onlyNew {
			return cur, false, nil
		}
		// Only the end, the reason and muting change; the jobs stay as they are.
		cur = c.updateMaintenance(func(m *Maintenance) {
			m.Until, m.Mute = until.Unix(), mute
			if strings.TrimSpace(reason) != "" {
				m.Reason = strings.TrimSpace(reason)
			}
		})
		c.Record("info", "Wartung", "Wartung geändert: bis "+fmtWhen(until.Unix()), "von "+by)
		return cur, true, nil
	}
	m := Maintenance{Active: true, Start: now.Unix(), Until: until.Unix(), Reason: strings.TrimSpace(reason), By: by, PauseJobs: pauseJobs, Mute: mute}
	if pauseJobs {
		m.Paused, m.Problems = c.pauseJobs(ctx)
	}
	body := "Bis " + fmtWhen(m.Until) + "."
	if m.Reason != "" {
		body = m.Reason + " – " + body
	}
	var what []string
	if pauseJobs {
		what = append(what, fmt.Sprintf("%d Jobs ausgesetzt, keine USB-Kopien, Restore-Tests und Updates", len(m.Paused)))
	}
	if mute {
		what = append(what, "keine Benachrichtigungen bis zum Ende")
	}
	if len(what) > 0 {
		body += " " + strings.Join(what, "; ") + "."
	}
	det := &notify.Details{Status: "warning", Link: "/", Facts: []notify.Fact{{Label: "Von", Value: by}, {Label: "Bis", Value: fmtWhen(m.Until)}}}
	if len(m.Problems) > 0 {
		det.Log = m.Problems
	}
	// Sent before muting starts, so that everyone knows.
	c.EventDetails("warning", "Wartung", "Wartung begonnen", body, det)
	m = c.updateMaintenance(func(cur *Maintenance) { *cur = m })
	c.RequestRefresh()
	return m, true, nil
}

// pauseJobs switches off every scheduled job that is on and returns them.
func (c *Core) pauseJobs(ctx context.Context) (paused []PausedJob, problems []string) {
	clusters, _ := c.Clusters()
	for _, cl := range clusters {
		pve := c.PVEClient(cl)
		jobs, err := pve.BackupJobs(ctx)
		if err != nil {
			problems = append(problems, cl.Name+": "+err.Error())
			continue
		}
		for _, j := range jobs {
			if !j.IsEnabled() {
				continue
			}
			if err := pve.UpdateBackupJob(ctx, j.ID, prox.Params{"enabled": 0}); err != nil {
				problems = append(problems, cl.Name+" / "+j.ID+": "+err.Error())
				continue
			}
			paused = append(paused, PausedJob{Kind: "pve", Server: cl.ID, Type: "backup", ID: j.ID, Name: cl.Name + ": " + cmp.Or(j.Comment, j.ID)})
		}
	}
	servers, _ := c.Servers()
	for _, srv := range servers {
		pbs := c.PBSClient(srv)
		for _, kind := range []string{"sync", "verify", "prune"} {
			jobs, err := pbs.Jobs(ctx, kind)
			if err != nil {
				problems = append(problems, srv.Name+": "+err.Error())
				break
			}
			for _, j := range jobs {
				if j.Disable || j.Schedule == "" && !j.RunOnMount {
					continue // off, or only by hand
				}
				if err := pbs.UpdateJob(ctx, kind, j.ID, prox.Params{"disable": true}); err != nil {
					problems = append(problems, srv.Name+" / "+j.ID+": "+err.Error())
					continue
				}
				paused = append(paused, PausedJob{Kind: "pbs", Server: srv.ID, Type: kind, ID: j.ID, Name: srv.Name + ": " + cmp.Or(j.Comment, j.ID)})
			}
		}
	}
	return paused, problems
}

// EndMaintenance switches the paused jobs on again and reports what happened meanwhile.
func (c *Core) EndMaintenance(ctx context.Context, why string) (Maintenance, error) {
	c.maintOp.Lock()
	defer c.maintOp.Unlock()
	m := c.MaintenanceState()
	if !m.Active {
		return m, errors.New("Es läuft keine Wartung.")
	}
	var problems []string
	var left []PausedJob
	for _, p := range m.Paused {
		var err error
		switch p.Kind {
		case "pve":
			var pve *prox.PVE
			if pve, _, err = c.PVE(p.Server); err == nil {
				err = pve.UpdateBackupJob(ctx, p.ID, prox.Params{"enabled": 1})
			}
		case "pbs":
			var pbs *prox.PBS
			if pbs, _, err = c.PBS(p.Server); err == nil {
				err = pbs.UpdateJob(ctx, p.Type, p.ID, prox.Params{"delete": "disable"})
			}
		}
		if err != nil && !prox.IsStatus(err, 404) && !strings.Contains(err.Error(), "does not exist") {
			problems = append(problems, p.Name+": "+err.Error())
			left = append(left, p)
		}
	}
	ended := c.updateMaintenance(func(cur *Maintenance) { cur.Active, cur.Paused = false, left })
	m.Muted = ended.Muted // counted until now
	// What happened during the maintenance (warnings and errors).
	var rows []notify.Row
	if evs, err := c.St.EventsBetween(ctx, m.Start, time.Now().Unix()+1); err == nil {
		for _, e := range evs {
			if (e.Severity == "warning" || e.Severity == "critical") && e.Source != "Wartung" {
				st := "warning"
				if e.Severity == "critical" {
					st = "error"
				}
				rows = append(rows, notify.Row{Kind: e.Source, ID: e.Title, Status: st, Time: e.TS})
			}
		}
	}
	body := fmt.Sprintf("Die Wartung (%s) ist %s. ", cmp.Or(m.Reason, "ohne Angabe"), why)
	if m.PauseJobs {
		body += fmt.Sprintf("%d Jobs sind wieder eingeschaltet, die nächsten Sicherungen laufen nach Plan. ", len(m.Paused)-len(left))
	}
	if len(rows) == 0 {
		body += "Währenddessen gab es keine Warnungen oder Fehler."
	} else {
		body += fmt.Sprintf("Währenddessen gab es %d Warnungen oder Fehler – bitte prüfen, ob noch etwas offen ist.", len(rows))
	}
	if m.Muted > 0 {
		body += fmt.Sprintf(" %d Benachrichtigungen wurden zurückgehalten.", m.Muted)
	}
	sev := "info"
	det := &notify.Details{Status: "ok", Link: "/ereignisse", Facts: []notify.Fact{{Label: "Beginn", Value: fmtWhen(m.Start)}, {Label: "Ende", Value: fmtWhen(time.Now().Unix())}}}
	if len(rows) > 0 {
		det.RowsTitle, det.Rows = "Während der Wartung", rows
		sev, det.Status = "warning", "warning"
	}
	if len(problems) > 0 {
		det.Log = problems
		sev, det.Status = "critical", "error"
		body += " Diese Jobs ließen sich nicht wieder einschalten – bitte von Hand prüfen."
	}
	c.EventDetails(sev, "Wartung", "Wartung beendet", body, det)
	c.RequestRefresh()
	if len(problems) > 0 {
		return ended, errors.New(strings.Join(problems, "; "))
	}
	return ended, nil
}

// checkMaintenance ends the maintenance at its time (called every minute).
func (c *Core) checkMaintenance(ctx context.Context, now time.Time) {
	if m := c.MaintenanceState(); m.Active && now.Unix() >= m.Until {
		_, _ = c.EndMaintenance(ctx, "planmäßig beendet")
	}
}
