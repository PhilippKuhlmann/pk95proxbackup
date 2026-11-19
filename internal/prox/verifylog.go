package prox

import (
	"regexp"
	"strings"
)

// VerifyFailure is one snapshot (or group) a PBS verification could not confirm.
type VerifyFailure struct {
	Path  string // ct/250101/2026-10-04T08:00:16Z (group: ct/250101), namespace prefix "ns/x/" included
	Error string // first error line the log has for it
}

// VerifyLog is what the log of a verification says.
type VerifyLog struct {
	Snapshots int // snapshots the task started to verify
	Failures  []VerifyFailure
}

var (
	vfStart  = regexp.MustCompile(`^verify [^:\s]+:(\S+)(?: - (.*))?$`)
	vfFailed = regexp.MustCompile(`^Failed to verify the following snapshots/groups:`)
	vfErrors = regexp.MustCompile(`\((\d+) errors\)`)
)

// ParseVerifyLog reads a verification log: which snapshots failed and why.
func ParseVerifyLog(lines []TaskLine) VerifyLog {
	var out VerifyLog
	why := map[string]string{}
	cur := ""
	inList := false
	for _, l := range lines {
		raw := bsStamp.ReplaceAllString(l.T, "")
		t := strings.TrimSpace(raw)
		switch {
		case vfFailed.MatchString(t):
			inList = true
		case inList && (strings.HasPrefix(raw, "\t") || strings.HasPrefix(raw, "  ")) && t != "":
			out.Failures = append(out.Failures, VerifyFailure{Path: t})
		case inList:
			inList = false
		}
		if inList {
			continue
		}
		if m := vfStart.FindStringSubmatch(t); m != nil && !strings.HasPrefix(t, "verify group") {
			cur = m[1]
			out.Snapshots++
			if m[2] != "" && why[cur] == "" {
				why[cur] = m[2]
			}
			continue
		}
		if cur == "" || why[cur] != "" {
			continue
		}
		low := strings.ToLower(t)
		if m := vfErrors.FindStringSubmatch(t); m != nil {
			if m[1] != "0" {
				why[cur] = t
			}
			continue
		}
		if strings.Contains(low, "corrupt") || strings.Contains(low, "failed") || strings.Contains(low, "error") || strings.Contains(low, "can't verify") {
			why[cur] = t
		}
	}
	for i, f := range out.Failures {
		out.Failures[i].Error = why[f.Path]
		if out.Failures[i].Error == "" { // a group: take the first error of its snapshots
			for p, w := range why {
				if strings.HasPrefix(p, f.Path+"/") && w != "" {
					out.Failures[i].Error = w
					break
				}
			}
		}
	}
	return out
}

// VerifyPath splits a snapshot path of a verification log into namespace, type, id and time
// ("" for a group).
func VerifyPath(p string) (ns, typ, id, tm string, ok bool) {
	parts := strings.Split(p, "/")
	n := len(parts)
	if n >= 3 && strings.Contains(parts[n-1], "T") && strings.HasSuffix(parts[n-1], "Z") {
		return nsFromPath(parts[:n-3]), parts[n-3], parts[n-2], parts[n-1], true
	}
	if n >= 2 {
		return nsFromPath(parts[:n-2]), parts[n-2], parts[n-1], "", true
	}
	return "", "", "", "", false
}
