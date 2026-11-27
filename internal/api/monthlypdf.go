package api

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/pdf"
)

// The monthly report as PDF: for the customer to file away. The IT service's logo and name on
// top of every page, a short part in plain words on the first page, the details below.

var (
	cInk    = pdf.Hex("#111827")
	cMuted  = pdf.Hex("#6b7280")
	cLine   = pdf.Hex("#e5e7eb")
	cSoft   = pdf.Hex("#f9fafb")
	cBrand  = pdf.Hex("#1f3d6e")
	cRed    = pdf.Hex("#b91c1c")
	pdfTone = map[string]struct{ accent, wash, ink pdf.Color }{
		"ok":      {pdf.Hex("#16a34a"), pdf.Hex("#dcfce7"), pdf.Hex("#14532d")},
		"warning": {pdf.Hex("#d97706"), pdf.Hex("#fef3c7"), pdf.Hex("#78350f")},
		"error":   {pdf.Hex("#dc2626"), pdf.Hex("#fee2e2"), pdf.Hex("#7f1d1d")},
		"unknown": {pdf.Hex("#6b7280"), pdf.Hex("#f3f4f6"), pdf.Hex("#374151")},
	}
)

func tone(st string) struct{ accent, wash, ink pdf.Color } {
	if t, ok := pdfTone[st]; ok {
		return t
	}
	return pdfTone["unknown"]
}

const (
	mLeft   = 42.0
	mRight  = 42.0
	mTop    = 92.0 // below the page header
	mBottom = 60.0 // above the footer
)

// layout writes top to bottom and turns the page when needed.
type layout struct {
	d      *pdf.Doc
	y      float64
	w      float64 // content width
	header func()
}

func (l *layout) newPage() {
	l.d.AddPage()
	l.header()
	l.y = mTop
}

// need turns the page unless h points still fit.
func (l *layout) need(h float64) {
	if l.y+h > pdf.PageH-mBottom {
		l.newPage()
	}
}

// text writes wrapped lines and moves down.
func (l *layout) text(s string, f pdf.Font, size float64, c pdf.Color, x, width float64) {
	lh := size * 1.35
	for _, line := range pdf.Wrap(s, f, size, width) {
		l.need(lh)
		l.d.Text(x, l.y+size, line, f, size, c)
		l.y += lh
	}
}

func (l *layout) heading(s string) {
	l.need(60)
	l.y += 14
	l.d.Text(mLeft, l.y+12, s, pdf.Bold, 12.5, cInk)
	l.y += 22
}

type pcol struct {
	title string
	w     float64 // share of the width
	right bool
}

type pcell struct {
	text   string
	sub    string // second line, small and grey
	bold   bool
	status string // drawn as a pill
	red    bool
}

