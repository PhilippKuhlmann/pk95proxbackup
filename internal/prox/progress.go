package prox

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// tailLog returns the last n lines of a task log (uses the "total" the APIs report).
func (c *Client) tailLog(ctx context.Context, logPath string, n int) ([]TaskLine, error) {
	var first []TaskLine
	total, err := c.DoTotal(ctx, http.MethodGet, logPath, Params{"start": 0, "limit": n}, &first)
	if err != nil {
		return nil, err
	}
	if total <= n || total <= len(first) {
		return first, nil
	}
	var last []TaskLine
	if _, err := c.DoTotal(ctx, http.MethodGet, logPath, Params{"start": total - n, "limit": n}, &last); err != nil {
		return first, nil
	}
	return last, nil
}

// TailLog returns the last n log lines of a PBS task.
func (p *PBS) TailLog(ctx context.Context, upid string, n int) ([]TaskLine, error) {
	return p.tailLog(ctx, "/nodes/localhost/tasks/"+url.PathEscape(upid)+"/log", n)
}

// RunningTasks lists the tasks that are still running.
func (p *PBS) RunningTasks(ctx context.Context) ([]Task, error) {
	var out []Task
	err := p.Do(ctx, http.MethodGet, "/nodes/localhost/tasks", Params{"running": true, "limit": 50}, &out)
	return out, err
}

// TailLog returns the last n log lines of a PVE task.
func (p *PVE) TailLog(ctx context.Context, upid string, n int) ([]TaskLine, error) {
	return p.tailLog(ctx, "/nodes/"+url.PathEscape(UPIDNode(upid))+"/tasks/"+url.PathEscape(upid)+"/log", n)
}

// Progress is what the log of a running task tells about its progress.
type Progress struct {
	Percent float64 `json:"percent"` // -1 = unknown
	Detail  string  `json:"detail,omitempty"`
	Step    string  `json:"step,omitempty"` // e.g. the guest a vzdump job is working on
	Last    string  `json:"last,omitempty"` // last log line
}

var (
	// PBS sync/verify: "percentage done: 45.00% (9/20 groups)"
	rePercentDone = regexp.MustCompile(`percentage done:\s*([\d.]+)\s*%\s*(\(.*\))?`)
	// PBS GC: "marked 45% (123 of 456 index files)"
	reMarked = regexp.MustCompile(`marked\s+([\d.]+)\s*%\s*(\(.*\))?`)
	// vzdump to PBS / file: "INFO:  45% (4.5 GiB of 10.0 GiB) in 30s, read: …"
	reVzdump = regexp.MustCompile(`INFO:\s+([\d.]+)%\s*(\([^)]*\))?`)
	// restore from PBS: "progress 12% (read 1.2 GiB of 10 GiB …)"
	reRestore = regexp.MustCompile(`progress\s+([\d.]+)%\s*(\([^)]*\))?`)
	// vzdump: "INFO: Starting Backup of VM 101 (qemu)"
	reGuest = regexp.MustCompile(`Starting Backup of VM (\d+)`)
	// sync: "sync group vm/101" / "sync snapshot vm/101/…"
	reGroup = regexp.MustCompile(`sync (?:group|snapshot) ((?:vm|ct|host)/[^/\s]+)`)
)

// ParseProgress reads the progress from the tail of a task log.
func ParseProgress(lines []TaskLine) Progress {
	p := Progress{Percent: -1}
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i].T)
		if t == "" {
			continue
		}
		if p.Last == "" {
			p.Last = t
		}
		if p.Percent < 0 {
			for _, re := range []*regexp.Regexp{rePercentDone, reMarked, reVzdump, reRestore} {
				if m := re.FindStringSubmatch(t); m != nil {
					if v, err := strconv.ParseFloat(m[1], 64); err == nil && v >= 0 && v <= 100 {
						p.Percent = v
						p.Detail = strings.Trim(m[2], "()")
					}
					break
				}
			}
		}
		if p.Step == "" {
			if m := reGuest.FindStringSubmatch(t); m != nil {
				p.Step = "VM/CT " + m[1]
			} else if m := reGroup.FindStringSubmatch(t); m != nil {
				p.Step = m[1]
			}
		}
		if p.Percent >= 0 && p.Step != "" {
			break
		}
	}
	return p
}
