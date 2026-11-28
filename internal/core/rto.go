package core

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/notify"
)

// Recovery time per guest: how long bringing a VM or container back from its backup would
// take. Best is a restore test of the guest itself (restore plus start until it answered);
// otherwise the speed the restore tests reached with other guests (data per second), applied to
// the data of the guest; without any restore test an assumed 100 MB/s. A target time (default
// for all, or per guest) makes the appliance warn when the estimate exceeds it.

const (
	rtoAssumedRate = 100e6 // bytes per second without any measurement
	rtoMinSecs     = 30    // shorter restores say little about the speed
	rtoMaxAge      = 365 * 24 * 3600
	rtoMaxTarget   = 7 * 24 * 60 // minutes
	rtoSource      = "Wiederherstellungszeit"
)

// RTOSettings are the target times in minutes (0: none).
type RTOSettings struct {
	Default int            `json:"default"`
	Guests  map[string]int `json:"guests"` // "cluster/vmid" → minutes; -1: no target for this guest
}

// GuestRTO is the estimate for one guest.
type GuestRTO struct {
	Secs   int64   `json:"secs"`             // restore
	Boot   int64   `json:"boot,omitempty"`   // start until it answered (measured only)
	Basis  string  `json:"basis"`            // measured, derived, assumed
	Tested int64   `json:"tested,omitempty"` // measured: when the restore test ran
	Scaled bool    `json:"scaled,omitempty"` // measured: disks grew since, scaled up
	Rate   float64 `json:"rate,omitempty"`   // derived/assumed: MB/s
	Data   int64   `json:"data,omitempty"`   // derived/assumed: bytes of data
	Rough  bool    `json:"rough,omitempty"`  // data: size of the disks (no backup log yet)
	Target int     `json:"target"`           // minutes (0: none)
	Own    bool    `json:"own_target,omitempty"`
	Over   bool    `json:"over"`
}

// Total is restore plus start.
func (r GuestRTO) Total() int64 { return r.Secs + r.Boot }

var errRTOMinutes = errors.New("Ziel-Zeit 5 Minuten bis 7 Tage oder keine.")

// RTOSettingsOf returns the target times.
func (c *Core) RTOSettingsOf() RTOSettings {
	var s RTOSettings
	_ = c.St.GetSetting("rto", &s)
	if s.Guests == nil {
		s.Guests = map[string]int{}
	}
	return s
}

var rtoMu sync.Mutex

// SetRTODefault sets the target time for all guests without their own (0: none).
func (c *Core) SetRTODefault(minutes int) error {
	if minutes != 0 && (minutes < 5 || minutes > rtoMaxTarget) {
		return errRTOMinutes
	}
	rtoMu.Lock()
	defer rtoMu.Unlock()
	s := c.RTOSettingsOf()
	s.Default = minutes
	return c.St.SetSetting("rto", s)
}

// SetRTOGuest sets the target time of one guest (nil: as all others, -1: none).
func (c *Core) SetRTOGuest(cluster, vmid string, minutes *int) error {
	if _, err := strconv.Atoi(vmid); err != nil || cluster == "" {
		return errors.New("Gast unbekannt.")
	}
	if minutes != nil && *minutes != -1 && (*minutes < 5 || *minutes > rtoMaxTarget) {
		return errRTOMinutes
	}
	rtoMu.Lock()
	defer rtoMu.Unlock()
	s := c.RTOSettingsOf()
	k := cluster + "/" + vmid
	if minutes == nil {
		delete(s.Guests, k)
	} else {
		s.Guests[k] = *minutes
	}
	return c.St.SetSetting("rto", s)
}

// rtoSample is the newest restore test of one guest.
type rtoSample struct {
	t, secs, boot, disk int64
}

// rtoSamples collects the newest restore test per guest ("cluster/vmid") whose restore
// finished.
func rtoSamples(plans []RestoreTestPlan, runs []RestoreTestRun, clusterByName map[string]string, now time.Time) map[string]rtoSample {
	planCluster := map[string]string{}
	for _, p := range plans {
		planCluster[p.ID] = p.Cluster
	}
	out := map[string]rtoSample{}
	for _, run := range runs {
		cl := cmp.Or(planCluster[run.PlanID], clusterByName[run.Cluster])
		if cl == "" {
			continue
		}
		for _, r := range run.Results {
			if r.RestoreEnd == 0 || r.Restore <= 0 || now.Unix()-r.RestoreEnd > rtoMaxAge {
				continue
			}
			k := cl + "/" + r.VMID
			if o, ok := out[k]; ok && o.t >= r.Started {
				continue
			}
			s := rtoSample{t: r.Started, secs: r.Restore, disk: r.DiskBytes}
			if r.Status == "ok" || r.Status == "warning" {
				s.boot = r.Boot
			}
			out[k] = s
		}
	}
	return out
}

func medianF(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	v = slices.Clone(v)
	sort.Float64s(v)
	return v[len(v)/2]
}

