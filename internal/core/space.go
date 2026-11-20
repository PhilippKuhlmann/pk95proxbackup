package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/hostexec"
	"pk95proxbackup/internal/prox"
)

// Space per guest: PBS deduplicates chunks across all backups of a datastore, so the size of a
// backup says little about the space it takes. A script on the Backup Server reads the index
// files of every snapshot, counts which groups use each chunk and adds up the chunk files:
// "share" splits shared chunks evenly (the shares add up to the chunk store), "exclusive" is
// what only this group uses (freed by deleting it and a garbage collection). Measured once a
// day; the history gives the growth.

const (
	spaceMarker   = "# pk95-space"
	spaceEvery    = 24 * time.Hour
	spaceTimeout  = 3 * time.Hour
	spaceHistDays = 120
)

// GuestSpace is the space of one backup group in a datastore.
type GuestSpace struct {
	Group     string `json:"group"` // [ns/]vm/101
	NS        string `json:"ns,omitempty"`
	Type      string `json:"type"` // vm, ct, host
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Share     int64  `json:"share"`
	Exclusive int64  `json:"exclusive"`
	Chunks    int64  `json:"chunks"`
	Logical   int64  `json:"logical"` // newest backup, as PBS reports it
	Snapshots int    `json:"snapshots"`
	Growth30  int64  `json:"growth30"` // change of the share over 30 days (0 without history)
	Basis     int    `json:"basis"`    // days the growth is based on
	Gone      bool   `json:"gone"`     // guest no longer in any connected Proxmox VE
}

// StoreSpace is the last measurement of a datastore.
type StoreSpace struct {
	ServerID   string       `json:"server_id"`
	ServerName string       `json:"server_name"`
	Store      string       `json:"store"`
	Measured   int64        `json:"measured"`
	Duration   int64        `json:"duration"`
	Chunks     int64        `json:"chunks"` // all referenced chunks
	Total      int64        `json:"total"`  // their size on disk
	Guests     []GuestSpace `json:"guests"`
	Error      string       `json:"error,omitempty"`
	Running    bool         `json:"running"`
	Shell      bool         `json:"shell"` // the appliance can run the script there
}

type spacePoint struct {
	Day   string `json:"d"`
	Share int64  `json:"s"`
}

type spaceRuntime struct {
	mu   sync.Mutex
	busy map[string]bool
}

func spaceKey(srv, store string) string { return srv + "|" + store }

func (c *Core) spaceAll() map[string]StoreSpace {
	m := map[string]StoreSpace{}
	_ = c.St.GetSetting("space", &m)
	return m
}

func (c *Core) spaceHist() map[string]map[string][]spacePoint {
	m := map[string]map[string][]spacePoint{}
	_ = c.St.GetSetting("space-hist", &m)
	return m
}

// Space returns the last measurement of a datastore.
func (c *Core) Space(serverID, store string) StoreSpace {
	s, ok := c.spaceAll()[spaceKey(serverID, store)]
	if !ok {
		s = StoreSpace{ServerID: serverID, Store: store}
	}
	if s.Guests == nil {
		s.Guests = []GuestSpace{}
	}
	if srv, err := c.Server(serverID); err == nil {
		s.ServerName = srv.Name
		s.Shell = srv.Local || srv.SSHEnabled
	}
	c.space.mu.Lock()
	s.Running = c.space.busy[spaceKey(serverID, store)]
	c.space.mu.Unlock()
	return s
}

// StartSpace measures a datastore now, in the background.
func (c *Core) StartSpace(serverID, store string) error {
	srv, err := c.Server(serverID)
	if err != nil {
		return err
	}
	if _, err := c.Runner(srv); err != nil {
		return err
	}
	c.space.mu.Lock()
	busy := c.space.busy[spaceKey(serverID, store)]
	c.space.mu.Unlock()
	if busy {
		return errors.New("Die Messung läuft gerade.")
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), spaceTimeout)
		defer cancel()
		if err := c.MeasureSpace(ctx, serverID, store); err != nil {
			log.Printf("space %s/%s: %v", serverID, store, err)
		}
	}()
	return nil
}

