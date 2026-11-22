package core

import (
	"regexp"
	"time"
)

// A reader task on the Backup Server is the server side of a client reading a backup: a restore
// in Proxmox VE, a file restore or the appliance's restore test. Its errors are judged here before
// anyone is alarmed:
//   - The client closed the connection ("connection error: not connected"). PBS often ends the
//     task like that although everything was read; the client reports its own outcome (a failed
//     restore is reported as such), so this one never is.
//   - Any other error is held back for readerGrace: when the same backup is read without error
//     afterwards, the problem fixed itself. Only otherwise it is reported.

const readerGrace = 15 * time.Minute

var readerGoneRe = regexp.MustCompile(`(?i)connection error: not connected|broken pipe|connection reset`)

// readerOutcome judges a failed reader task: "harmless" (with a note for the task list), "wait"
// (still within readerGrace) or "" (an error to report). tasks are the other known tasks.
func readerOutcome(t TaskInfo, tasks []TaskInfo, now time.Time) (state, note string) {
	if t.Type != "reader" || t.EndTime == 0 || t.Status == "" || t.Status == "OK" {
		return "", ""
	}
	if readerGoneRe.MatchString(t.Status) {
		return "harmless", "ohne Befund: der Leser hat die Verbindung beendet"
	}
	for _, o := range tasks {
		if o.Type == "reader" && o.UPID != t.UPID && o.Source == t.Source && o.ID == t.ID &&
			o.StartTime >= t.StartTime && o.EndTime != 0 && o.Status == "OK" {
			return "harmless", "ohne Befund: danach fehlerfrei gelesen"
		}
	}
	if now.Sub(time.Unix(t.EndTime, 0)) < readerGrace {
		return "wait", ""
	}
	return "", ""
}
