package core

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
)

// A failed verification of PBS is not always a damaged backup: a snapshot the retention
// removes while the check runs (it takes hours on a large datastore) fails as "manifest load
// error". The appliance reads the log, compares the failed snapshots with what is still there
// and tells harmless failures from damaged backups.

// VerifyResult is the reading of one failed verification task.
type VerifyResult struct {
	UPID       string          `json:"upid"`
	ServerID   string          `json:"server_id"`
	ServerName string          `json:"server_name"`
	Store      string          `json:"store"`
	End        int64           `json:"end"`
	State      string          `json:"state"` // harmless, damaged, unknown (log gave no answer)
	Vanished   []string        `json:"vanished,omitempty"`
	Damaged    []DamagedBackup `json:"damaged,omitempty"`
}

// DamagedBackup is a snapshot (Time 0: a whole group) whose data could not be read back.
type DamagedBackup struct {
	NS    string `json:"ns,omitempty"`
	Type  string `json:"type"`
	ID    string `json:"id"`
	Time  int64  `json:"time,omitempty"`
	Name  string `json:"name,omitempty"`
	Error string `json:"error,omitempty"`
}

const verifyResultsKeep = 60 * 24 * time.Hour

func isVerifyTask(typ string) bool {
	return typ == "verify" || typ == "verificationjob" || typ == "verify_group" || typ == "verify_snapshot"
}

// VerifyResults returns the readings of failed verifications, by task.
func (c *Core) VerifyResults() map[string]VerifyResult {
	m := map[string]VerifyResult{}
	_ = c.St.GetSetting("verify-results", &m)
	return m
}

func (c *Core) saveVerifyResult(r VerifyResult) {
	m := c.VerifyResults()
	m[r.UPID] = r
	for k, v := range m {
		if time.Since(time.Unix(v.End, 0)) > verifyResultsKeep {
			delete(m, k)
		}
	}
	_ = c.St.SetSetting("verify-results", m)
}

// RecentDamage lists damaged-backup findings of the last days, newest first.
func (c *Core) RecentDamage(within time.Duration) []VerifyResult {
	out := []VerifyResult{}
	for _, r := range c.VerifyResults() {
		if r.State == "damaged" && time.Since(time.Unix(r.End, 0)) <= within {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].End > out[j].End })
	return out
}

// annotateTasks marks failed verifications the appliance found harmless or damaging, and reader
// errors that were none (see readerOutcome).
func (c *Core) annotateTasks(tasks []TaskInfo) {
	now := time.Now()
	for i, t := range tasks {
		if state, note := readerOutcome(t, tasks, now); state == "harmless" {
			tasks[i].Harmless, tasks[i].Note = true, note
		}
	}
	res := c.VerifyResults()
	if len(res) == 0 {
		return
	}
	for i, t := range tasks {
		r, ok := res[t.UPID]
		if !ok || r.State == "unknown" {
			continue
		}
		if r.State == "harmless" {
			tasks[i].Harmless = true
			tasks[i].Note = fmt.Sprintf("ohne Befund: %d %s während der Prüfung aufgeräumt", len(r.Vanished), plural(len(r.Vanished), "Stand", "Stände"))
		} else {
			tasks[i].Note = fmt.Sprintf("%d %s beschädigt", len(r.Damaged), plural(len(r.Damaged), "Sicherung", "Sicherungen"))
		}
	}
}