// scheduleSpace measures every fixed datastore of servers with shell access once a day.
func (c *Core) scheduleSpace(ctx context.Context, now time.Time) {
	c.space.mu.Lock()
	if c.space.busy["*"] {
		c.space.mu.Unlock()
		return
	}
	c.space.busy = initMap(c.space.busy)
	c.space.busy["*"] = true
	c.space.mu.Unlock()
	go func() {
		defer func() {
			c.space.mu.Lock()
			delete(c.space.busy, "*")
			c.space.mu.Unlock()
		}()
		all := c.spaceAll()
		servers, _ := c.Servers()
		for _, srv := range servers {
			if !srv.Local && !srv.SSHEnabled {
				continue
			}
			cfgs, err := c.PBSClient(srv).Datastores(ctx)
			if err != nil {
				continue
			}
			for _, d := range cfgs {
				if d.BackingDevice != "" || d.Backend != "" || d.Maintenance != "" {
					continue
				}
				last := all[spaceKey(srv.ID, d.Name)]
				if now.Sub(time.Unix(last.Measured, 0)) < spaceEvery && last.Error == "" {
					continue
				}
				if last.Error != "" && now.Sub(time.Unix(last.Measured, 0)) < 6*time.Hour {
					continue // a failed measurement is tried again after six hours
				}
				mctx, cancel := context.WithTimeout(context.Background(), spaceTimeout)
				if err := c.MeasureSpace(mctx, srv.ID, d.Name); err != nil {
					log.Printf("space %s/%s: %v", srv.Name, d.Name, err)
				}
				cancel()
			}
		}
	}()
}

func initMap(m map[string]bool) map[string]bool {
	if m == nil {
		return map[string]bool{}
	}
	return m
}

// MeasureSpace runs the script on the Backup Server and stores the result.
func (c *Core) MeasureSpace(ctx context.Context, serverID, store string) error {
	key := spaceKey(serverID, store)
	c.space.mu.Lock()
	c.space.busy = initMap(c.space.busy)
	if c.space.busy[key] {
		c.space.mu.Unlock()
		return errors.New("läuft bereits")
	}
	c.space.busy[key] = true
	c.space.mu.Unlock()
	defer func() {
		c.space.mu.Lock()
		delete(c.space.busy, key)
		c.space.mu.Unlock()
	}()
	start := time.Now()
	res, err := c.measureSpace(ctx, serverID, store)
	res.ServerID, res.Store, res.Measured, res.Duration = serverID, store, start.Unix(), int64(time.Since(start).Seconds())
	if err != nil {
		prev := c.spaceAll()[key]
		prev.ServerID, prev.Store, prev.Error = serverID, store, err.Error()
		if prev.Measured == 0 {
			prev.Measured = start.Unix()
		}
		c.saveSpace(key, prev, nil)
		return err
	}
	c.saveSpace(key, res, start)
	return nil
}

var spaceMu sync.Mutex

// saveSpace stores a measurement, adds today's shares to the history and computes the growth.
func (c *Core) saveSpace(key string, s StoreSpace, at interface{ Format(string) string }) {
	spaceMu.Lock()
	defer spaceMu.Unlock()
	if at != nil {
		hist := c.spaceHist()
		h := hist[key]
		if h == nil {
			h = map[string][]spacePoint{}
		}
		day := at.Format("2006-01-02")
		present := map[string]bool{}
		for i := range s.Guests {
			g := &s.Guests[i]
			present[g.Group] = true
			pts := h[g.Group]
			if n := len(pts); n > 0 && pts[n-1].Day == day {
				pts[n-1].Share = g.Share
			} else {
				pts = append(pts, spacePoint{Day: day, Share: g.Share})
			}
			if len(pts) > spaceHistDays {
				pts = pts[len(pts)-spaceHistDays:]
			}
			h[g.Group] = pts
			g.Growth30, g.Basis = growth30(pts)
		}
		for grp := range h {
			if !present[grp] {
				delete(h, grp) // a group that is gone starts anew when it comes back
			}
		}
		hist[key] = h
		_ = c.St.SetSetting("space-hist", hist)
	}
	all := c.spaceAll()
	s.Running, s.Shell, s.ServerName = false, false, ""
	all[key] = s
	_ = c.St.SetSetting("space", all)
}

