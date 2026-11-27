package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"pk95proxbackup/internal/notify"
)

// Proof of backup: for a period and a choice of guests, when each was backed up (from the
// recorded runs), where its backups lie today and when a restore of it was last tested. For
// insurers, auditors and customers – printable, without anything secret.

const eventsKept = 180 * 24 * time.Hour // see PruneEvents in the monitor

// ProofRequest says what the proof covers.
type ProofRequest struct {
	From, To int64    // To exclusive
	Guests   []string // "vm/101", "ct/110"; empty = all
	For      string   // company or customer
	By       string
}

// ProofCopy is where backups of a guest lie today.
type ProofCopy struct {
	Where string `json:"where"`
	Kind  string `json:"kind"` // fixed, cloud, usb, archive
	Count int    `json:"count"`
	Last  int64  `json:"last"`
	Seen  int64  `json:"seen,omitempty"` // disk not plugged in: when its content was read
}

// ProofGuest is one VM or container in the proof.
type ProofGuest struct {
	Kind     string      `json:"kind"` // VM, CT
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	OK       int         `json:"ok"`
	Failed   int         `json:"failed"`
	LastOK   int64       `json:"last_ok"`
	Days     int         `json:"days"`    // days with at least one successful backup
	MaxGap   int         `json:"max_gap"` // longest stretch in days without a successful backup
	Copies   []ProofCopy `json:"copies"`
	TestAt   int64       `json:"test_at,omitempty"`
	TestOK   string      `json:"test_status,omitempty"` // ok, warning, error
	TestNote string      `json:"test_note,omitempty"`
	Status   string      `json:"status"` // ok, warning, error
	Gone     bool        `json:"gone"`   // no longer in Proxmox VE
}

// ProofReport is the content of the document.
type ProofReport struct {
	From, To, Built int64
	Host, For, By   string
	Days            int
	Guests          []ProofGuest
	Runs, RunsOK    int
	Offsite         bool // any copy outside the main server (second server, cloud, USB)
	Tests           int  // restore tests in the period
	Partial         int64
	Status          string
	Headline        string
}

