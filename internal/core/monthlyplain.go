package core

import (
	"fmt"
	"strings"
	"time"
)

// The monthly report for the management of the customer: a few sentences without technical
// terms on top (are our data safe, is there anything to do), the details below.

// PlainSummary is the short part of the report.
type PlainSummary struct {
	Lead   string   // one sentence: how the month went
	Points []string // what happened, in plain words
	Todo   []string // what the customer (or the IT service) should do
}

// Plain writes the short part; who names the IT service ("" = "Ihr IT-Betreuer").
func (r MonthlyReport) Plain(who string) PlainSummary {
	if strings.TrimSpace(who) == "" {
		who = "Ihr IT-Betreuer"
	}
	month := strings.SplitN(r.Title, " ", 2)[0]
	var s PlainSummary
	switch r.Status {
	case "ok":
		s.Lead = "Ihre Daten wurden im " + month + " zuverlässig gesichert. Es besteht kein Handlungsbedarf."
	case "warning":
		s.Lead = "Ihre Daten wurden im " + month + " gesichert. Einzelne Punkte erfordern Ihre Aufmerksamkeit."
	default:
		s.Lead = "Im " + month + " sind Probleme bei der Datensicherung aufgetreten. Bitte beachten Sie die Punkte unter „Was ist zu tun?“."
	}
	day := func(ts int64) string { return time.Unix(ts, 0).Format("02.01.2006") }

	// Servers
	bad, last := 0, int64(0)
	for _, g := range r.Guests {
		if g.Failed > 0 {
			bad++
		}
		last = max(last, g.LastOK)
	}
	switch n := len(r.Guests); {
	case n == 0:
		s.Points = append(s.Points, "In diesem Monat wurde keine Sicherung aufgezeichnet.")
	case bad == 0 && n == 1:
		s.Points = append(s.Points, "Der Server wurde bei jeder Sicherung erfolgreich gesichert.")
	case bad == 0:
		s.Points = append(s.Points, fmt.Sprintf("Alle %d Server wurden bei jeder Sicherung erfolgreich gesichert.", n))
	default:
		s.Points = append(s.Points, fmt.Sprintf("%d von %d Servern wurden ohne Ausfall gesichert, bei %d kam es zu einzelnen Fehlern.", n-bad, n, bad))
	}
	if last > 0 {
		s.Points = append(s.Points, "Die letzte erfolgreiche Sicherung war am "+day(last)+".")
	}
	// USB disks
	copies, lastUSB := 0, int64(0)
	for _, u := range r.USB {
		copies += u.Copies
		lastUSB = max(lastUSB, u.LastOK)
	}
	switch {
	case len(r.USB) == 0:
	case copies > 0:
		s.Points = append(s.Points, fmt.Sprintf("Auf die USB-Festplatten wurde %d-mal gesichert, zuletzt am %s. Damit liegt eine aktuelle Kopie außer Haus vor.", copies, day(lastUSB)))
	default:
		s.Points = append(s.Points, "Die USB-Festplatten wurden in diesem Monat nicht verwendet; es liegt keine aktuelle Kopie außer Haus vor.")
	}
	// Restore tests
	ok := 0
	for _, t := range r.RestoreTests {
		if t.Status == "ok" {
			ok++
		}
	}
	switch n := len(r.RestoreTests); {
	case n == 0:
		s.Points = append(s.Points, "In diesem Monat wurde die Wiederherstellung nicht getestet.")
	case ok == n:
		s.Points = append(s.Points, fmt.Sprintf("Die Wiederherstellung wurde %d-mal getestet – jeweils erfolgreich.", n))
	default:
		s.Points = append(s.Points, fmt.Sprintf("Die Wiederherstellung wurde %d-mal getestet, davon %d-mal erfolgreich.", n, ok))
	}
	diskBad := false
	for _, d := range r.Disks {
		diskBad = diskBad || d.Status == "error" || d.Status == "warning"
	}
	if len(r.Disks) > 0 && !diskBad {
		s.Points = append(s.Points, "Die Festplatten der Datensicherung sind in Ordnung.")
	}

	// What to do
	for _, g := range r.Guests {
		if g.Status == "error" {
			s.Todo = append(s.Todo, "Server „"+strings.TrimSpace(g.ID+" "+g.Name)+"“ wurde zuletzt nicht gesichert – "+who+" prüft das.")
		}
	}
	for _, u := range r.USB {
		switch {
		case u.Status == "error":
			s.Todo = append(s.Todo, "Die USB-Festplatte „"+u.Label+"“ meldet Fehler – "+who+" prüft sie.")
		case u.Copies == 0:
			s.Todo = append(s.Todo, "Bitte schließen Sie die USB-Festplatte „"+u.Label+"“ wieder an und nehmen Sie sie in den Wechsel auf.")
		}
	}
	for _, d := range r.Disks {
		if d.Status == "error" {
			s.Todo = append(s.Todo, "Die Festplatte "+d.Label+" sollte bald getauscht werden – "+who+" meldet sich dazu.")
		}
	}
	for _, t := range r.RestoreTests {
		if t.Status == "error" {
			s.Todo = append(s.Todo, "Ein Wiederherstellungstest ist fehlgeschlagen – "+who+" prüft die Ursache.")
			break
		}
	}
	for _, a := range r.Archives {
		if a.Stale {
			s.Todo = append(s.Todo, "Bitte schließen Sie die Archiv-Festplatte „"+a.Name+"“ einmal an, damit sie geprüft werden kann.")
		}
	}
	return s
}