// rtoEstimates adds the estimate to every guest that has a backup.
func (c *Core) rtoEstimates(guests []GuestCoverage, plans []RestoreTestPlan, runs []RestoreTestRun, now time.Time) {
	var sizes map[string]*sizeState
	_ = c.St.GetSetting("guestsize", &sizes)
	byName := map[string]string{}
	for _, g := range guests {
		byName[g.ClusterName] = g.Cluster
	}
	samples := rtoSamples(plans, runs, byName, now)
	// Speed of the restore tests: data per second, per Proxmox VE and for all.
	rates := map[string][]float64{}
	for k, s := range samples {
		if s.secs < rtoMinSecs {
			continue
		}
		data := s.disk
		if z := sizes[k]; z != nil && z.Data > 0 {
			data = z.Data
		}
		if data <= 0 {
			continue
		}
		cl, _, _ := strings.Cut(k, "/")
		r := float64(data) / float64(s.secs)
		rates[cl] = append(rates[cl], r)
		rates[""] = append(rates[""], r)
	}
	set := c.RTOSettingsOf()
	for i := range guests {
		g := &guests[i]
		if len(g.Backups) == 0 && len(g.Seen) == 0 {
			continue
		}
		k := g.Cluster + "/" + strconv.Itoa(g.VMID)
		var e GuestRTO
		if s, ok := samples[k]; ok {
			e = GuestRTO{Secs: s.secs, Boot: s.boot, Basis: "measured", Tested: s.t}
			if s.disk > 0 && g.MaxDisk > s.disk+s.disk/10 {
				e.Secs, e.Scaled = int64(math.Round(float64(s.secs)*float64(g.MaxDisk)/float64(s.disk))), true
			}
		} else {
			data, rough := g.MaxDisk, true
			if z := sizes[k]; z != nil && z.Data > 0 {
				data, rough = z.Data, false
			}
			if data <= 0 {
				continue
			}
			rate, basis := medianF(rates[g.Cluster]), "derived"
			if rate == 0 {
				rate = medianF(rates[""])
			}
			if rate == 0 {
				rate, basis = rtoAssumedRate, "assumed"
			}
			e = GuestRTO{Secs: max(1, int64(math.Round(float64(data)/rate))), Basis: basis, Rate: math.Round(rate / 1e6), Data: data, Rough: rough}
		}
		e.Target = set.Default
		if m, ok := set.Guests[k]; ok {
			e.Target, e.Own = max(m, 0), true
		}
		e.Over = e.Target > 0 && e.Total() > int64(e.Target)*60
		g.RTO = &e
	}
}

// rtoText writes an estimate for people: "unter 1 Min.", "25 Min.", "2 Std. 15 Min.".
func rtoText(s int64) string {
	if s < 60 {
		return "unter 1 Min."
	}
	if s < 3600 {
		return strconv.FormatInt((s+30)/60, 10) + " Min."
	}
	return minutesText(int((s + 150) / 300 * 5)) // to 5 minutes
}

// BasisText says what an estimate rests on.
func (r GuestRTO) BasisText() string {
	switch r.Basis {
	case "measured":
		s := "gemessen beim Restore-Test am " + time.Unix(r.Tested, 0).Format("02.01.2006")
		if r.Scaled {
			s += ", hochgerechnet auf die größeren Platten"
		}
		return s
	case "derived":
		return fmt.Sprintf("geschätzt aus den Restore-Tests (%.0f MB/s)", r.Rate)
	}
	return "angenommen 100 MB/s, noch kein Restore-Test"
}

type rtoWarned struct {
	T    int64    `json:"t"`
	Keys []string `json:"keys"`
}

// checkRTO warns when guests would take longer than their target: at once when a guest is new
// over it, otherwise once a week.
func (c *Core) checkRTO(guests []GuestCoverage, now time.Time) {
	var over []GuestCoverage
	var keys []string
	for _, g := range guests {
		if g.RTO != nil && g.RTO.Over {
			over = append(over, g)
			keys = append(keys, g.Cluster+"/"+strconv.Itoa(g.VMID))
		}
	}
	var w rtoWarned
	_ = c.St.GetSetting("rtowarned", &w)
	if len(over) == 0 {
		if len(w.Keys) > 0 {
			_ = c.St.SetSetting("rtowarned", rtoWarned{})
		}
		return
	}
	news := false
	for _, k := range keys {
		news = news || !slices.Contains(w.Keys, k)
	}
	if !news && now.Unix()-w.T < 7*24*3600 {
		if len(keys) != len(w.Keys) { // fewer now: remember, no message
			w.Keys = keys
			_ = c.St.SetSetting("rtowarned", w)
		}
		return
	}
	_ = c.St.SetSetting("rtowarned", rtoWarned{T: now.Unix(), Keys: keys})
	who := func(g GuestCoverage) string { return strings.TrimSpace(strconv.Itoa(g.VMID) + " " + g.Name) }
	title := fmt.Sprintf("Wiederherstellung von %d Gästen dauert länger als die Ziel-Zeit", len(over))
	text := fmt.Sprintf("%d Gäste bräuchten nach der Schätzung länger als ihre Ziel-Zeit.", len(over))
	if len(over) == 1 {
		g := over[0]
		title = "Wiederherstellung von " + who(g) + " dauert länger als die Ziel-Zeit"
		text = fmt.Sprintf("Nach der Schätzung dauert die Wiederherstellung von %s etwa %s, mehr als die Ziel-Zeit (%s).", who(g), rtoText(g.RTO.Total()), minutesText(g.RTO.Target))
	}
	text += " Abhilfe: von einem schnellen lokalen Datastore statt von USB oder aus der Cloud wiederherstellen, beim Wiederherstellen „Live-Restore“ wählen (die VM startet, während die Daten noch kommen), schnellere Platten oder ein schnelleres Netzwerk am Backup-Server. Ein Restore-Test misst die tatsächliche Zeit."
	d := &notify.Details{Status: "warning", Link: "/sicherungen"}
	for _, g := range over {
		d.Rows = append(d.Rows, notify.Row{Kind: map[bool]string{true: "CT", false: "VM"}[g.Type == "lxc"], ID: strconv.Itoa(g.VMID), Name: g.Name,
			Status: "warning", Duration: "ca. " + rtoText(g.RTO.Total()), Info: "Ziel " + minutesText(g.RTO.Target) + " · " + g.RTO.BasisText()})
	}
	c.EventDetails("warning", rtoSource, title, text, d)
}
