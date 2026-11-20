package fake

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/prox"
)

// groupSpace is the simulated chunk usage of a backup group: a share of its newest size that
// grows with the number of backups; most of it belongs to the group alone.
type groupSpace struct {
	group          string
	share, excl, n int64
}

func spaceOf(snaps []prox.Snapshot) []groupSpace {
	type agg struct {
		newest prox.Snapshot
		count  int64
	}
	by := map[string]*agg{}
	for _, s := range snaps {
		k := s.Type + "/" + s.ID
		a := by[k]
		if a == nil {
			a = &agg{}
			by[k] = a
		}
		a.count++
		if s.Time > a.newest.Time {
			a.newest = s
		}
	}
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []groupSpace
	for _, k := range keys {
		a := by[k]
		share := a.newest.Size*38/100 + a.count*a.newest.Size/180
		if strings.HasPrefix(k, "host/") {
			share = a.newest.Size/4 + a.count*mib
		}
		out = append(out, groupSpace{group: k, share: share, excl: share * 62 / 100, n: share / (2 * mib)})
	}
	return out
}

// spaceOutput answers the space script for the datastore at path.
func (p *PBS) spaceOutput(path string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.stores {
		if s.cfg.Path != path {
			continue
		}
		var b strings.Builder
		var chunks, bytes int64
		for _, g := range spaceOf(s.snaps) {
			fmt.Fprintf(&b, "G\t%s\t%d\t%d\t%d\n", g.group, g.share, g.excl, g.n)
			chunks += g.n
			bytes += g.share
		}
		fmt.Fprintf(&b, "T\t%d\t%d\n", chunks, bytes)
		return b.String()
	}
	return "E\tKein Datastore unter " + path + " (.chunks fehlt)\n"
}

// seedSpaceHistory gives the demo 60 days of history: nextcloud grows fast, the file server
// steadily, the others hardly.
func seedSpaceHistory(c *core.Core, p *PBS, server, store string, now time.Time) {
	p.mu.Lock()
	groups := spaceOf(p.stores[store].snaps)
	p.mu.Unlock()
	rate := map[string]float64{"vm/102": 0.012, "vm/101": 0.004, "vm/100": 0.0015}
	hist := map[string]map[string][]map[string]any{}
	h := map[string][]map[string]any{}
	for _, g := range groups {
		var pts []map[string]any
		for d := 60; d >= 1; d-- {
			f := 1 - rate[g.group]*float64(d)
			pts = append(pts, map[string]any{"d": now.AddDate(0, 0, -d).Format("2006-01-02"), "s": int64(float64(g.share) * f)})
		}
		h[g.group] = pts
	}
	hist[server+"|"+store] = h
	_ = c.St.SetSetting("space-hist", hist)
}
