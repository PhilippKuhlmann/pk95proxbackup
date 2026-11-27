package core

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
)

// Space check of USB disks: does one disk of a set hold what it is meant to hold? A disk keeps,
// per server, the backups its retention leaves – how many and over how long a time follows from
// the rhythm, the number of disks and the retention, played through for two years. The space
// is one backup of every server plus what changed over that time. Data size and daily change
// come from the logs of the backup tasks (Proxmox VE notes both); a server without a log is
// counted with the size of its disks. On disk the data is compressed: assumed to 60 %.

const (
	spaceCompress = 0.6 // compressed size of backup data (zstd), rather cautious
	spaceReserve  = 1.2 // 20 % free on top
	sizeKeep      = 30
)

// sizeChange is the change of a guest between two backups.
type sizeChange struct {
	T     int64   `json:"t"`
	Bytes int64   `json:"bytes"`
	Days  float64 `json:"days"`
}

type sizeState struct {
	Name  string `json:"name"`
	Data  int64  `json:"data"` // data without zeros, uncompressed
	LastT int64  `json:"last_t"`
	// ZeroPart of a VM's disks that only holds zeros (known from a backup that read them in
	// full; an incremental backup reports only the size of the disks).
	ZeroPart float64      `json:"zero_part,omitempty"`
	Changes  []sizeChange `json:"changes"`
}

// recordSizes notes data size and change of the guests of one backup task.
func (c *Core) recordSizes(clusterID string, end int64, guests []prox.GuestBackup) {
	var st map[string]*sizeState
	_ = c.St.GetSetting("guestsize", &st)
	if st == nil {
		st = map[string]*sizeState{}
	}
	changed := false
	for _, g := range guests {
		if !g.OK {
			continue
		}
		k := clusterID + "/" + g.VMID
		s := st[k]
		if s == nil {
			s = &sizeState{}
			st[k] = s
		}
		if end <= s.LastT {
			continue // read again, or older than what is known
		}
		if g.Name != "" {
			s.Name = g.Name
		}
		switch total := prox.ParseSize(g.Total); {
		case g.Scanned == "" && g.Dirty == "" && g.Zero != "":
			if read := prox.ParseSize(g.Transferred); read > 0 {
				s.ZeroPart = min(float64(prox.ParseSize(g.Zero))/float64(read), 1)
			}
			s.Data = g.DataBytes()
		case g.Scanned == "" && total > 0:
			s.Data = int64(float64(total) * (1 - s.ZeroPart))
		default:
			if d := g.DataBytes(); d > 0 {
				s.Data = d
			}
		}
		if b, ok := g.ChangedBytes(); ok && s.LastT > 0 {
			days := min(max(float64(end-s.LastT)/86400, 0.5), 14)
			s.Changes = append(s.Changes, sizeChange{T: end, Bytes: b, Days: days})
			if len(s.Changes) > sizeKeep {
				s.Changes = s.Changes[len(s.Changes)-sizeKeep:]
			}
		}
		s.LastT = end
		changed = true
	}
	if changed {
		_ = c.St.SetSetting("guestsize", st)
	}
}

// perDay is the usual change a day (median, uncompressed; 0 when unknown).
func (s sizeState) perDay() int64 {
	var v []float64
	for _, ch := range s.Changes {
		v = append(v, float64(ch.Bytes)/ch.Days)
	}
	if len(v) == 0 {
		return 0
	}
	sort.Float64s(v)
	return int64(v[len(v)/2])
}

// SpaceEstimate is what one disk of a set needs.
type SpaceEstimate struct {
	Guests   int      `json:"guests"`
	Base     int64    `json:"base"`      // one backup of every server, compressed
	PerDay   int64    `json:"per_day"`   // new data a day, compressed
	Keep     int      `json:"keep"`      // backups per server that stay on a disk
	SpanDays int      `json:"span_days"` // from the oldest of them to the newest
	Need     int64    `json:"need"`      // with 20 % reserve
	KeepAll  bool     `json:"keep_all"`  // no retention: the disk only fills up
	Smallest int64    `json:"smallest"`  // smallest known disk of the set (0 = none known)
	Label    string   `json:"label,omitempty"`
	Fits     string   `json:"fits"`                 // yes, no, unknown
	LastsDay int      `json:"lasts_days,omitempty"` // keep-all: days until the smallest disk is full
	Rough    []string `json:"rough,omitempty"`      // servers counted with the size of their disks
	Suggest  string   `json:"suggest,omitempty"`    // retention that would fit
	MinDisk  int64    `json:"min_disk,omitempty"`   // disk size that would fit
	Note     string   `json:"note,omitempty"`
}

