package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/store"
)

// Monthly report for the customer: what was backed up in a month, the USB copies, restore
// tests, the state of the disks and servers and what went wrong. Sent on the 1st of the
// month for the month before (as mail with the report attached), viewable any time.

// MonthlyReport is the content of one month.
type MonthlyReport struct {
	Month    string // "2026-10"
	Title    string // "Oktober 2026"
	From, To int64
	Built    int64
	Host     string
	Status   string // ok, warning, error
	Headline string

	BackupRuns, BackupRunsOK int
	BackupRunsFailed         int // ended without any server backed up
	Guests                   []GuestMonth
	USB                      []USBMonth
	RestoreTests             []RestoreMonth
	Disks                    []DiskHealthState
	Servers                  []ServerHealth
	Archives                 []ArchiveMonth
	Problems                 []ProblemLine
	ConfigBackup             int64 // last backup of the appliance's configuration
	ConfigBackupOn           bool
}

// GuestMonth is one VM or container over the month.
type GuestMonth struct {
	Kind, ID, Name string
	OK, Failed     int
	LastOK         int64
	LastError      string
	Status         string // ok (all fine), warning (some failed), error (the last one failed)
}

// USBMonth is one rotation disk over the month.
type USBMonth struct {
	Set, Label   string
	Copies       int
	Failed       int
	LastOK       int64
	Health       string // ok, warning, error, unknown, ""
	HealthDetail string
	Status       string
}

// RestoreMonth is one restore test run.
type RestoreMonth struct {
	Plan    string
	Start   int64
	Status  string
	OK, All int
}

// ArchiveMonth is an archive disk with when it was last seen.
type ArchiveMonth struct {
	Name, Location string
	LastSeen       int64
	Servers        int
	Stale          bool
}

// ProblemLine is a warning or error of the month (same titles counted together).
type ProblemLine struct {
	Severity string
	Source   string
	Title    string
	Count    int
	Last     int64
}

var monthNames = []string{"", "Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"}

// MonthRange returns the start and end (exclusive) of a month "2026-10".
func MonthRange(month string) (time.Time, time.Time, error) {
	t, err := time.ParseInLocation("2006-01", month, time.Local)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("Monat als JJJJ-MM angeben")
	}
	return t, t.AddDate(0, 1, 0), nil
}

// PreviousMonth returns "2026-10" for a time in November 2026.
func PreviousMonth(now time.Time) string {
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, -1, 0).Format("2006-01")
}

func factValue(d *notify.Details, label string) string {
	for _, f := range d.Facts {
		if f.Label == label {
			return f.Value
		}
	}
	return ""
}

