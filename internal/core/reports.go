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

// backupWindow groups the vzdump tasks of one job run: a scheduled job starts one task per
// node at the same time; they are reported together once all of them have ended.
const backupWindow = 10 * time.Minute

// reportBackups turns finished backup runs of Proxmox VE into one event each, with the list
// of guests and how each one went.
func (c *Core) reportBackups(ctx context.Context, ov *Overview) {
	seen := map[string]bool{}
	_ = c.St.GetSetting("seen-vzdump", &seen)
	first := len(seen) == 0
	var done, running []TaskInfo
	for _, t := range ov.Tasks {
		if t.Type != "vzdump" || !strings.HasPrefix(t.Source, "pve:") {
			continue
		}
		if t.EndTime == 0 {
			running = append(running, t)
		} else if !seen[t.UPID] {
			done = append(done, t)
		}
	}
	if len(done) == 0 {
		return
	}
	if first {
		// First look after installation or an update: remember, do not report the past.
		for _, t := range done {
			seen[t.UPID] = true
		}
		_ = c.St.SetSetting("seen-vzdump", seen)
		return
	}
	sort.Slice(done, func(i, j int) bool { return done[i].StartTime < done[j].StartTime })
	// group by cluster and start time
	type group struct{ tasks []TaskInfo }
	var groups []*group
	for _, t := range done {
		var g *group
		for _, x := range groups {
			if x.tasks[0].Source == t.Source && time.Duration(t.StartTime-x.tasks[0].StartTime)*time.Second <= backupWindow {
				g = x
			}
		}
		if g == nil {
			g = &group{}
			groups = append(groups, g)
		}
		g.tasks = append(g.tasks, t)
	}
	changed := false
	for _, g := range groups {
		start := g.tasks[0].StartTime
		busy := false
		for _, r := range running {
			if r.Source == g.tasks[0].Source && r.StartTime >= start-int64(backupWindow.Seconds()) && r.StartTime <= start+int64(backupWindow.Seconds()) {
				busy = true
			}
		}
		if busy {
			continue // wait for the other nodes of the same run
		}
		c.reportBackupRun(ctx, g.tasks)
		for _, t := range g.tasks {
			seen[t.UPID] = true
		}
		changed = true
	}
	if changed {
		// keep the set small: only tasks the overview still lists
		keep := map[string]bool{}
		for _, t := range ov.Tasks {
			if seen[t.UPID] {
				keep[t.UPID] = true
			}
		}
		if len(keep) == 0 {
			keep["-"] = true
		}
		_ = c.St.SetSetting("seen-vzdump", keep)
	}
}

func (c *Core) reportBackupRun(ctx context.Context, tasks []TaskInfo) {
	clusterID := strings.TrimPrefix(tasks[0].Source, "pve:")
	pve, cl, err := c.PVE(clusterID)
	if err != nil {
		return
	}
	names := c.guestNames(ctx)
	var rows []notify.Row
	var errLines []string
	var nodes []string
	storage, mode := "", ""
	start, end := tasks[0].StartTime, int64(0)
	taskFailed := false
	for _, t := range tasks {
		nodes = append(nodes, t.Node)
		if t.EndTime > end {
			end = t.EndTime
		}
		if t.Status != "OK" && !strings.HasPrefix(t.Status, "WARNINGS") {
			taskFailed = true
		}
		lines, err := pve.TaskLog(ctx, t.UPID)
		if err != nil {
			errLines = append(errLines, t.Node+": Protokoll nicht lesbar: "+err.Error())
			continue
		}
		r := prox.ParseVzdump(lines)
		if storage == "" {
			storage, mode = r.Storage, r.Mode
		}
		for _, g := range r.Guests {
			row := notify.Row{Kind: "VM", ID: g.VMID, Name: g.Name, Duration: g.Duration, Size: g.Transferred}
			if g.Type == "lxc" {
				row.Kind = "CT"
			}
			if row.Name == "" {
				row.Name = names[strings.ToLower(row.Kind)+"/"+g.VMID]
			}
			switch {
			case g.OK:
				row.Status = "ok"
			case g.Done:
				row.Status, row.Info = "error", g.Error
			default:
				row.Status, row.Info = "error", "Sicherung wurde abgebrochen"
			}
			rows = append(rows, row)
		}
		errLines = append(errLines, r.Errors...)
		for _, l := range lines {
			if strings.Contains(l.T, "ERROR") && len(errLines) < 20 {
				errLines = append(errLines, strings.TrimSpace(l.T))
			}
		}
	}
	errLines = dedupe(errLines)
	okN, failN := 0, 0
	for _, r := range rows {
		if r.Status == "ok" {
			okN++
		} else {
			failN++
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if (rows[i].Status == "ok") != (rows[j].Status == "ok") {
			return rows[i].Status != "ok" // problems first
		}
		a, _ := strconv.Atoi(rows[i].ID)
		b, _ := strconv.Atoi(rows[j].ID)
		return a < b
	})
	d := &notify.Details{RowsTitle: "Gesicherte Server", Rows: rows, Link: "/sicherungen"}
	_ = mode
	d.Facts = []notify.Fact{
		{Label: "System", Value: cl.Name},
		{Label: "Beginn", Value: fmtWhen(start)},
		{Label: "Dauer", Value: humanDuration(end - start)},
	}
	if storage != "" {
		d.Facts = append(d.Facts, notify.Fact{Label: "Gesichert nach", Value: storage})
	}
	severity, title, body := "info", "", ""
	total := okN + failN
	var failed []string
	for _, r := range rows {
		if r.Status != "ok" {
			failed = append(failed, strings.TrimSpace(r.Name+" ("+r.ID+")"))
		}
	}
	forward := " Bitte leiten Sie diese Nachricht an Ihren IT-Betreuer weiter."
	switch {
	case total == 0:
		severity, d.Status = "critical", "error"
		title = "Datensicherung fehlgeschlagen"
		body = "Die Datensicherung hat nicht funktioniert, es wurde kein Server gesichert." + forward
	case failN == 0 && !taskFailed:
		d.Status = "ok"
		title = fmt.Sprintf("Datensicherung erfolgreich – %s gesichert", allServers(okN))
		body = fmt.Sprintf("Die Datensicherung war erfolgreich: %s gesichert. Sie müssen nichts tun.", allServers(okN))
	case okN == 0:
		severity, d.Status = "critical", "error"
		title = "Datensicherung fehlgeschlagen"
		body = fmt.Sprintf("Die Datensicherung hat nicht funktioniert, keiner der %d Server wurde gesichert.", failN) + forward
	default:
		severity, d.Status = "warning", "warning"
		title = fmt.Sprintf("Datensicherung unvollständig – %d von %d Servern gesichert", okN, total)
		body = fmt.Sprintf("%d von %d Servern wurden gesichert. Nicht gesichert: %s.", okN, total, strings.Join(failed, ", ")) + forward
	}
	if d.Status != "ok" && len(errLines) > 0 {
		if len(errLines) > 15 {
			errLines = errLines[:15]
		}
		d.Log = errLines
	}
	c.EventDetails(severity, cl.Name, title, body, d)
}

