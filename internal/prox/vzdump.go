package prox

import (
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
	Transferred string // e.g. "32.00 GiB"
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
	vzStart    = regexp.MustCompile(`Starting Backup of VM (\d+) \((qemu|lxc)\)`)
	vzFinish   = regexp.MustCompile(`Finished Backup of VM (\d+) \(([\d:]+)\)`)
	vzFailed   = regexp.MustCompile(`ERROR: Backup of VM (\d+) failed - (.*)`)
	vzName     = regexp.MustCompile(`INFO: (?:VM|CT) Name: (.+)`)
	vzTransfer = regexp.MustCompile(`transferred ([\d.]+ \w+)`)
	vzReused   = regexp.MustCompile(`backup was done incrementally, reused ([\d.]+ \w+) \((\d+%)\)`)
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
		case vzFinish.MatchString(t):
			m := vzFinish.FindStringSubmatch(t)
			g := get(m[1])
			g.OK, g.Done, g.Duration = true, true, m[2]
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
			} else if m := vzArchive.FindStringSubmatch(t); m != nil {
				g.Archive = m[1]
			} else if strings.HasPrefix(t, "ERROR:") && g.Error == "" {
				g.Error = strings.TrimSpace(strings.TrimPrefix(t, "ERROR:"))
			}
		}
	}
	return r
}
