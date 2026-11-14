package core

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Time windows for copy jobs: a copy (sync job) that still runs at the end of its window is
// stopped, so the line and the disks are free during the day. The next run goes on where it
// stopped – what was copied stays.

var windowRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// CopyWindows returns the end times of the copy jobs of a server (job id → "HH:MM").
func (c *Core) CopyWindows(srvID string) map[string]string {
	m := map[string]string{}
	_ = c.St.GetSetting("copywindow:"+srvID, &m)
	return m
}

// SetCopyWindow sets ("HH:MM") or removes ("") the end time of a copy job.
func (c *Core) SetCopyWindow(srvID, jobID, end string) error {
	if end != "" && !windowRe.MatchString(end) {
		return fmt.Errorf("Uhrzeit wie 06:30")
	}
	m := c.CopyWindows(srvID)
	if end == "" {
		delete(m, jobID)
	} else {
		m[jobID] = end
	}
	return c.St.SetSetting("copywindow:"+srvID, m)
}

// windowEnd is the last time the window closed at or before now.
func windowEnd(end string, now time.Time) time.Time {
	var h, m int
	_, _ = fmt.Sscanf(end, "%d:%d", &h, &m)
	t := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if t.After(now) {
		t = t.AddDate(0, 0, -1)
	}
	return t
}

// enforceCopyWindows stops copies that started before the end of their window and still run.
func (c *Core) enforceCopyWindows(ctx context.Context, now time.Time) {
	servers, _ := c.Servers()
	for _, srv := range servers {
		windows := c.CopyWindows(srv.ID)
		if len(windows) == 0 {
			continue
		}
		pbs := c.PBSClient(srv)
		tasks, err := pbs.RunningTasks(ctx)
		if err != nil {
			continue
		}
		for _, t := range tasks {
			if t.Type != "syncjob" || t.EndTime != 0 {
				continue
			}
			// worker id: <remote>:<remote-store>:<store>:<ns>:<job id>
			job := t.WorkerID[strings.LastIndex(t.WorkerID, ":")+1:]
			end, ok := windows[job]
			if !ok || t.StartTime >= windowEnd(end, now).Unix() {
				continue
			}
			if err := pbs.StopTask(ctx, t.UPID); err != nil {
				c.Event("warning", srv.Name, "Kopierjob "+job+" ließ sich nicht anhalten", "Das Zeitfenster endete um "+end+": "+err.Error())
				continue
			}
			name := job
			if js, err := pbs.Jobs(ctx, "sync"); err == nil {
				for _, j := range js {
					if j.ID == job && j.Comment != "" {
						name = "„" + j.Comment + "“"
					}
				}
			}
			c.Event("info", srv.Name, "Kopierjob "+name+" um "+end+" angehalten",
				"Das Zeitfenster ist vorbei, die Kopie lief seit "+time.Unix(t.StartTime, 0).Format("15:04")+". Der nächste Lauf setzt fort; was schon kopiert ist, bleibt.")
		}
	}
}

// EnforceCopyWindows runs the check now (for tests).
func (c *Core) EnforceCopyWindows(ctx context.Context, now time.Time) { c.enforceCopyWindows(ctx, now) }