// guestNames maps "vm/100" / "ct/110" to the guest names of all connected clusters.
func (c *Core) guestNames(ctx context.Context) map[string]string {
	out := map[string]string{}
	clusters, _ := c.Clusters()
	for _, cl := range clusters {
		guests, err := c.PVEClient(cl).Guests(ctx)
		if err != nil {
			continue
		}
		for _, g := range guests {
			t := "vm"
			if g.Type == "lxc" {
				t = "ct"
			}
			out[t+"/"+strconv.Itoa(g.VMID)] = g.Name
		}
	}
	return out
}

// diskContents lists the newest backup of each guest on a datastore (for the copy report).
func (c *Core) diskContents(ctx context.Context, pbs *prox.PBS, store string) []notify.Row {
	snaps, err := pbs.Snapshots(ctx, store, "")
	if err != nil {
		return nil
	}
	names := c.guestNames(ctx)
	newest := map[string]prox.Snapshot{}
	count := map[string]int{}
	for _, s := range snaps {
		k := s.Type + "/" + s.ID
		count[k]++
		if s.Time > newest[k].Time {
			newest[k] = s
		}
	}
	var rows []notify.Row
	for k, s := range newest {
		kind := strings.ToUpper(s.Type)
		if kind == "HOST" {
			kind = "Host"
		}
		name := names[k]
		if name == "" {
			name = s.Comment
		}
		info := ""
		if count[k] > 1 {
			info = fmt.Sprintf("dazu %d ältere Stände", count[k]-1)
		}
		rows = append(rows, notify.Row{Kind: kind, ID: s.ID, Name: name, Status: "ok", Time: s.Time, Size: fmtBytes(s.Size), Info: info})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, _ := strconv.Atoi(rows[i].ID)
		b, _ := strconv.Atoi(rows[j].ID)
		return a < b
	})
	return rows
}

// taskLogExcerpt returns the error lines (or the last lines) of a failed task.
func taskLogExcerpt(lines []prox.TaskLine) []string {
	var errs []string
	for _, l := range lines {
		t := strings.TrimSpace(l.T)
		if strings.Contains(strings.ToLower(t), "error") || strings.Contains(t, "failed") {
			errs = append(errs, t)
		}
	}
	if len(errs) == 0 {
		for i := max(0, len(lines)-8); i < len(lines); i++ {
			errs = append(errs, strings.TrimSpace(lines[i].T))
		}
	}
	if len(errs) > 15 {
		errs = errs[len(errs)-15:]
	}
	return errs
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// allServers: "der Server" / "alle 7 Server".
func allServers(n int) string {
	if n == 1 {
		return "der Server wurde"
	}
	return fmt.Sprintf("alle %d Server wurden", n)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func humanDuration(s int64) string {
	switch {
	case s < 60:
		return fmt.Sprintf("%d s", s)
	case s < 3600:
		return fmt.Sprintf("%d Min. %d s", s/60, s%60)
	}
	return fmt.Sprintf("%d Std. %d Min.", s/3600, (s%3600)/60)
}

func fmtBytes(n int64) string {
	if n <= 0 {
		return ""
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if f >= 100 || i == 0 {
		return strings.Replace(fmt.Sprintf("%.0f %s", f, units[i]), ".", ",", 1)
	}
	return strings.Replace(fmt.Sprintf("%.1f %s", f, units[i]), ".", ",", 1)
}
