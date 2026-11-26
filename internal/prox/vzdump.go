package prox

import (
	"fmt"
	"regexp"
	"strings"
)

// GuestBackup is the result for one guest in a vzdump log.
type GuestBackup struct {
	VMID        string
	Type        string // qemu, lxc
	Name        string
	OK          bool
	Done        bool // finished or failed (false = log ended while it ran)
	Duration    string
	Transferred string // e.g. "32.00 GiB" (VM: data read; container: new data)
	Scanned     string // container: all data read, e.g. "3.6 GiB"
	Reused      string // incremental: reused part, e.g. "94%"
	Archive     string // PBS snapshot, e.g. vm/100/2026-11-09T20:00:02Z
	Error       string
}

// VzdumpResult is the parsed log of a backup task.
type VzdumpResult struct {
	Storage string
	Mode    string
	Guests  []GuestBackup
	Errors  []string // job-level errors
}

var (
	// Outcome lines are matched as whole lines with vzdump's own prefix: text a guest or a
	// hook script writes into the log can then not mark a backup as done.
	vzStart    = regexp.MustCompile(`^INFO: Starting Backup of VM (\d+) \((qemu|lxc)\)$`)
	vzFinish   = regexp.MustCompile(`^INFO: Finished Backup of VM (\d+) \(([\d:]+)\)$`)
	vzFailed   = regexp.MustCompile(`^ERROR: Backup of VM (\d+) failed - (.*)$`)
	vzName     = regexp.MustCompile(`INFO: (?:VM|CT) Name: (.+)`)
	vzTransfer = regexp.MustCompile(`transferred ([\d.]+ \w+)`)
	vzReused   = regexp.MustCompile(`backup was done incrementally, reused ([\d.]+ \w+) \((\d+%)\)`)
	vzHad      = regexp.MustCompile(`had to backup ([\d.]+ \w+) of ([\d.]+ \w+)`)
	vzArchive  = regexp.MustCompile(`creating (?:Proxmox Backup Server )?archive '([^']+)'`)
	vzJob      = regexp.MustCompile(`starting new backup job: vzdump .*`)
	vzStorage  = regexp.MustCompile(`--storage (\S+)`)
	vzMode     = regexp.MustCompile(`--mode (\S+)`)
)

// ParseVzdump reads which guests a backup task saved and how it went.
func ParseVzdump(lines []TaskLine) VzdumpResult {
	var r VzdumpResult
	idx := map[string]int{}
	cur := -1
	get := func(id string) *GuestBackup {
		i, ok := idx[id]
		if !ok {
			r.Guests = append(r.Guests, GuestBackup{VMID: id})
			i = len(r.Guests) - 1
			idx[id] = i
		}
		return &r.Guests[i]
	}
	for _, l := range lines {
		t := strings.TrimSpace(l.T)
		switch {
		case vzJob.MatchString(t):
			if m := vzStorage.FindStringSubmatch(t); m != nil {
				r.Storage = m[1]
			}
			if m := vzMode.FindStringSubmatch(t); m != nil {
				r.Mode = m[1]
			}
		case vzStart.MatchString(t):
			m := vzStart.FindStringSubmatch(t)
			g := get(m[1])
			g.Type = m[2]
			cur = idx[m[1]]
		case vzFinish.MatchString(t) && cur >= 0 && r.Guests[cur].VMID == vzFinish.FindStringSubmatch(t)[1]:
			// success only for the guest being backed up right now, and never over a failure
			m := vzFinish.FindStringSubmatch(t)
			g := &r.Guests[cur]
			if !(g.Done && !g.OK) {
				g.OK, g.Done, g.Duration = true, true, m[2]
			}
			cur = -1
		case vzFailed.MatchString(t):
			m := vzFailed.FindStringSubmatch(t)
			g := get(m[1])
			g.OK, g.Done, g.Error = false, true, strings.TrimSpace(m[2])
			cur = -1
		case strings.HasPrefix(t, "ERROR:") && cur < 0:
			r.Errors = append(r.Errors, strings.TrimSpace(strings.TrimPrefix(t, "ERROR:")))
		case cur >= 0:
			g := &r.Guests[cur]
			if m := vzName.FindStringSubmatch(t); m != nil {
				g.Name = strings.TrimSpace(m[1])
			} else if m := vzReused.FindStringSubmatch(t); m != nil {
				g.Reused = m[2]
			} else if m := vzTransfer.FindStringSubmatch(t); m != nil && strings.HasPrefix(t, "INFO: transferred") {
				g.Transferred = m[1]
			} else if m := vzHad.FindStringSubmatch(t); m != nil && strings.HasPrefix(t, "INFO:") {
				// a container (proxmox-backup-client): new data of all it read
				g.Transferred = addSize(g.Transferred, m[1])
				g.Scanned = addSize(g.Scanned, m[2])
			} else if m := vzArchive.FindStringSubmatch(t); m != nil {
				g.Archive = m[1]
			} else if strings.HasPrefix(t, "ERROR:") && g.Error == "" {
				g.Error = strings.TrimSpace(strings.TrimPrefix(t, "ERROR:"))
			}
		}
	}
	return r
}

// addSize adds two sizes ("1.2 GiB" + "300 MiB"; a container may have several archives).
func addSize(a, b string) string {
	if a == "" {
		return b
	}
	return fmt.Sprintf("%.2f MiB", float64(ParseSize(a)+ParseSize(b))/(1<<20))
}

// Seconds is how long the backup of the guest took ("Finished Backup of VM 100 (00:01:30)").
func (g GuestBackup) Seconds() int64 {
	var h, m, s int64
	if n, _ := fmt.Sscanf(g.Duration, "%d:%d:%d", &h, &m, &s); n != 3 {
		return 0
	}
	return h*3600 + m*60 + s
}

// ReadBytes is how much the backup read: the changed data of a VM, everything of a container.
func (g GuestBackup) ReadBytes() int64 {
	if g.Scanned != "" {
		return ParseSize(g.Scanned)
	}
	return ParseSize(g.Transferred)
}

// VzdumpJob is the command line of a backup task: which job and which guests.
type VzdumpJob struct {
	JobID   string   // --job-id (newer Proxmox VE), "" if not in the log
	All     bool     // --all 1
	IDs     []string // guests named on the command line
	Exclude []string // --exclude
	Storage string
}

var (
	vzJobID   = regexp.MustCompile(`--job-id (\S+)`)
	vzAll     = regexp.MustCompile(`--all 1\b`)
	vzExclude = regexp.MustCompile(`--exclude '?([\d,]+)'?`)
	vzIDs     = regexp.MustCompile(`vzdump((?: \d+)+)`)
)

// ParseVzdumpJob reads the command line ("starting new backup job: vzdump …") of a backup task.
func ParseVzdumpJob(lines []TaskLine) (VzdumpJob, bool) {
	for _, l := range lines {
		t := strings.TrimSpace(l.T)
		if !vzJob.MatchString(t) {
			continue
		}
		var j VzdumpJob
		if m := vzJobID.FindStringSubmatch(t); m != nil {
			j.JobID = strings.Trim(m[1], "'")
		}
		if m := vzStorage.FindStringSubmatch(t); m != nil {
			j.Storage = strings.Trim(m[1], "'")
		}
		j.All = vzAll.MatchString(t)
		if m := vzExclude.FindStringSubmatch(t); m != nil {
			j.Exclude = strings.Split(m[1], ",")
		}
		if m := vzIDs.FindStringSubmatch(t); m != nil {
			j.IDs = strings.Fields(m[1])
		}
		return j, true
	}
	return VzdumpJob{}, false
}
