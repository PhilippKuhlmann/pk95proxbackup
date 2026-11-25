package prox

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"
)

// Keep is a retention: how many backups per period stay (0: the rule is not used).
type Keep struct {
	Last, Hourly, Daily, Weekly, Monthly, Yearly int
}

// Any tells whether a rule is set; without any the Backup Server keeps everything.
func (k Keep) Any() bool {
	return k.Last+k.Hourly+k.Daily+k.Weekly+k.Monthly+k.Yearly > 0
}

func (k Keep) params(p Params) {
	for name, v := range map[string]int{"last": k.Last, "hourly": k.Hourly, "daily": k.Daily, "weekly": k.Weekly, "monthly": k.Monthly, "yearly": k.Yearly} {
		if v > 0 {
			p["keep-"+name] = v
		}
	}
}

// PruneItem is one backup in a (simulated) cleanup of a group.
type PruneItem struct {
	Type      string `json:"backup-type"`
	ID        string `json:"backup-id"`
	Time      int64  `json:"backup-time"`
	Keep      bool   `json:"keep"`
	Protected bool   `json:"protected"`
}

// PruneDryRun asks the Backup Server which backups of a guest a cleanup with these rules
// would keep – nothing is deleted.
func (p *PBS) PruneDryRun(ctx context.Context, store, ns, typ, id string, k Keep) ([]PruneItem, error) {
	params := Params{"backup-type": typ, "backup-id": id, "dry-run": true, "ns": emptyNil(ns)}
	k.params(params)
	var out []PruneItem
	err := p.Do(ctx, http.MethodPost, "/admin/datastore/"+url.PathEscape(store)+"/prune", params, &out)
	return out, err
}

// PruneSelect applies the rules of the Backup Server to the backups of one group: newest first,
// keep-last, -hourly, -daily, -weekly, -monthly, -yearly in this order; each rule keeps the
// newest backup of each period (in local time, ISO weeks) not yet covered by an earlier rule;
// further backups of a period it took are dropped. Protected backups always stay and count for
// no rule. The result tells per backup time whether it stays.
func PruneSelect(snaps []Snapshot, k Keep, loc *time.Location) map[int64]bool {
	list := append([]Snapshot(nil), snaps...)
	sort.Slice(list, func(i, j int) bool { return list[i].Time > list[j].Time })
	keep := map[int64]bool{}
	if !k.Any() {
		for _, s := range list {
			keep[s.Time] = true
		}
		return keep
	}
	decided := map[int64]bool{}
	rules := []struct {
		n  int
		id func(time.Time) string
	}{
		{k.Last, func(t time.Time) string { return t.Format("2006/01/02/15/04/05") }},
		{k.Hourly, func(t time.Time) string { return t.Format("2006/01/02/15") }},
		{k.Daily, func(t time.Time) string { return t.Format("2006/01/02") }},
		{k.Weekly, func(t time.Time) string { y, w := t.ISOWeek(); return fmt.Sprintf("%d/%02d", y, w) }},
		{k.Monthly, func(t time.Time) string { return t.Format("2006/01") }},
		{k.Yearly, func(t time.Time) string { return t.Format("2006") }},
	}
	for _, r := range rules {
		if r.n <= 0 {
			continue
		}
		already := map[string]bool{} // periods a backup kept by an earlier rule covers
		for _, s := range list {
			if keep[s.Time] && !s.Protected {
				already[r.id(time.Unix(s.Time, 0).In(loc))] = true
			}
		}
		taken := map[string]bool{}
		for _, s := range list {
			if decided[s.Time] {
				continue
			}
			if s.Protected {
				decided[s.Time], keep[s.Time] = true, true
				continue
			}
			id := r.id(time.Unix(s.Time, 0).In(loc))
			if already[id] {
				continue
			}
			if !taken[id] {
				if len(taken) >= r.n {
					break
				}
				taken[id] = true
				decided[s.Time], keep[s.Time] = true, true
			} else {
				decided[s.Time] = true // a second backup of a period already kept
			}
		}
	}
	for _, s := range list {
		if s.Protected {
			keep[s.Time] = true
		} else if !keep[s.Time] {
			keep[s.Time] = false
		}
	}
	return keep
}