// analyzeVerify reads the log of a failed verification and checks which failed snapshots
// still exist. ok is false when the log names no failed snapshot (then it failed otherwise).
func (c *Core) analyzeVerify(ctx context.Context, t TaskInfo) (VerifyResult, bool) {
	kind, id, _ := strings.Cut(t.Source, ":")
	if kind != "pbs" {
		return VerifyResult{}, false
	}
	pbs, srv, err := c.PBS(id)
	if err != nil {
		return VerifyResult{}, false
	}
	store, _, _ := strings.Cut(t.ID, ":")
	// The list of failed snapshots is at the end; their errors may be anywhere.
	_, total, err := pbs.TaskLogRange(ctx, t.UPID, 0, 1)
	if err != nil {
		return VerifyResult{}, false
	}
	start := 0
	if total > 200000 {
		start = total - 2000 // huge log: the list of failures is enough
	}
	lines, _, err := pbs.TaskLogRange(ctx, t.UPID, start, max(total-start, 1))
	if err != nil {
		return VerifyResult{}, false
	}
	vl := prox.ParseVerifyLog(lines)
	if len(vl.Failures) == 0 {
		return VerifyResult{}, false
	}
	type grp struct{ ns, typ, id string }
	exists := map[string]bool{} // ns|type/id/time
	groups := map[grp]bool{}    // groups with at least one snapshot
	listed := map[string]bool{} // namespaces read
	var names map[string]string
	r := VerifyResult{UPID: t.UPID, ServerID: srv.ID, ServerName: srv.Name, Store: store, End: t.EndTime}
	for _, f := range vl.Failures {
		ns, typ, gid, tm, ok := prox.VerifyPath(f.Path)
		if !ok {
			continue
		}
		if !listed[ns] {
			snaps, err := pbs.Snapshots(ctx, store, ns)
			if err != nil {
				return VerifyResult{}, false
			}
			for _, s := range snaps {
				exists[ns+"|"+s.Type+"/"+s.ID+"/"+time.Unix(s.Time, 0).UTC().Format("2006-01-02T15:04:05Z")] = true
				groups[grp{ns, s.Type, s.ID}] = true
			}
			listed[ns] = true
		}
		var at int64
		gone := !groups[grp{ns, typ, gid}]
		if tm != "" {
			gone = !exists[ns+"|"+typ+"/"+gid+"/"+tm]
			if x, err := time.Parse("2006-01-02T15:04:05Z", tm); err == nil {
				at = x.Unix()
			}
		}
		if gone {
			r.Vanished = append(r.Vanished, f.Path)
			continue
		}
		if names == nil {
			names = c.guestNames(ctx)
		}
		r.Damaged = append(r.Damaged, DamagedBackup{NS: ns, Type: typ, ID: gid, Time: at, Name: names[typ+"/"+gid], Error: f.Error})
	}
	r.State = "harmless"
	if len(r.Damaged) > 0 {
		r.State = "damaged"
	}
	return r, true
}

// handleFailedVerify reads a failed verification and reports it in plain words. It returns
// false when the log gives no answer (the generic report of failed tasks takes over).
func (c *Core) handleFailedVerify(ctx context.Context, t TaskInfo) bool {
	r, ok := c.analyzeVerify(ctx, t)
	if !ok {
		c.saveVerifyResult(VerifyResult{UPID: t.UPID, End: t.EndTime, State: "unknown"})
		return false
	}
	c.saveVerifyResult(r)
	where := r.ServerName + " / " + r.Store
	if r.State == "harmless" {
		n := len(r.Vanished)
		c.Event("info", r.ServerName, "Prüfung von "+where+" ohne Befund",
			fmt.Sprintf("Die Prüfung meldete einen Fehler, weil %d %s während der Prüfung planmäßig aufgeräumt %s und nicht mehr gelesen werden %s. Alle übrigen Sicherungen sind in Ordnung.",
				n, plural(n, "Stand", "Stände"), plural(n, "wurde", "wurden"), plural(n, "konnte", "konnten")))
		return true
	}
	var rows []notify.Row
	for _, d := range r.Damaged {
		row := notify.Row{Kind: strings.ToUpper(d.Type), ID: d.ID, Name: d.Name, Status: "error", Time: d.Time, Info: d.Error}
		if d.Time == 0 {
			row.Info = "ganze Gruppe: " + d.Error
		}
		rows = append(rows, row)
	}
	body := fmt.Sprintf("Die Prüfung von %s hat %d %s gefunden, deren Daten nicht vollständig lesbar sind. Aus diesen Ständen lässt sich nicht verlässlich wiederherstellen. "+
		"Bei der nächsten Sicherung schickt Proxmox VE die fehlenden Daten neu – am besten jetzt eine Sicherung der betroffenen Server starten. "+
		"Treten immer wieder neue Schäden auf, die Festplatten des Backup-Servers prüfen.", where, len(r.Damaged), plural(len(r.Damaged), "Sicherung", "Sicherungen"))
	if n := len(r.Vanished); n > 0 {
		body += fmt.Sprintf(" (%d weitere %s während der Prüfung aufgeräumt – das ist kein Fehler.)", n, plural(n, "Stand wurde", "Stände wurden"))
	}
	c.EventDetails("critical", r.ServerName, "Beschädigte Sicherungen auf "+where, body,
		&notify.Details{Status: "error", Link: "/datastores?server=" + r.ServerID, Rows: rows,
			Facts: []notify.Fact{{Label: "Datastore", Value: where}, {Label: "Beschädigt", Value: strconv.Itoa(len(r.Damaged))}}})
	return true
}