// table draws a table; the header row comes again on each new page.
func (l *layout) table(cols []pcol, rows [][]pcell) {
	const pad, size, lh = 5.0, 8.5, 11.0
	xs := make([]float64, len(cols)+1)
	xs[0] = mLeft
	for i, c := range cols {
		xs[i+1] = xs[i] + c.w*l.w
	}
	head := func() {
		l.d.Rect(mLeft, l.y, l.w, 18, cSoft)
		for i, c := range cols {
			t := strings.ToUpper(c.title)
			x := xs[i] + pad
			if c.right {
				x = xs[i+1] - pad - pdf.Width(t, pdf.Bold, 7)
			}
			l.d.Text(x, l.y+12, t, pdf.Bold, 7, cMuted)
		}
		l.d.Line(mLeft, l.y+18, mLeft+l.w, l.y+18, 0.6, cLine)
		l.y += 18
	}
	l.need(40)
	head()
	for _, row := range rows {
		lines := make([][]string, len(cols))
		h := 0.0
		for i, c := range row {
			cw := xs[i+1] - xs[i] - 2*pad
			n := 1.0
			if c.status == "" {
				f := pdf.Regular
				if c.bold {
					f = pdf.Bold
				}
				lines[i] = pdf.Wrap(c.text, f, size, cw)
				n = float64(len(lines[i]))
			}
			if c.sub != "" {
				n += float64(len(pdf.Wrap(c.sub, pdf.Regular, 7.5, cw)))
			}
			h = max(h, n*lh+2*pad)
		}
		if l.y+h > pdf.PageH-mBottom {
			l.newPage()
			head()
		}
		for i, c := range row {
			x, y := xs[i]+pad, l.y+pad
			cw := xs[i+1] - xs[i] - 2*pad
			if c.status != "" {
				word := strings.ToUpper(statusWord(c.status))
				t := tone(c.status)
				l.d.Rect(x, y, pdf.Width(word, pdf.Bold, 6.5)+8, 11, t.wash)
				l.d.Text(x+4, y+8, word, pdf.Bold, 6.5, t.ink)
				y += lh
			} else {
				f, col := pdf.Regular, cInk
				if c.bold {
					f = pdf.Bold
				}
				if c.red {
					col = cRed
				}
				for _, line := range lines[i] {
					lx := x
					if cols[i].right {
						lx = xs[i+1] - pad - pdf.Width(line, f, size)
					}
					l.d.Text(lx, y+8.5, line, f, size, col)
					y += lh
				}
			}
			if c.sub != "" {
				for _, line := range pdf.Wrap(c.sub, pdf.Regular, 7.5, cw) {
					l.d.Text(x, y+8, line, pdf.Regular, 7.5, cMuted)
					y += lh
				}
			}
		}
		l.y += h
		l.d.Line(mLeft, l.y, mLeft+l.w, l.y, 0.4, cLine)
	}
	l.y += 4
}