// guestSize is what the estimate knows about one server.
type guestSize struct {
	name         string
	data, perDay int64 // uncompressed
	rough        bool  // size of its disks: no backup log yet
}

// setGuests lists the servers whose backups go onto the disks of a set.
func (c *Core) setGuests(ctx context.Context, r Rotation) ([]guestSize, error) {
	var st map[string]*sizeState
	_ = c.St.GetSetting("guestsize", &st)
	byID := map[string]*sizeState{} // vmid → newest facts of any cluster
	for k, s := range st {
		_, id, _ := strings.Cut(k, "/")
		if o := byID[id]; o == nil || s.LastT > o.LastT {
			byID[id] = s
		}
	}
	from := func(id, name string, alloc int64) guestSize {
		g := guestSize{name: strings.TrimSpace(id + " " + name)}
		if s := byID[id]; s != nil && s.Data > 0 {
			g.data, g.perDay = s.Data, s.perDay()
			if s.Name != "" {
				g.name = id + " " + s.Name
			}
		} else {
			g.data, g.rough = alloc, true
		}
		return g
	}
	var out []guestSize
	if r.Direct() {
		pve, _, err := c.PVE(r.Cluster)
		if err != nil {
			return nil, err
		}
		guests, err := pve.Guests(ctx)
		if err != nil {
			return nil, err
		}
		only := idSet(r.Guests)
		for _, g := range guests {
			id := strconv.Itoa(g.VMID)
			if g.Template != 0 || len(only) > 0 && !only[id] {
				continue
			}
			// this cluster's guest, not one with the same number elsewhere
			own := st[r.Cluster+"/"+id]
			byID[id] = own
			out = append(out, from(id, g.Name, g.MaxDisk))
		}
		return out, nil
	}
	pbs, _, err := c.PBS(r.SourceServer)
	if err != nil {
		return nil, err
	}
	groups, err := pbs.Groups(ctx, r.SourceStore)
	if err != nil {
		return nil, err
	}
	sizes := map[string]int64{}
	if snaps, err := pbs.Snapshots(ctx, r.SourceStore, r.SourceNS); err == nil {
		for _, s := range snaps {
			sizes[s.Type+"/"+s.ID] = max(sizes[s.Type+"/"+s.ID], s.Size)
		}
	}
	for _, g := range groups {
		if g.Type != "vm" && g.Type != "ct" {
			continue
		}
		out = append(out, from(g.ID, g.Comment, sizes[g.Type+"/"+g.ID]))
	}
	return out, nil
}

func rotKeep(r Rotation) prox.Keep {
	return prox.Keep{Last: r.KeepLast, Daily: r.KeepDaily, Weekly: r.KeepWeekly, Monthly: r.KeepMonthly}
}