// BuildMonthlyReport collects the report of a month.
func (c *Core) BuildMonthlyReport(ctx context.Context, month string) (MonthlyReport, error) {
	from, to, err := MonthRange(month)
	if err != nil {
		return MonthlyReport{}, err
	}
	host, _ := os.Hostname()
	rep := MonthlyReport{Month: month, Title: monthNames[from.Month()] + " " + from.Format("2006"), From: from.Unix(), To: to.Unix(),
		Built: time.Now().Unix(), Host: host}
	evs, err := c.St.EventsBetween(ctx, rep.From, rep.To)
	if err != nil {
		return rep, err
	}
	guests := map[string]*GuestMonth{}
	usb := map[string]*USBMonth{} // label → disk
	problems := map[string]*ProblemLine{}
	for _, e := range evs {
		var d notify.Details
		if len(e.Details) > 0 {
			_ = json.Unmarshal(e.Details, &d)
		}
		switch {
		case d.RowsTitle == "Gesicherte Server":
			rep.BackupRuns++
			switch d.Status {
			case "ok":
				rep.BackupRunsOK++
			case "error":
				rep.BackupRunsFailed++
			}
			for _, r := range d.Rows {
				key := strings.ToLower(r.Kind) + "/" + r.ID
				g := guests[key]
				if g == nil {
					g = &GuestMonth{Kind: r.Kind, ID: r.ID}
					guests[key] = g
				}
				if r.Name != "" {
					g.Name = r.Name
				}
				if r.Status == "ok" {
					g.OK++
					g.LastOK, g.LastError = e.TS, ""
				} else {
					g.Failed++
					g.LastError = r.Info
				}
			}
		case e.Source == rotationSource && strings.Contains(e.Title, "USB-Sicherung"):
			label := strings.Trim(factValue(&d, "Festplatte"), "„“")
			if label != "" {
				u := usb[label]
				if u == nil {
					u = &USBMonth{Label: label}
					usb[label] = u
				}
				if strings.Contains(e.Title, "fertig") {
					u.Copies++
					u.LastOK = e.TS
				} else if strings.Contains(e.Title, "fehlgeschlagen") {
					u.Failed++
				}
			}
		}
		if e.Severity == "warning" || e.Severity == "critical" {
			k := e.Severity + "|" + e.Source + "|" + e.Title
			p := problems[k]
			if p == nil {
				p = &ProblemLine{Severity: e.Severity, Source: e.Source, Title: e.Title}
				problems[k] = p
			}
			p.Count++
			p.Last = e.TS
		}
	}
	for _, g := range guests {
		switch {
		case g.Failed == 0:
			g.Status = "ok"
		case g.OK == 0 || g.LastError != "":
			g.Status = "error"
		default:
			g.Status = "warning"
		}
		rep.Guests = append(rep.Guests, *g)
	}
	rank := map[string]int{"error": 0, "warning": 1, "ok": 2}
	sort.Slice(rep.Guests, func(i, j int) bool {
		a, b := rep.Guests[i], rep.Guests[j]
		if rank[a.Status] != rank[b.Status] {
			return rank[a.Status] < rank[b.Status]
		}
		if a.Kind != b.Kind {
			return a.Kind > b.Kind // VM before CT
		}
		return fmt.Sprintf("%08s", a.ID) < fmt.Sprintf("%08s", b.ID)
	})

	// USB: every disk of every set, also those without a copy this month.
	hidx := c.healthIndex()
	if rots, err := c.Rotations(); err == nil {
		for _, r := range rots {
			for _, d := range r.Disks {
				u := USBMonth{Set: r.Name, Label: d.Label}
				if x := usb[d.Label]; x != nil {
					u.Copies, u.Failed, u.LastOK = x.Copies, x.Failed, x.LastOK
					delete(usb, d.Label)
				}
				if h := hidx.rotation[r.ID+"/"+d.Key]; h != nil {
					u.Health = h.Status
					if len(h.Problems) > 0 {
						u.HealthDetail = h.Problems[0]
					}
				}
				switch {
				case u.Health == "error" || u.Failed > 0 && u.Copies == 0:
					u.Status = "error"
				case u.Copies == 0 || u.Failed > 0 || u.Health == "warning":
					u.Status = "warning"
				default:
					u.Status = "ok"
				}
				rep.USB = append(rep.USB, u)
			}
		}
	}
	for _, x := range usb { // disks no longer in a set
		x.Set, x.Status = "–", "ok"
		rep.USB = append(rep.USB, *x)
	}

	for _, run := range c.RestoreTestRuns() {
		if run.Start < rep.From || run.Start >= rep.To || run.Status == "running" {
			continue
		}
		ok := 0
		for _, x := range run.Results {
			if x.Status == "ok" {
				ok++
			}
		}
		rep.RestoreTests = append(rep.RestoreTests, RestoreMonth{Plan: run.PlanName, Start: run.Start, Status: run.Status, OK: ok, All: len(run.Results)})
	}
	sort.Slice(rep.RestoreTests, func(i, j int) bool { return rep.RestoreTests[i].Start < rep.RestoreTests[j].Start })

	for _, h := range c.DiskHealths() {
		if !h.Virtual {
			rep.Disks = append(rep.Disks, h)
		}
	}
	rep.Servers = c.ServerHealths()
	if list, err := c.Archives(); err == nil {
		for _, a := range list {
			seen := c.ArchiveLastSeen(a)
			rep.Archives = append(rep.Archives, ArchiveMonth{Name: a.Name, Location: a.Location, LastSeen: seen, Servers: len(a.Contents),
				Stale: seen > 0 && time.Since(time.Unix(seen, 0)) > ArchiveCheckAfter})
		}
	}
	for _, p := range problems {
		rep.Problems = append(rep.Problems, *p)
	}
	sort.Slice(rep.Problems, func(i, j int) bool {
		if rep.Problems[i].Severity != rep.Problems[j].Severity {
			return rep.Problems[i].Severity == "critical"
		}
		return rep.Problems[i].Last > rep.Problems[j].Last
	})
	if len(rep.Problems) > 40 {
		rep.Problems = rep.Problems[:40]
	}
	sb := c.SelfBackup()
	rep.ConfigBackup, rep.ConfigBackupOn = sb.LastOK, sb.Enabled

	rep.Status, rep.Headline = rep.verdict()
	return rep, nil
}

