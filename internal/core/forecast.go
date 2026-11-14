package core

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
)

// Space forecast: the appliance keeps one usage value per day for every datastore (fixed ones
// from the monitor every minute, USB disks whenever they are plugged in), fills the first year
// from the history of the Backup Server and extrapolates the growth of the last weeks.

// UsagePoint is the usage of a datastore at the end of a day: time, used, total (bytes).
type UsagePoint [3]int64

// Forecast is the outlook of one datastore.
type Forecast struct {
	Points    []UsagePoint `json:"points"`                // last 180 days
	Used      int64        `json:"used"`                  // newest value
	Total     int64        `json:"total"`                 //
	GrowthDay int64        `json:"growth_day"`            // bytes a day (negative: shrinks)
	FullAt    int64        `json:"full_at,omitempty"`     // when it is full at this rate
	DaysLeft  int          `json:"days_left"`             // -1: not foreseeable
	BasisDays int          `json:"basis_days"`            // days of history the rate is based on
	State     string       `json:"state"`                 // unknown, stable, ok, soon, urgent, critical
	Pct90At   int64        `json:"pct90_at,omitempty"`    // when 90 % are reached
	Removable bool         `json:"removable,omitempty"`   // USB disk: values only when plugged in
	Sample    int64        `json:"last_sample,omitempty"` // day of the newest value
}

const (
	usageKeep       = 400 // days of history
	forecastWindow  = 60  // days the growth is measured over
	forecastWindowR = 180 // … for USB disks (fewer values)
)

var usageMu sync.Mutex

func usageKey(srvID, store string) string { return srvID + "|" + store }

func dayStart(t time.Time) int64 {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location()).Unix()
}

// UsageHistory returns the daily values of a datastore, oldest first.
func (c *Core) UsageHistory(srvID, store string) []UsagePoint {
	var h []UsagePoint
	_ = c.St.GetSetting("usage:"+usageKey(srvID, store), &h)
	return h
}

// addUsage stores the value of today (the day keeps its newest value).
func (c *Core) addUsage(srvID, store string, now time.Time, used, total int64) {
	usageMu.Lock()
	defer usageMu.Unlock()
	h := c.UsageHistory(srvID, store)
	day := dayStart(now)
	if n := len(h); n > 0 && h[n-1][0] == day {
		h[n-1] = UsagePoint{day, used, total}
	} else {
		h = append(h, UsagePoint{day, used, total})
	}
	if len(h) > usageKeep {
		h = h[len(h)-usageKeep:]
	}
	_ = c.St.SetSetting("usage:"+usageKey(srvID, store), h)
}

// RecordUsage takes the values of the overview (called every minute by the monitor; each
// datastore is written at most once an hour).
func (c *Core) RecordUsage(ov *Overview) {
	now := time.Now()
	for _, s := range ov.Servers {
		if !s.Online {
			continue
		}
		for _, d := range s.Datastores {
			if d.Total <= 0 || d.Error != "" || d.MountStatus == "notmounted" {
				continue
			}
			key := usageKey(s.ID, d.Store)
			c.mu.Lock()
			if c.usageAt == nil {
				c.usageAt = map[string]time.Time{}
			}
			last, seen := c.usageAt[key]
			due := !seen || now.Sub(last) >= time.Hour || dayStart(last) != dayStart(now)
			if due {
				c.usageAt[key] = now
			}
			c.mu.Unlock()
			if !due {
				continue
			}
			removable := d.MountStatus == "mounted"
			if !seen && !removable {
				c.backfillUsage(s.PBSServer, d.Store)
			}
			c.addUsage(s.ID, d.Store, now, d.Used, d.Total)
			c.forecastAlert(s.PBSServer, d.Store, removable)
		}
	}
}