// renderMonthlyPDF writes the monthly report as PDF.
func (s *Server) renderMonthlyPDF(rep core.MonthlyReport) []byte {
	set := s.C.Monthly()
	d := pdf.New()
	d.Title = "Monatsbericht " + rep.Title
	logo, logoW, logoH := -1, 0, 0
	if lg, ok := s.C.Logo(); ok {
		if id, w, h, err := d.AddImage(lg.Data); err == nil {
			logo, logoW, logoH = id, w, h
		}
	}
	from := cmp.Or(set.Company, "PK95 ProxBackup")
	l := &layout{d: d, w: pdf.PageW - mLeft - mRight}
	l.header = func() {
		x := mLeft
		if logo >= 0 {
			h := 28.0
			w := h * float64(logoW) / float64(logoH)
			if w > 140 {
				w, h = 140, 140*float64(logoH)/float64(logoW)
			}
			d.Image(logo, x, 32+(28-h)/2, w, h)
			x += w + 10
		}
		d.Text(x, 51, from, pdf.Bold, 14, cBrand)
		right := "Monatsbericht " + rep.Title
		d.Text(pdf.PageW-mRight-pdf.Width(right, pdf.Bold, 9), 44, right, pdf.Bold, 9, cInk)
		sub := cmp.Or(set.Customer, rep.Host)
		d.Text(pdf.PageW-mRight-pdf.Width(sub, pdf.Regular, 8), 56, sub, pdf.Regular, 8, cMuted)
		d.Line(mLeft, 72, pdf.PageW-mRight, 72, 0.6, cLine)
	}
	l.newPage()

	// Status band
	t := tone(rep.Status)
	headLines := pdf.Wrap(rep.Headline, pdf.Regular, 10, l.w-30)
	bandH := 56 + float64(len(headLines))*13
	if set.Customer != "" {
		bandH += 16
	}
	d.Rect(mLeft, l.y, l.w, bandH, t.wash)
	d.Rect(mLeft, l.y, 5, bandH, t.accent)
	y := l.y + 20
	d.Text(mLeft+18, y, strings.ToUpper(statusWord(rep.Status)+" · Datensicherung"), pdf.Bold, 7.5, t.accent)
	y += 22
	d.Text(mLeft+18, y, "Monatsbericht "+rep.Title, pdf.Bold, 19, t.ink)
	if set.Customer != "" {
		y += 16
		d.Text(mLeft+18, y, "für "+set.Customer, pdf.Regular, 10, t.ink)
	}
	y += 3
	for _, line := range headLines {
		y += 13
		d.Text(mLeft+18, y, line, pdf.Regular, 10, t.ink)
	}
	l.y += bandH + 8

	// In plain words
	plain := rep.Plain(set.Company)
	l.heading("Kurz gesagt")
	l.text(plain.Lead, pdf.Bold, 11, cInk, mLeft, l.w)
	l.y += 4
	for _, p := range plain.Points {
		l.need(15)
		d.Text(mLeft+2, l.y+10, "•", pdf.Bold, 10, cBrand)
		l.text(p, pdf.Regular, 10, cInk, mLeft+14, l.w-14)
		l.y += 2
	}
	l.y += 6
	if len(plain.Todo) > 0 {
		h := 30.0
		for _, x := range plain.Todo {
			h += float64(len(pdf.Wrap(x, pdf.Regular, 10, l.w-44)))*13.5 + 3
		}
		l.need(h)
		tw := tone("warning")
		d.Rect(mLeft, l.y, l.w, h, tw.wash)
		d.Rect(mLeft, l.y, 4, h, tw.accent)
		d.Text(mLeft+16, l.y+17, "Was ist zu tun?", pdf.Bold, 10.5, tw.ink)
		l.y += 26
		for _, x := range plain.Todo {
			d.Text(mLeft+16, l.y+10, "•", pdf.Bold, 10, tw.ink)
			l.text(x, pdf.Regular, 10, tw.ink, mLeft+28, l.w-44)
			l.y += 3
		}
		l.y += 8
	} else {
		l.text("Es besteht kein Handlungsbedarf.", pdf.Bold, 10, tone("ok").ink, mLeft, l.w)
	}

	// Figures
	copies, passed := 0, 0
	for _, u := range rep.USB {
		copies += u.Copies
	}
	for _, x := range rep.RestoreTests {
		if x.Status == "ok" {
			passed++
		}
	}
	cfg := "wird nicht gesichert"
	if rep.ConfigBackupOn {
		cfg = "gesichert, zuletzt " + reportDay(rep.ConfigBackup)
	}
	usb := fmt.Sprint(copies)
	if len(rep.USB) > 0 {
		usb += fmt.Sprintf(" auf %d Platten", len(rep.USB))
	}
	facts := [][2]string{
		{"Zeitraum", reportDay(rep.From) + " – " + reportDay(rep.To-1)},
		{"Sicherungsläufe", fmt.Sprintf("%d, davon %d ohne Fehler", rep.BackupRuns, rep.BackupRunsOK)},
		{"Gesicherte Server", fmt.Sprint(len(rep.Guests))},
		{"USB-Kopien", usb},
		{"Restore-Tests", fmt.Sprintf("%d, davon %d bestanden", len(rep.RestoreTests), passed)},
		{"Konfiguration der Appliance", cfg},
	}
	l.heading("In Zahlen")
	colW := l.w / 2
	for i := 0; i < len(facts); i += 2 {
		l.need(30)
		for k := 0; k < 2 && i+k < len(facts); k++ {
			x := mLeft + float64(k)*colW
			d.Text(x, l.y+9, facts[i+k][0], pdf.Regular, 8, cMuted)
			d.Text(x, l.y+22, facts[i+k][1], pdf.Bold, 10, cInk)
		}
		l.y += 30
		d.Line(mLeft, l.y-3, mLeft+l.w, l.y-3, 0.4, cLine)
	}

	// Details
	l.heading("Server")
	if len(rep.Guests) == 0 {
		l.text("In diesem Monat wurde keine Sicherung aufgezeichnet.", pdf.Regular, 9.5, cMuted, mLeft, l.w)
	} else {
		var rows [][]pcell
		for _, g := range rep.Guests {
			rows = append(rows, []pcell{{text: g.ID + " " + g.Name, bold: true, sub: g.Kind}, {status: g.Status},
				{text: fmt.Sprint(g.OK)}, {text: fmt.Sprint(g.Failed), red: g.Failed > 0}, {text: reportDayTime(g.LastOK)}, {text: g.LastError, red: g.LastError != ""}})
		}
		l.table([]pcol{{"Server", .25, false}, {"Status", .17, false}, {"Gesichert", .11, true}, {"Fehler", .09, true}, {"Zuletzt gesichert", .17, false}, {"Hinweis", .21, false}}, rows)
	}
	if len(rep.USB) > 0 {
		l.heading("USB-Wechselplatten")
		var rows [][]pcell
		for _, u := range rep.USB {
			health := pcell{text: "nicht geprüft"}
			if u.Health != "" {
				health = pcell{status: u.Health, sub: u.HealthDetail}
			}
			rows = append(rows, []pcell{{text: u.Label, bold: true, sub: u.Set}, {status: u.Status}, {text: fmt.Sprint(u.Copies)}, {text: fmt.Sprint(u.Failed), red: u.Failed > 0}, {text: reportDayTime(u.LastOK)}, health})
		}
		l.table([]pcol{{"Platte", .24, false}, {"Status", .17, false}, {"Kopien", .09, true}, {"Fehler", .09, true}, {"Zuletzt", .17, false}, {"Zustand der Platte", .24, false}}, rows)
	}
	l.heading("Restore-Tests")
	if len(rep.RestoreTests) == 0 {
		l.text("In diesem Monat lief kein Restore-Test. Ein regelmäßiger Test zeigt, dass sich die Sicherungen auch wirklich starten lassen.", pdf.Regular, 9.5, cMuted, mLeft, l.w)
	} else {
		var rows [][]pcell
		for _, x := range rep.RestoreTests {
			rows = append(rows, []pcell{{text: reportDayTime(x.Start)}, {text: x.Plan, bold: true}, {status: x.Status}, {text: fmt.Sprintf("%d von %d", x.OK, x.All)}})
		}
		l.table([]pcol{{"Datum", .2, false}, {"Plan", .38, false}, {"Ergebnis", .2, false}, {"Server gestartet", .22, true}}, rows)
	}
	if len(rep.Disks) > 0 {
		l.heading("Festplatten (Stand " + reportDay(rep.Built) + ")")
		var rows [][]pcell
		for _, x := range rep.Disks {
			find := cmp.Or(x.Note, x.Info, "keine Fehler")
			if len(x.Problems) > 0 {
				find = strings.Join(x.Problems, " · ")
			}
			rows = append(rows, []pcell{{text: x.Label, bold: true, sub: x.Serial}, {text: x.Host}, {text: x.Use}, {status: x.Status}, {text: find, red: len(x.Problems) > 0}})
		}
		l.table([]pcol{{"Platte", .24, false}, {"Server", .16, false}, {"Verwendung", .2, false}, {"Zustand", .16, false}, {"Befund", .24, false}}, rows)
	}
	if len(rep.Servers) > 0 {
		l.heading("Backup-Server")
		var rows [][]pcell
		for _, x := range rep.Servers {
			restart := "nicht nötig"
			if x.RestartNeeded {
				restart = "Dienste neu laden"
			} else if x.RebootNeeded {
				restart = "Neustart nötig"
			}
			avail := ""
			if x.Available != "" {
				avail = "verfügbar: " + x.Available
			}
			rows = append(rows, []pcell{{text: x.Name, bold: true}, {text: x.Running, sub: avail}, {text: restart}, {text: fmt.Sprint(x.Updates)}})
		}
		l.table([]pcol{{"Server", .32, false}, {"Version", .26, false}, {"Neustart", .24, false}, {"Updates", .18, true}}, rows)
	}
	if len(rep.Archives) > 0 {
		l.heading("Archiv-Platten")
		var rows [][]pcell
		for _, a := range rep.Archives {
			last := reportDay(a.LastSeen)
			if a.Stale {
				last += " – bitte einmal anstecken"
			}
			rows = append(rows, []pcell{{text: a.Name, bold: true}, {text: a.Location}, {text: fmt.Sprint(a.Servers)}, {text: last, red: a.Stale}})
		}
		l.table([]pcol{{"Platte", .3, false}, {"Aufbewahrt", .3, false}, {"Server", .12, true}, {"Zuletzt angesteckt", .28, false}}, rows)
	}
	l.heading("Hinweise und Fehler im Monat")
	if len(rep.Problems) == 0 {
		l.text("Keine Warnungen und Fehler.", pdf.Regular, 9.5, cMuted, mLeft, l.w)
	} else {
		var rows [][]pcell
		for _, p := range rep.Problems {
			rows = append(rows, []pcell{{status: sevStatus(p.Severity)}, {text: p.Title}, {text: p.Source}, {text: fmt.Sprint(p.Count)}, {text: reportDayTime(p.Last)}})
		}
		l.table([]pcol{{"Art", .17, false}, {"Meldung", .4, false}, {"Quelle", .17, false}, {"Anzahl", .09, true}, {"Zuletzt", .17, false}}, rows)
	}
	if set.Company != "" && set.Contact != "" {
		l.heading("Fragen?")
		l.text("Wenden Sie sich bitte an "+set.Company+":", pdf.Regular, 9.5, cInk, mLeft, l.w)
		l.text(set.Contact, pdf.Regular, 9.5, cInk, mLeft, l.w)
	}

	// Footer with page numbers
	n := d.Pages()
	for i := 0; i < n; i++ {
		d.SetPage(i)
		d.Line(mLeft, pdf.PageH-42, pdf.PageW-mRight, pdf.PageH-42, 0.4, cLine)
		d.Text(mLeft, pdf.PageH-28, "Erstellt am "+reportDayTime(rep.Built)+" · "+rep.Host, pdf.Regular, 7.5, cMuted)
		right := fmt.Sprintf("Seite %d von %d", i+1, n)
		d.Text(pdf.PageW-mRight-pdf.Width(right, pdf.Regular, 7.5), pdf.PageH-28, right, pdf.Regular, 7.5, cMuted)
	}
	return d.Bytes()
}