// growth30 is the change of the share over the last 30 days (scaled from a shorter history of
// at least 7 days), and how many days it is based on.
func growth30(pts []spacePoint) (int64, int) {
	if len(pts) < 2 {
		return 0, 0
	}
	last := pts[len(pts)-1]
	lt, err := time.Parse("2006-01-02", last.Day)
	if err != nil {
		return 0, 0
	}
	base := pts[0]
	for _, p := range pts {
		if t, err := time.Parse("2006-01-02", p.Day); err == nil && lt.Sub(t) <= 30*24*time.Hour {
			base = p
			break
		}
	}
	bt, _ := time.Parse("2006-01-02", base.Day)
	days := int(lt.Sub(bt).Hours()/24 + 0.5)
	if days < 7 {
		return 0, days
	}
	return (last.Share - base.Share) * 30 / int64(days), days
}

func (c *Core) measureSpace(ctx context.Context, serverID, store string) (StoreSpace, error) {
	var out StoreSpace
	srv, err := c.Server(serverID)
	if err != nil {
		return out, err
	}
	pbs := c.PBSClient(srv)
	cfgs, err := pbs.Datastores(ctx)
	if err != nil {
		return out, err
	}
	var cfg *prox.DatastoreConfig
	for i := range cfgs {
		if cfgs[i].Name == store {
			cfg = &cfgs[i]
		}
	}
	switch {
	case cfg == nil:
		return out, fmt.Errorf("Datastore %s gibt es nicht", store)
	case cfg.Backend != "":
		return out, errors.New("Bei Cloud-Datastores liegen die Daten im Bucket – dort lässt sich nicht nachzählen.")
	case cfg.Path == "":
		return out, errors.New("Pfad des Datastores unbekannt")
	}
	run, err := c.Runner(srv)
	if err != nil {
		return out, err
	}
	switch r := run.(type) {
	case hostexec.Local:
		run = hostexec.Local{Timeout: spaceTimeout}
	case *hostexec.SSH:
		r.Timeout = spaceTimeout
	}
	text, err := run.Run(ctx, "perl", "-e", spaceScript, cfg.Path)
	if err != nil {
		return out, fmt.Errorf("Messung auf %s: %w", srv.Name, err)
	}
	out, err = parseSpace(text)
	if err != nil {
		return out, err
	}
	// Names, newest size and number of backups from the API.
	names := c.guestNames(ctx)
	snaps := map[string][]prox.Snapshot{}
	nss := map[string]bool{}
	for _, g := range out.Guests {
		nss[g.NS] = true
	}
	for ns := range nss {
		list, err := pbs.Snapshots(ctx, store, ns)
		if err != nil {
			continue
		}
		for _, s := range list {
			k := s.Type + "/" + s.ID
			if ns != "" {
				k = ns + "/" + k
			}
			snaps[k] = append(snaps[k], s)
		}
	}
	for i := range out.Guests {
		g := &out.Guests[i]
		name, known := names[g.Type+"/"+g.ID]
		g.Name = name
		if g.Type == "host" {
			g.Name = g.ID
		}
		if list := snaps[g.Group]; len(list) > 0 {
			sort.Slice(list, func(a, b int) bool { return list[a].Time < list[b].Time })
			newest := list[len(list)-1]
			g.Snapshots, g.Logical = len(list), newest.Size
			if g.Name == "" {
				g.Name, _, _ = strings.Cut(newest.Comment, "\n") // the note of the backup carries the name
			}
		}
		g.Gone = g.Type != "host" && !known && len(names) > 0
	}
	sort.Slice(out.Guests, func(a, b int) bool { return out.Guests[a].Share > out.Guests[b].Share })
	return out, nil
}