// BuildProof gathers the proof for a period.
func (c *Core) BuildProof(ctx context.Context, req ProofRequest) (ProofReport, error) {
	now := time.Now()
	if req.To <= req.From {
		return ProofReport{}, errors.New("Der Zeitraum endet vor seinem Beginn.")
	}
	if req.To > now.Unix() {
		req.To = now.Unix()
	}
	host, _ := os.Hostname()
	rep := ProofReport{From: req.From, To: req.To, Built: now.Unix(), Host: host, For: strings.TrimSpace(req.For), By: req.By,
		Days: int(math.Ceil(time.Unix(req.To, 0).Sub(time.Unix(req.From, 0)).Hours()/24 - 0.1))} // DST: 745 h are 31 days
	if oldest := now.Add(-eventsKept).Unix(); req.From < oldest {
		rep.Partial = oldest
	}
	want := map[string]bool{}
	for _, g := range req.Guests {
		want[strings.ToLower(g)] = true
	}
	pick := func(key string) bool { return len(want) == 0 || want[key] }

	type acc struct {
		g   *ProofGuest
		oks []int64
	}
	guests := map[string]*acc{}
	get := func(key, kind, id string) *acc {
		a := guests[key]
		if a == nil {
			a = &acc{g: &ProofGuest{Kind: kind, ID: id, Copies: []ProofCopy{}}}
			guests[key] = a
		}
		return a
	}
	evs, err := c.St.EventsBetween(ctx, req.From, req.To)
	if err != nil {
		return rep, err
	}
	for _, e := range evs {
		var d notify.Details
		if len(e.Details) == 0 || json.Unmarshal(e.Details, &d) != nil || d.RowsTitle != "Gesicherte Server" {
			continue
		}
		counted := false
		for _, r := range d.Rows {
			key := strings.ToLower(r.Kind) + "/" + r.ID
			if !pick(key) {
				continue
			}
			if !counted {
				rep.Runs++
				if d.Status == "ok" {
					rep.RunsOK++
				}
				counted = true
			}
			a := get(key, r.Kind, r.ID)
			if r.Name != "" {
				a.g.Name = r.Name
			}
			if r.Status == "ok" {
				a.g.OK++
				a.oks = append(a.oks, e.TS)
				if e.TS > a.g.LastOK {
					a.g.LastOK = e.TS
				}
			} else {
				a.g.Failed++
			}
		}
	}
	// Where the backups lie today; guests without a run in the period count too.
	cov, _ := c.Coverage(ctx)
	main := c.localServerID()
	for _, gc := range cov {
		if gc.Template == 1 {
			continue
		}
		kind, typ := "VM", "vm"
		if gc.Type == "lxc" {
			kind, typ = "CT", "ct"
		}
		key := typ + "/" + strconv.Itoa(gc.VMID)
		if !pick(key) || len(gc.Backups)+len(gc.Seen) == 0 && guests[key] == nil {
			continue
		}
		a := get(key, kind, strconv.Itoa(gc.VMID))
		a.g.Name = cmp.Or(gc.Name, a.g.Name)
		for _, b := range gc.Backups {
			a.g.Copies = append(a.g.Copies, proofCopy(b, false))
			if b.Kind != "fixed" || b.Server != main {
				rep.Offsite = true
			}
		}
		for _, b := range gc.Seen {
			a.g.Copies = append(a.g.Copies, proofCopy(b, true))
			rep.Offsite = true
		}
	}
	known := map[string]bool{}
	for _, gc := range cov {
		t := "vm"
		if gc.Type == "lxc" {
			t = "ct"
		}
		known[t+"/"+strconv.Itoa(gc.VMID)] = true
	}
	// The last restore test of each guest in the period.
	for _, run := range c.RestoreTestRuns() {
		if run.Start < req.From || run.Start >= req.To || run.Status == "running" {
			continue
		}
		counted := false
		for _, x := range run.Results {
			key := x.Type + "/" + x.VMID
			a := guests[key]
			if a == nil || x.Status == "skipped" || x.Status == "pending" {
				continue
			}
			if !counted {
				rep.Tests++
				counted = true
			}
			if run.Start >= a.g.TestAt {
				a.g.TestAt, a.g.TestOK = run.Start, x.Status
				a.g.TestNote = cmp.Or(x.OS, x.Info)
			}
		}
	}
	end := req.To
	for key, a := range guests {
		g := a.g
		g.Gone = len(known) > 0 && !known[key]
		g.Days, g.MaxGap = coverageDays(a.oks, req.From, end)
		switch {
		case g.OK == 0:
			g.Status = "error"
		case g.Failed > 0 || g.MaxGap > 2:
			g.Status = "warning"
		default:
			g.Status = "ok"
		}
		sort.Slice(g.Copies, func(i, j int) bool { return copyRank(g.Copies[i].Kind) < copyRank(g.Copies[j].Kind) })
		rep.Guests = append(rep.Guests, *g)
	}
	sort.Slice(rep.Guests, func(i, j int) bool {
		a, b := rep.Guests[i], rep.Guests[j]
		if a.Kind != b.Kind {
			return a.Kind > b.Kind // VM before CT
		}
		x, _ := strconv.Atoi(a.ID)
		y, _ := strconv.Atoi(b.ID)
		return x < y
	})
	rep.Status, rep.Headline = rep.verdict()
	return rep, nil
}

func proofCopy(b BackupRef, seen bool) ProofCopy {
	where := b.ServerName + " / " + b.Store
	if b.Label != "" {
		where = b.Label
	}
	p := ProofCopy{Where: where, Kind: b.Kind, Count: b.Count, Last: b.Last}
	if seen {
		p.Seen = b.Seen
	}
	return p
}

func copyRank(k string) int {
	switch k {
	case "fixed":
		return 0
	case "cloud":
		return 1
	case "usb":
		return 2
	}
	return 3
}

// coverageDays counts the local days with a successful backup and the longest stretch of days
// without one (from the start of the period to its end).
func coverageDays(oks []int64, from, to int64) (int, int) {
	sort.Slice(oks, func(i, j int) bool { return oks[i] < oks[j] })
	days := map[string]bool{}
	gap, prev := 0, from
	for _, t := range oks {
		days[time.Unix(t, 0).Format("2006-01-02")] = true
		if g := int((t - prev) / 86400); g > gap {
			gap = g
		}
		prev = t
	}
	if g := int((to - prev) / 86400); g > gap {
		gap = g
	}
	return len(days), gap
}

func (r ProofReport) verdict() (string, string) {
	bad, warn := 0, 0
	for _, g := range r.Guests {
		switch g.Status {
		case "error":
			bad++
		case "warning":
			warn++
		}
	}
	n := len(r.Guests)
	switch {
	case n == 0:
		return "warning", "Im Zeitraum wurde kein Server gesichert."
	case bad > 0:
		return "error", fmt.Sprintf("%d von %d Servern wurden im Zeitraum nicht erfolgreich gesichert.", bad, n)
	case warn > 0:
		return "warning", fmt.Sprintf("Alle %d Server wurden gesichert, %d mit einzelnen Fehlern oder Lücken.", n, warn)
	}
	return "ok", fmt.Sprintf("Alle %d Server wurden im Zeitraum regelmäßig gesichert.", n)
}