// backfillUsage fills the history once from the year archive of the Backup Server.
func (c *Core) backfillUsage(srv PBSServer, store string) {
	key := "usage-backfill:" + usageKey(srv.ID, store)
	var done bool
	if _ = c.St.GetSetting(key, &done); done {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rrd, err := c.PBSClient(srv).DatastoreRRD(ctx, store, "year")
	if err != nil {
		return
	}
	daily := map[int64]UsagePoint{}
	for _, p := range rrd { // oldest first: the last value of a day wins
		if p.Used == nil || p.Total == nil || *p.Total <= 0 {
			continue
		}
		day := dayStart(time.Unix(p.Time, 0))
		daily[day] = UsagePoint{day, int64(*p.Used), int64(*p.Total)}
	}
	usageMu.Lock()
	defer usageMu.Unlock()
	h := c.UsageHistory(srv.ID, store)
	have := map[int64]bool{}
	for _, p := range h {
		have[p[0]] = true
	}
	for day, p := range daily {
		if !have[day] {
			h = append(h, p)
		}
	}
	sort.Slice(h, func(i, j int) bool { return h[i][0] < h[j][0] })
	if len(h) > usageKeep {
		h = h[len(h)-usageKeep:]
	}
	_ = c.St.SetSetting("usage:"+usageKey(srv.ID, store), h)
	_ = c.St.SetSetting(key, true)
}

// MakeForecast extrapolates the growth of the last weeks (least squares over the daily values).
func MakeForecast(h []UsagePoint, removable bool, now time.Time) Forecast {
	f := Forecast{Points: []UsagePoint{}, DaysLeft: -1, State: "unknown", Removable: removable}
	from := now.AddDate(0, 0, -180).Unix()
	for _, p := range h {
		if p[0] >= from {
			f.Points = append(f.Points, p)
		}
	}
	if len(h) == 0 {
		return f
	}
	last := h[len(h)-1]
	f.Used, f.Total, f.Sample = last[1], last[2], last[0]
	window := forecastWindow
	if removable {
		window = forecastWindowR
	}
	start := now.AddDate(0, 0, -window).Unix()
	var xs, ys []float64
	for _, p := range h {
		if p[0] >= start && p[2] == last[2] { // a bigger disk starts a new history
			xs = append(xs, float64(p[0]-last[0])/86400)
			ys = append(ys, float64(p[1]))
		}
	}
	if len(xs) < 4 || xs[len(xs)-1]-xs[0] < 14 {
		return f
	}
	f.BasisDays = int(math.Round(xs[len(xs)-1] - xs[0]))
	var mx, my float64
	for i := range xs {
		mx += xs[i]
		my += ys[i]
	}
	mx /= float64(len(xs))
	my /= float64(len(xs))
	var sxy, sxx float64
	for i := range xs {
		sxy += (xs[i] - mx) * (ys[i] - my)
		sxx += (xs[i] - mx) * (xs[i] - mx)
	}
	if sxx == 0 {
		return f
	}
	slope := sxy / sxx
	f.GrowthDay = int64(slope)
	free := float64(f.Total - f.Used)
	if free <= 0 {
		f.State, f.DaysLeft, f.FullAt = "critical", 0, last[0]
		return f
	}
	if slope <= 0 || free/slope > 3650 {
		f.State = "stable"
		return f
	}
	from0 := time.Unix(last[0], 0)
	f.FullAt = from0.Add(time.Duration(free / slope * 24 * float64(time.Hour))).Unix()
	f.DaysLeft = max(0, int(math.Floor(float64(f.FullAt-now.Unix())/86400)))
	if p90 := float64(f.Total)*0.9 - float64(f.Used); p90 > 0 {
		f.Pct90At = from0.Add(time.Duration(p90 / slope * 24 * float64(time.Hour))).Unix()
	}
	switch {
	case f.DaysLeft <= 7:
		f.State = "critical"
	case f.DaysLeft <= 30:
		f.State = "urgent"
	case f.DaysLeft <= 90:
		f.State = "soon"
	default:
		f.State = "ok"
	}
	return f
}

// Forecast returns the outlook of a datastore.
func (c *Core) Forecast(srvID, store string, removable bool) Forecast {
	return MakeForecast(c.UsageHistory(srvID, store), removable, time.Now())
}

// forecastAlert raises an event when a datastore will be full within 30 or 7 days (once per
// level; quiet again when the outlook is above 45 days).
func (c *Core) forecastAlert(srv PBSServer, store string, removable bool) {
	f := c.Forecast(srv.ID, store, removable)
	level := 0
	switch f.State {
	case "urgent":
		level = 1
	case "critical":
		level = 2
	}
	key := "fcalert:" + usageKey(srv.ID, store)
	var old int
	_ = c.St.GetSetting(key, &old)
	if level > old {
		sev, status := "warning", "warning"
		if level == 2 {
			sev, status = "critical", "error"
		}
		title := fmt.Sprintf("Datastore %s ist voraussichtlich in %d Tagen voll", store, f.DaysLeft)
		if f.DaysLeft <= 0 {
			title = "Datastore " + store + " ist voll"
		}
		body := fmt.Sprintf("Auf %s / %s sind %s von %s belegt; zuletzt kamen täglich etwa %s dazu. Ist der Datastore voll, schlagen die Sicherungen fehl. Was hilft, steht unter Datastores → Prognose.",
			srv.Name, store, fmtBytes(f.Used), fmtBytes(f.Total), fmtBytes(f.GrowthDay))
		c.EventDetails(sev, srv.Name, title, body, &notify.Details{Status: status, Link: "/datastores?server=" + srv.ID,
			Facts: []notify.Fact{{Label: "Datastore", Value: srv.Name + " / " + store}, {Label: "Voll etwa", Value: time.Unix(f.FullAt, 0).Format("02.01.2006")}}})
		_ = c.St.SetSetting(key, level)
	} else if level == 0 && old > 0 && (f.State == "stable" || f.DaysLeft > 45) {
		_ = c.St.SetSetting(key, 0)
	}
}

// SpaceIssue is a datastore that runs full soon (for the overview).
type SpaceIssue struct {
	Server   string `json:"server"`
	ServerID string `json:"server_id"`
	Store    string `json:"store"`
	DaysLeft int    `json:"days_left"`
	FullAt   int64  `json:"full_at"`
	State    string `json:"state"`
}

// SpaceIssues lists the datastores of the overview that will be full within 30 days.
func (c *Core) SpaceIssues(ov Overview) []SpaceIssue {
	out := []SpaceIssue{}
	for _, s := range ov.Servers {
		for _, d := range s.Datastores {
			f := c.Forecast(s.ID, d.Store, d.MountStatus == "mounted" || d.MountStatus == "notmounted")
			if f.State == "urgent" || f.State == "critical" {
				out = append(out, SpaceIssue{Server: s.Name, ServerID: s.ID, Store: d.Store, DaysLeft: f.DaysLeft, FullAt: f.FullAt, State: f.State})
			}
		}
	}
	return out
}

// SpaceAdvice says what helps when a datastore runs full.
func (c *Core) SpaceAdvice(ctx context.Context, srv PBSServer, store string, f Forecast, gcSchedule string, prune *prox.Job, rot *Rotation) []string {
	var out []string
	if gc, err := c.PBSClient(srv).GCStatus(ctx, store); err == nil && f.Total > 0 && gc.PendingBytes > f.Total/50 {
		out = append(out, fmt.Sprintf("%s warten auf die Speicherfreigabe – sie werden frei, sobald „Speicher freigeben“ das nächste Mal läuft (frühestens einen Tag nach dem Aufräumen).", fmtBytes(gc.PendingBytes)))
	}
	if gcSchedule == "" && rot == nil {
		out = append(out, "Die Speicherfreigabe (Garbage Collection) hat keinen Zeitplan: Aufgeräumte Sicherungen geben ihren Platz nie frei. Unter Aufbewahrung und Zeitpläne z. B. täglich einstellen.")
	}
	keep := func(name string, v, least int) string {
		if v > least {
			return fmt.Sprintf("%s %d → %d", name, v, max(least, v/2))
		}
		return ""
	}
	join := func(parts ...string) string {
		var p []string
		for _, x := range parts {
			if x != "" {
				p = append(p, x)
			}
		}
		return strings.Join(p, ", ")
	}
	const bigger = "Mehr Platz: die Platte der VM oder die Freigabe vergrößern oder einen weiteren Datastore anlegen und die Sicherungsjobs aufteilen."
	switch {
	case rot != nil:
		if rot.TransferLast == 0 {
			out = append(out, "Auf die Wechselplatten werden alle Sicherungen kopiert. Nur die neuesten zu kopieren spart viel Platz: Wechselplatten → Satz bearbeiten → „Nur die neuesten“, z. B. 7.")
		}
		if s := join(keep("tägl.", rot.KeepDaily, 7), keep("wöch.", rot.KeepWeekly, 4), keep("monatl.", rot.KeepMonthly, 6), keep("letzte", rot.KeepLast, 3)); s != "" {
			out = append(out, "Weniger Stände auf den Platten behalten: "+s+".")
		}
		out = append(out, "Größere Platten für diesen Satz verwenden; eine neue Platte bekommt beim ersten Anstecken alles.")
	case prune == nil:
		out = append(out, "Für diesen Datastore ist keine Aufbewahrung eingestellt – es wird nie etwas gelöscht. Unter Aufbewahrung und Zeitpläne z. B. 7 tägliche, 4 wöchentliche und 6 monatliche Stände einstellen.", bigger)
	default:
		if s := join(keep("tägl.", prune.KeepDaily, 7), keep("wöch.", prune.KeepWeekly, 4), keep("monatl.", prune.KeepMonthly, 6), keep("jährl.", prune.KeepYearly, 1), keep("letzte", prune.KeepLast, 3)); s != "" {
			out = append(out, "Weniger Stände behalten: "+s+". Was wegfällt, ist nach der nächsten Speicherfreigabe frei.")
		}
		out = append(out, bigger)
	}
	return out
}