// verdict rates the month in one sentence.
func (r MonthlyReport) verdict() (string, string) {
	var bad, warn []string
	guestsBad := 0
	for _, g := range r.Guests {
		if g.Status == "error" {
			guestsBad++
		}
	}
	switch {
	case r.BackupRuns == 0:
		bad = append(bad, "keine Sicherung aufgezeichnet")
	case guestsBad > 0:
		bad = append(bad, fmt.Sprintf("%d Server zuletzt nicht gesichert", guestsBad))
	case r.BackupRunsFailed > 0:
		bad = append(bad, fmt.Sprintf("%d von %d Sicherungsläufen ganz fehlgeschlagen", r.BackupRunsFailed, r.BackupRuns))
	case r.BackupRunsOK < r.BackupRuns:
		warn = append(warn, fmt.Sprintf("%d von %d Sicherungsläufen mit Fehlern", r.BackupRuns-r.BackupRunsOK, r.BackupRuns))
	}
	for _, t := range r.RestoreTests {
		if t.Status == "error" {
			bad = append(bad, "ein Restore-Test ist fehlgeschlagen")
			break
		}
	}
	for _, u := range r.USB {
		if u.Status == "error" {
			bad = append(bad, "USB-Platte „"+u.Label+"“ fehlerhaft")
		} else if u.Status == "warning" && u.Copies == 0 {
			warn = append(warn, "USB-Platte „"+u.Label+"“ im ganzen Monat nicht benutzt")
		}
	}
	for _, d := range r.Disks {
		if d.Status == "error" {
			bad = append(bad, "Festplatte "+d.Label+" bald tauschen")
		} else if d.Status == "warning" {
			warn = append(warn, "Festplatte "+d.Label+" beobachten")
		}
	}
	for _, s := range r.Servers {
		if s.RestartNeeded || s.RebootNeeded {
			warn = append(warn, s.Name+": Neustart ausstehend")
		}
	}
	switch {
	case len(bad) > 0:
		return "error", "Handlungsbedarf: " + strings.Join(dedupe(bad), ", ") + "."
	case len(warn) > 0:
		return "warning", "Im Großen und Ganzen in Ordnung, aber: " + strings.Join(dedupe(warn), ", ") + "."
	case len(r.RestoreTests) == 0:
		return "ok", "Alle Sicherungen erfolgreich. In diesem Monat lief kein Restore-Test."
	}
	return "ok", "Alle Sicherungen erfolgreich, Restore-Tests bestanden, Platten in Ordnung."
}

// MonthlySettings: whether and to whom the report goes.
type MonthlySettings struct {
	Enabled  bool   `json:"enabled"`
	To       string `json:"to"` // mail recipients ("" = those of the notifications)
	LastSent string `json:"last_sent"`
}

// Monthly returns the settings of the monthly report.
func (c *Core) Monthly() MonthlySettings {
	s := MonthlySettings{}
	_ = c.St.GetSetting("monthly", &s)
	return s
}