// parseSpace reads the output of the script: "G\t<group>\t<share>\t<exclusive>\t<chunks>" per
// group and "T\t<chunks>\t<bytes>" at the end.
func parseSpace(text string) (StoreSpace, error) {
	out := StoreSpace{Guests: []GuestSpace{}}
	done := false
	for _, line := range strings.Split(text, "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		switch {
		case len(f) == 5 && f[0] == "G":
			share, _ := strconv.ParseInt(f[2], 10, 64)
			excl, _ := strconv.ParseInt(f[3], 10, 64)
			n, _ := strconv.ParseInt(f[4], 10, 64)
			g := GuestSpace{Group: f[1], Share: share, Exclusive: excl, Chunks: n}
			parts := strings.Split(f[1], "/")
			if len(parts) < 2 {
				continue
			}
			g.Type, g.ID = parts[len(parts)-2], parts[len(parts)-1]
			g.NS = strings.Join(parts[:len(parts)-2], "/")
			out.Guests = append(out.Guests, g)
		case len(f) == 3 && f[0] == "T":
			out.Chunks, _ = strconv.ParseInt(f[1], 10, 64)
			out.Total, _ = strconv.ParseInt(f[2], 10, 64)
			done = true
		case len(f) == 2 && f[0] == "E":
			return out, errors.New(f[1])
		}
	}
	if !done {
		return out, errors.New("Messung unvollständig")
	}
	return out, nil
}

// spaceScript (perl, part of every Debian) counts the chunks of every group in two passes, so
// it holds one entry per chunk: refcount<<32 | size on disk.
const spaceScript = spaceMarker + `
use strict; use warnings;
my $base = shift or die "E\tkein Pfad\n";
-d "$base/.chunks" or do { print "E\tKein Datastore unter $base (.chunks fehlt)\n"; exit 0 };
my (%ref, @groups);
sub groups { my ($dir, $ns) = @_;
  for my $t ('vm', 'ct', 'host') {
    opendir(my $d, "$dir/$t") or next;
    for my $id (sort grep { !/^\./ } readdir $d) { push @groups, [($ns eq '' ? '' : "$ns/") . "$t/$id", "$dir/$t/$id"] }
  }
  if (opendir(my $n, "$dir/ns")) { for my $x (sort grep { !/^\./ } readdir $n) { groups("$dir/ns/$x", $ns eq '' ? $x : "$ns/$x") } }
}
sub digests { my ($gdir, $cb) = @_;
  opendir(my $sd, $gdir) or return;
  for my $snap (grep { /^\d{4}-/ } readdir $sd) {
    opendir(my $fd, "$gdir/$snap") or next;
    for my $f (readdir $fd) {
      my ($stride, $off);
      if ($f =~ /\.fidx$/) { ($stride, $off) = (32, 0) } elsif ($f =~ /\.didx$/) { ($stride, $off) = (40, 8) } else { next }
      open(my $fh, '<:raw', "$gdir/$snap/$f") or next;
      local $/; my $buf = <$fh>; close $fh;
      next if !defined $buf || length($buf) < 4096;
      for (my $i = 4096 + $off; $i + 32 <= length($buf); $i += $stride) { $cb->(substr($buf, $i, 32)) }
    }
  }
}
groups($base, '');
for my $g (@groups) { my %seen; digests($g->[1], sub { my $d = shift; return if $seen{$d}++; $ref{$d} += 1 << 32 }) }
my ($chunks, $bytes) = (0, 0);
for my $g (@groups) {
  my %seen; my ($share, $excl, $n) = (0, 0, 0);
  digests($g->[1], sub { my $d = shift; return if $seen{$d}++;
    my $v = $ref{$d}; my $size = $v & 0xffffffff;
    if ($size == 0) { my $h = unpack('H*', $d); my @st = stat("$base/.chunks/" . substr($h, 0, 4) . "/$h"); $size = @st ? ($st[12] ? $st[12] * 512 : $st[7]) : 1; $size = 1 if $size < 1; $size = 0xffffffff if $size > 0xffffffff;
      $ref{$d} = $v + $size; $chunks++; $bytes += $size }
    my $c = $v >> 32; $share += $size / $c; $excl += $size if $c == 1; $n++ });
  printf "G\t%s\t%.0f\t%.0f\t%d\n", $g->[0], $share, $excl, $n;
}
print "T\t$chunks\t$bytes\n";
`
