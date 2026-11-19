package prox

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// BackupStats is what the log of a PBS backup task says about one backup.
type BackupStats struct {
	NS       string // namespace ("" = root)
	Type     string // vm, ct, host
	ID       string
	Time     int64 // backup time of the snapshot
	Indexes  int   // archives created (from the head of the log)
	Archives int   // archives with upload statistics (from the tail)
	Upload   int64 // new data sent to the server, all archives
	Size     int64 // data the client read (VMs: only the changed part)
}

var (
	bsStart  = regexp.MustCompile(`starting new backup on datastore '[^']*'.*: "([^"]+)"`)
	bsIndex  = regexp.MustCompile(`^created new (?:fixed|dynamic) index \d+`)
	bsStats  = regexp.MustCompile(`^Upload statistics for '`)
	bsSize   = regexp.MustCompile(`^Size: (\d+)`)
	bsUpload = regexp.MustCompile(`^Upload size: (\d+)`)
)

// ParseBackupHead reads snapshot and number of archives from the first lines of the log.
func ParseBackupHead(lines []TaskLine, st *BackupStats) bool {
	for _, l := range lines {
		t := strings.TrimSpace(l.T)
		if m := bsStart.FindStringSubmatch(t); m != nil {
			parts := strings.Split(m[1], "/")
			if len(parts) < 3 {
				return false
			}
			n := len(parts)
			tm, err := parseBackupTime(parts[n-1])
			if err != nil {
				return false
			}
			st.Type, st.ID, st.Time = parts[n-3], parts[n-2], tm
			st.NS = nsFromPath(parts[:n-3])
		}
		if bsIndex.MatchString(t) {
			st.Indexes++
		}
	}
	return st.Type != ""
}

// ParseBackupTail adds up the upload statistics of all archives found in lines.
func ParseBackupTail(lines []TaskLine, st *BackupStats) {
	st.Archives, st.Upload, st.Size = 0, 0, 0
	in := false
	for _, l := range lines {
		t := strings.TrimSpace(l.T)
		switch {
		case bsStats.MatchString(t):
			in = true
			st.Archives++
		case in && bsSize.MatchString(t):
			n, _ := strconv.ParseInt(bsSize.FindStringSubmatch(t)[1], 10, 64)
			st.Size += n
		case in && bsUpload.MatchString(t):
			n, _ := strconv.ParseInt(bsUpload.FindStringSubmatch(t)[1], 10, 64)
			st.Upload += n
			in = false
		}
	}
}

// nsFromPath turns "ns/a/ns/b" into "a/b".
func nsFromPath(p []string) string {
	var out []string
	for i := 0; i+1 < len(p); i += 2 {
		if p[i] == "ns" {
			out = append(out, p[i+1])
		}
	}
	return strings.Join(out, "/")
}

// BackupTasks lists the backup tasks since a unix time, newest first.
func (p *PBS) BackupTasks(ctx context.Context, since int64) ([]Task, error) {
	var out []Task
	err := p.Do(ctx, http.MethodGet, "/nodes/localhost/tasks", Params{"typefilter": "backup", "since": since, "limit": 1000}, &out)
	return out, err
}

// TaskLogRange returns lines start … start+limit-1 of a task log and the total number of lines.
func (p *PBS) TaskLogRange(ctx context.Context, upid string, start, limit int) ([]TaskLine, int, error) {
	var out []TaskLine
	total, err := p.DoTotal(ctx, http.MethodGet, "/nodes/localhost/tasks/"+url.PathEscape(upid)+"/log", Params{"start": start, "limit": limit}, &out)
	return out, total, err
}

// BackupStatsOf reads the statistics of a finished backup task: the head for the snapshot,
// the tail for the upload statistics, the whole log only when the tail misses archives
// (logs of containers list every chunk and grow to tens of thousands of lines).
func (p *PBS) BackupStatsOf(ctx context.Context, upid string) (BackupStats, bool, error) {
	var st BackupStats
	head, total, err := p.TaskLogRange(ctx, upid, 0, 60)
	if err != nil {
		return st, false, err
	}
	if !ParseBackupHead(head, &st) {
		return st, false, nil
	}
	lines := head
	if total > len(head) {
		start := max(total-400, 0)
		if lines, _, err = p.TaskLogRange(ctx, upid, start, 400); err != nil {
			return st, false, err
		}
	}
	ParseBackupTail(lines, &st)
	if st.Archives < st.Indexes && total > 400 {
		if lines, _, err = p.TaskLogRange(ctx, upid, 0, total); err != nil {
			return st, false, err
		}
		ParseBackupTail(lines, &st)
	}
	return st, st.Archives > 0, nil
}

func parseBackupTime(s string) (int64, error) {
	t, err := time.Parse("2006-01-02T15:04:05Z", s)
	if err != nil {
		return 0, err
	}
	return t.Unix(), nil
}