// SetMonthly stores the settings of the monthly report.
func (c *Core) SetMonthly(enabled bool, to string) error {
	s := c.Monthly()
	s.Enabled, s.To = enabled, strings.TrimSpace(to)
	return c.St.SetSetting("monthly", s)
}

func (c *Core) scheduleMonthly(now time.Time) {
	s := c.Monthly()
	if !s.Enabled || now.Day() != 1 || now.Hour() < 7 {
		return
	}
	month := PreviousMonth(now)
	if s.LastSent == month {
		return
	}
	s.LastSent = month
	_ = c.St.SetSetting("monthly", s)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := c.SendMonthlyReport(ctx, month); err != nil {
			c.Event("warning", "Appliance", "Monatsbericht nicht versandt", err.Error())
		}
	}()
}

// SendMonthlyReport sends the report of a month (summary in the message, the full report
// attached) by mail, Telegram and push, whatever severity is set for other messages.
func (c *Core) SendMonthlyReport(ctx context.Context, month string) error {
	rep, err := c.BuildMonthlyReport(ctx, month)
	if err != nil {
		return err
	}
	if c.MonthlyHTML == nil {
		return errors.New("Bericht kann nicht erzeugt werden")
	}
	ns := c.NotifySettings()
	if extra := c.Monthly().To; extra != "" {
		ns.MailTo = extra
	}
	if !ns.MailEnabled && !ns.HookEnabled && !ns.TelegramEnabled {
		return errors.New("Keine Benachrichtigung eingerichtet (E-Mail, Telegram oder Push).")
	}
	ns.MinSeverity = "info"
	d := &notify.Details{Status: rep.Status, Link: "/", RowsTitle: "Server im " + rep.Title}
	d.Facts = []notify.Fact{{Label: "Zeitraum", Value: rep.Title}, {Label: "Sicherungsläufe", Value: fmt.Sprintf("%d, davon %d ohne Fehler", rep.BackupRuns, rep.BackupRunsOK)}}
	copies := 0
	for _, u := range rep.USB {
		copies += u.Copies
	}
	if len(rep.USB) > 0 {
		d.Facts = append(d.Facts, notify.Fact{Label: "USB-Kopien", Value: fmt.Sprintf("%d auf %d Platten", copies, len(rep.USB))})
	}
	rtOK := 0
	for _, t := range rep.RestoreTests {
		if t.Status == "ok" {
			rtOK++
		}
	}
	d.Facts = append(d.Facts, notify.Fact{Label: "Restore-Tests", Value: fmt.Sprintf("%d, davon %d bestanden", len(rep.RestoreTests), rtOK)})
	for _, g := range rep.Guests {
		info := fmt.Sprintf("%d von %d Sicherungen erfolgreich", g.OK, g.OK+g.Failed)
		if g.LastOK > 0 {
			info += ", zuletzt " + time.Unix(g.LastOK, 0).Format("02.01.")
		}
		if g.LastError != "" {
			info += " – " + g.LastError
		}
		d.Rows = append(d.Rows, notify.Row{Kind: g.Kind, ID: g.ID, Name: g.Name, Status: g.Status, Info: info})
	}
	d.Note = "Der vollständige Bericht mit USB-Platten, Restore-Tests, Festplatten und Hinweisen liegt als Datei bei."
	sev := map[string]string{"ok": "info", "warning": "warning", "error": "critical"}[rep.Status]
	msg := c.Message(sev, "Monatsbericht", "Monatsbericht "+rep.Title, rep.Headline, d)
	msg.Attachments = []notify.Attachment{{Name: "Monatsbericht-" + month + ".html", ContentType: "text/html; charset=utf-8", Data: c.MonthlyHTML(rep)}}
	if err := notify.Send(ctx, ns, msg); err != nil {
		return err
	}
	if b, err := json.Marshal(d); err == nil {
		_, _ = c.St.AddEvent(store.Event{Severity: "info", Source: "Monatsbericht", Title: "Monatsbericht " + rep.Title + " versandt", Body: rep.Headline, Details: b})
	}
	return nil
}
