package core

import (
	"fmt"
	"sync"
	"time"

	"pk95proxbackup/internal/notify"
)

// Planned restarts: when the appliance restarts a Backup Server itself (by hand or after an
// automatic update), "not reachable" and "reachable again" are no news. The appliance keeps
// quiet while the server is away, reports once when it is back ("restarted, back after two
// minutes", with the updates before it) and only raises an alarm when it does not come back
// within 15 minutes.

const restartGrace = 15 * time.Minute

// PlannedRestart is a restart the appliance triggered.
type PlannedRestart struct {
	Since  int64  `json:"since"`
	Until  int64  `json:"until"`
	By     string `json:"by"`
	Before string `json:"before,omitempty"` // what happened before (updates installed)
	Down   bool   `json:"down"`             // seen unreachable
	Late   bool   `json:"late"`             // reported as not back
}

var (
	restartMu    sync.Mutex
	processStart = time.Now()
)

func (c *Core) plannedRestarts() map[string]PlannedRestart {
	m := map[string]PlannedRestart{}
	_ = c.St.GetSetting("planned-restarts", &m)
	return m
}

func (c *Core) planRestart(id, by, before string, now time.Time) {
	restartMu.Lock()
	defer restartMu.Unlock()
	m := c.plannedRestarts()
	m[id] = PlannedRestart{Since: now.Unix(), Until: now.Add(restartGrace).Unix(), By: by, Before: before}
	_ = c.St.SetSetting("planned-restarts", m)
}

// followRestarts watches the planned restarts: quiet while away, one message when back, an
// alarm when the server does not come back in time.
func (c *Core) followRestarts(ov *Overview, now time.Time) {
	restartMu.Lock()
	defer restartMu.Unlock()
	m := c.plannedRestarts()
	if len(m) == 0 {
		return
	}
	state := map[string]ServerState{}
	for _, s := range ov.Servers {
		state[s.ID] = s
	}
	changed := false
	for id, r := range m {
		s, ok := state[id]
		if !ok {
			delete(m, id)
			changed = true
			continue
		}
		at := time.Unix(r.Since, 0).Format("15:04")
		if !s.Online {
			if !r.Down {
				r.Down, changed = true, true
			}
			if now.Unix() > r.Until && !r.Late {
				r.Late, changed = true, true
				c.EventDetails("critical", s.Name, "Backup-Server nach Neustart nicht wieder erreichbar",
					fmt.Sprintf("Neu gestartet um %s (von %s), seit %d Minuten keine Antwort. %s", at, r.By, int(now.Sub(time.Unix(r.Since, 0)).Minutes()), s.Error),
					&notify.Details{Status: "error", Link: "/verbindungen"})
			}
			m[id] = r
			continue
		}
		booted := s.Uptime > 0 && now.Unix()-s.Uptime >= r.Since-60
		restarted := r.Down || booted || s.Local && processStart.Unix() > r.Since
		if !restarted && now.Unix() <= r.Until {
			continue
		}
		delete(m, id)
		changed = true
		who := "Backup-Server"
		if s.Local {
			who = "Appliance"
		}
		if !restarted {
			c.EventDetails("warning", s.Name, "Neustart hat nicht stattgefunden",
				fmt.Sprintf("%sDer Neustart um %s (von %s) wurde ausgelöst, der %s lief aber durchgehend weiter.", prefixed(r.Before), at, r.By, who),
				&notify.Details{Status: "warning", Link: "/einstellungen/aktualisierung"})
			continue
		}
		mins := int(now.Sub(time.Unix(r.Since, 0)).Minutes() + 0.5)
		back := "nach etwa einer Minute"
		if mins > 1 {
			back = fmt.Sprintf("nach etwa %d Minuten", mins)
		}
		title := who + " neu gestartet"
		if r.Before != "" {
			title = "Updates eingespielt und neu gestartet"
		}
		if r.Late {
			title = "Backup-Server wieder erreichbar"
		}
		c.EventDetails("info", s.Name, title, fmt.Sprintf("%sNeustart um %s (von %s), %s wieder erreichbar.", prefixed(r.Before), at, r.By, back),
			&notify.Details{Status: "ok", Link: "/verbindungen"})
	}
	if changed {
		_ = c.St.SetSetting("planned-restarts", m)
	}
}

func prefixed(s string) string {
	if s == "" {
		return ""
	}
	return s + " "
}

// plannedRestartIDs are the servers whose unreachability is expected right now.
func (c *Core) plannedRestartIDs() map[string]bool {
	out := map[string]bool{}
	for id := range c.plannedRestarts() {
		out[id] = true
	}
	return out
}

// pendingUpdate holds the result of an automatic update that is reported together with what
// follows (reload of the services or restart).
func pendingKey(id string) string { return "upd-pending:" + id }

func (c *Core) setPendingUpdate(id, text string) { _ = c.St.SetSetting(pendingKey(id), text) }

func (c *Core) takePendingUpdate(id string) string {
	var s string
	_ = c.St.GetSetting(pendingKey(id), &s)
	if s != "" {
		_ = c.St.SetSetting(pendingKey(id), "")
	}
	return s
}