// simulateKeep plays the backups onto one disk of a set through two years and returns how many
// stay at the end, the days between the oldest and the newest of them and the days between
// each of them and the one before.
func simulateKeep(r Rotation, disks int, k prox.Keep, now time.Time) (count, span int, gaps []int) {
	n := max(disks, 1)
	const days = 730
	start := time.Date(now.Year(), now.Month(), now.Day(), 21, 0, 0, 0, time.Local).AddDate(0, 0, -days)
	var snaps []prox.Snapshot
	last := -1 // day of the disk's previous turn
	for d := 0; d < days; d++ {
		turn := d%n == 0
		if r.Rhythm == "weekly" {
			turn = (d/7)%n == 0
		}
		if !turn {
			continue
		}
		at := start.AddDate(0, 0, d)
		newDays := 1
		if !r.Direct() && last >= 0 {
			newDays = d - last
			if r.TransferLast > 0 {
				newDays = min(newDays, r.TransferLast)
			}
		}
		for i := newDays - 1; i >= 0; i-- {
			snaps = append(snaps, prox.Snapshot{Type: "vm", ID: "1", Time: at.AddDate(0, 0, -i).Unix()})
		}
		last = d
		if k.Any() {
			keep := prox.PruneSelect(snaps, k, time.Local)
			snaps = slices.DeleteFunc(snaps, func(s prox.Snapshot) bool { return !keep[s.Time] })
		}
	}
	if len(snaps) == 0 {
		return 0, 0, nil
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].Time < snaps[j].Time })
	for i := 1; i < len(snaps); i++ {
		gaps = append(gaps, int((snaps[i].Time-snaps[i-1].Time+43200)/86400))
	}
	return len(snaps), int((snaps[len(snaps)-1].Time - snaps[0].Time) / 86400), gaps
}

// diskSizes is the smallest known disk of a set (from now or the last time it was mounted).
func (c *Core) diskSizes(r Rotation, st *RotationState) (int64, string) {
	var small int64
	label := ""
	for _, d := range r.Disks {
		var total int64
		if st != nil {
			for _, ds := range st.Disks {
				if ds.Key == d.Key && ds.Total > 0 {
					total = ds.Total
				}
			}
		}
		if total == 0 {
			if h := c.UsageHistory(r.ServerID, d.Store); len(h) > 0 {
				total = h[len(h)-1][2]
			}
		}
		if total > 0 && (small == 0 || total < small) {
			small, label = total, d.Label
		}
	}
	return small, label
}

// RotationSpace estimates the space one disk of a set needs. st (may be nil) gives the sizes of
// disks that are mounted right now.
func (c *Core) RotationSpace(ctx context.Context, r Rotation, st *RotationState) (SpaceEstimate, error) {
	guests, err := c.setGuests(ctx, r)
	if err != nil {
		return SpaceEstimate{}, err
	}
	e := SpaceEstimate{Guests: len(guests)}
	var data, perDay int64
	for _, g := range guests {
		data += g.data
		perDay += g.perDay
		if g.rough {
			e.Rough = append(e.Rough, g.name)
		}
	}
	e.Base, e.PerDay = int64(float64(data)*spaceCompress), int64(float64(perDay)*spaceCompress)
	k := rotKeep(r)
	e.KeepAll = !k.Any()
	now := time.Now()
	// One backup of everything, plus for each further one what changed since the one before –
	// at most everything once more (old data is rewritten, not added up day by day).
	need := func(k prox.Keep) int64 {
		_, _, gaps := simulateKeep(r, len(r.Disks), k, now)
		total := e.Base
		for _, g := range gaps {
			total += min(e.PerDay*int64(g), e.Base)
		}
		return int64(float64(total) * spaceReserve)
	}
	e.Keep, e.SpanDays, _ = simulateKeep(r, len(r.Disks), k, now)
	e.Need = need(k)
	e.Smallest, e.Label = c.diskSizes(r, st)
	switch {
	case e.Guests == 0:
		e.Fits, e.Note = "unknown", "Noch keine Server, die auf die Platten kommen."
	case e.Smallest == 0:
		e.Fits = "unknown"
	case e.Need <= e.Smallest:
		e.Fits = "yes"
	default:
		e.Fits = "no"
	}
	if e.KeepAll && e.Smallest > 0 && e.PerDay > 0 {
		free := float64(e.Smallest)/spaceReserve - float64(e.Base)
		e.LastsDay = max(int(free/float64(e.PerDay)), 0)
	}
	if e.Fits == "no" {
		e.MinDisk = diskSizeFor(e.Need)
		e.Suggest = fitKeep(k, e.Smallest, need)
	}
	return e, nil
}