func (s *Server) monthlyPDF(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	if month == "" {
		month = core.PreviousMonth(time.Now())
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	rep, err := s.C.BuildMonthlyReport(ctx, month)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Cache-Control", "no-store")
	disp := "inline"
	if r.URL.Query().Get("download") == "1" {
		disp = "attachment"
	}
	w.Header().Set("Content-Disposition", disp+`; filename="Monatsbericht-`+rep.Month+`.pdf"`)
	_, _ = w.Write(s.renderMonthlyPDF(rep))
}

// ---- logo of the IT service ----

func (s *Server) getLogo(w http.ResponseWriter, r *http.Request) {
	l, ok := s.C.Logo()
	if !ok {
		writeErr(w, http.StatusNotFound, "Kein Logo.")
		return
	}
	w.Header().Set("Content-Type", l.Type)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(l.Data)
}

func (s *Server) putLogo(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 512<<10+1))
	if err != nil || len(data) > 512<<10 {
		writeErr(w, http.StatusRequestEntityTooLarge, "Das Logo darf höchstens 512 KB groß sein.")
		return
	}
	typ := http.DetectContentType(data)
	if typ != "image/png" && typ != "image/jpeg" {
		writeErr(w, http.StatusUnsupportedMediaType, "Bitte ein PNG- oder JPEG-Bild.")
		return
	}
	if _, _, _, err := pdf.New().AddImage(data); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := s.C.SetLogo(&core.ReportLogo{Type: typ, Data: data}); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) deleteLogo(w http.ResponseWriter, r *http.Request) {
	if err := s.C.SetLogo(nil); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}
