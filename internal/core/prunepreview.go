package core

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"pk95proxbackup/internal/prox"
)

// Before the retention of a datastore changes: which backups would a cleanup with the new rules
// keep, per guest. The Backup Server answers itself (prune with dry-run), so the preview follows
// exactly the rules it applies later; nothing is deleted.

// PruneMark is one backup in the preview.
type PruneMark struct {
	Time      int64 `json:"time"`
	Keep      bool  `json:"keep"`
	Protected bool  `json:"protected"`
	Size      int64 `json:"size"`
}

// PruneGroup is the preview for one guest.
type PruneGroup struct {
	Type      string      `json:"type"`
	ID        string      `json:"bid"`
	Name      string      `json:"name"`
	Keep      int         `json:"keep"`
	Remove    int         `json:"remove"`
	Protected int         `json:"protected"`
	Items     []PruneMark `json:"items"` // newest first
}

// PrunePreview is what a cleanup with new rules would do on a datastore.
type PrunePreview struct {
	Keep        int          `json:"keep"`
	Remove      int          `json:"remove"`
	Protected   int          `json:"protected"`
	RemoveBytes int64        `json:"remove_bytes"` // logical size of what goes (shared chunks stay)
	Groups      []PruneGroup `json:"groups"`
	Errors      []string     `json:"errors"`
}

// PrunePreview asks the Backup Server what a cleanup with these rules would keep.
func (c *Core) PrunePreview(ctx context.Context, srvID, store string, k prox.Keep) (PrunePreview, error) {
	out := PrunePreview{Groups: []PruneGroup{}, Errors: []string{}}
	pbs, _, err := c.PBS(srvID)
	if err != nil {
		return out, err
	}
	snaps, err := pbs.Snapshots(ctx, store, "")
	if err != nil {
		return out, err
	}
	size := map[string]int64{}
	byGroup := map[string]*PruneGroup{}
	var keys []string
	names := c.guestNames(ctx)
	newest := map[string]int64{}
	for _, sn := range snaps {
		g := sn.Type + "/" + sn.ID
		size[fmt.Sprint(g, "@", sn.Time)] = sn.Size
		if byGroup[g] == nil {
			byGroup[g] = &PruneGroup{Type: sn.Type, ID: sn.ID, Name: names[g]}
			keys = append(keys, g)
		}
		if byGroup[g].Name == "" && sn.Comment != "" && sn.Time > newest[g] {
			newest[g] = sn.Time
			byGroup[g].Name = sn.Comment
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, key := range keys {
		g := byGroup[key]
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			items, err := pbs.PruneDryRun(ctx, store, "", g.Type, g.ID, k)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.Errors = append(out.Errors, g.Type+"/"+g.ID+": "+err.Error())
				return
			}
			sort.Slice(items, func(i, j int) bool { return items[i].Time > items[j].Time })
			for _, it := range items {
				m := PruneMark{Time: it.Time, Keep: it.Keep || it.Protected, Protected: it.Protected, Size: size[fmt.Sprint(key, "@", it.Time)]}
				g.Items = append(g.Items, m)
				switch {
				case m.Protected:
					g.Protected++
				case m.Keep:
					g.Keep++
				default:
					g.Remove++
					out.RemoveBytes += m.Size
				}
			}
		}()
	}
	wg.Wait()
	for _, key := range keys {
		g := byGroup[key]
		if g.Items == nil {
			continue
		}
		out.Keep += g.Keep
		out.Remove += g.Remove
		out.Protected += g.Protected
		out.Groups = append(out.Groups, *g)
	}
	// most removed first, then by ID
	sort.SliceStable(out.Groups, func(i, j int) bool {
		a, b := out.Groups[i], out.Groups[j]
		if a.Remove != b.Remove {
			return a.Remove > b.Remove
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return len(a.ID) < len(b.ID) || len(a.ID) == len(b.ID) && a.ID < b.ID
	})
	return out, nil
}