// fitKeep looks for a smaller retention that fits on the disk: fewer monthly, then weekly, then
// daily backups ("" when even one backup per server does not fit).
func fitKeep(k prox.Keep, size int64, need func(prox.Keep) int64) string {
	if !k.Any() {
		k = prox.Keep{Daily: 7, Weekly: 4, Monthly: 3}
	}
	for _, f := range []*int{&k.Monthly, &k.Weekly, &k.Daily, &k.Last} {
		for *f > 0 {
			if need(k) <= size {
				return keepWords(k)
			}
			*f--
		}
	}
	if k = (prox.Keep{Last: 1}); need(k) <= size {
		return keepWords(k)
	}
	return ""
}

func keepWords(k prox.Keep) string {
	var p []string
	for _, x := range []struct {
		n    int
		word string
	}{{k.Last, "letzte"}, {k.Daily, "tägliche"}, {k.Weekly, "wöchentliche"}, {k.Monthly, "monatliche"}} {
		if x.n > 0 {
			p = append(p, fmt.Sprintf("%d %s", x.n, x.word))
		}
	}
	return strings.Join(p, ", ")
}

// diskSizeText writes a disk size the way it is sold: "2 TB", "500 GB".
func diskSizeText(n int64) string {
	if n >= 1e12 {
		return strings.Replace(strconv.FormatFloat(float64(n)/1e12, 'f', -1, 64), ".", ",", 1) + " TB"
	}
	return strconv.FormatInt(n/1e9, 10) + " GB"
}

// diskSizeFor rounds a need up to a disk size that is sold (decimal, as on the box).
func diskSizeFor(need int64) int64 {
	for _, tb := range []float64{0.5, 1, 2, 4, 5, 8, 10, 12, 16, 20, 24} {
		if s := int64(tb * 1e12); s >= need {
			return s
		}
	}
	return need
}

// checkRotationSpace warns once a week when the disks of a set are too small for what they are
// meant to hold (looked at once a day).
func (c *Core) checkRotationSpace(ctx context.Context, r Rotation, st RotationState, now time.Time) {
	if len(r.Disks) == 0 || now.Hour() < 9 {
		return
	}
	dayKey := "usbspace-day:" + r.ID
	var day string
	_ = c.St.GetSetting(dayKey, &day)
	if day == now.Format("2006-01-02") || !c.tryNow(dayKey, 30*time.Minute, now) {
		return
	}
	e, err := c.RotationSpace(ctx, r, &st)
	if err != nil {
		return // server not reachable: again in half an hour
	}
	_ = c.St.SetSetting(dayKey, now.Format("2006-01-02"))
	if e.Fits != "no" {
		return
	}
	key := "usbspace-warned:" + r.ID
	var last int64
	_ = c.St.GetSetting(key, &last)
	if now.Unix()-last < 7*24*3600 {
		return
	}
	_ = c.St.SetSetting(key, now.Unix())
	msg := fmt.Sprintf("Auf jede Platte des Satzes „%s“ kommen bei der eingestellten Aufbewahrung (%s) voraussichtlich etwa %s – die kleinste Platte („%s“) hat %s. Sie wird volllaufen, dann schlagen die Sicherungen fehl.",
		r.Name, cmp.Or(keepWords(rotKeep(r)), "alles behalten"), fmtBytes(e.Need), e.Label, fmtBytes(e.Smallest))
	var tips []string
	if e.Suggest != "" {
		tips = append(tips, "die Aufbewahrung auf „"+e.Suggest+"“ verringern")
	}
	if e.MinDisk > 0 {
		tips = append(tips, "Platten mit mindestens "+diskSizeText(e.MinDisk)+" verwenden")
	}
	if len(tips) > 0 {
		msg += " Abhilfe: " + strings.Join(tips, " oder ") + "."
	}
	c.EventDetails("warning", rotationSource, "USB-Platten von „"+r.Name+"“ werden zu klein", msg,
		&notify.Details{Status: "warning", Link: "/wechselplatten", Facts: []notify.Fact{
			{Label: "Bedarf je Platte", Value: fmtBytes(e.Need)}, {Label: "Kleinste Platte", Value: fmtBytes(e.Smallest)},
			{Label: "Stände je Server", Value: fmt.Sprintf("%d über %d Tage", e.Keep, e.SpanDays)}}})
}
