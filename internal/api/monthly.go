package api

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"pk95proxbackup/internal/core"
)

// Monthly report: view (any month), send now, settings.

var monthlyFuncs = template.FuncMap{
	"day": func(ts int64) string {
		if ts == 0 {
			return "–"
		}
		return time.Unix(ts, 0).Format("02.01.2006")
	},
	"dayTime": func(ts int64) string {
		if ts == 0 {
			return "–"
		}
		return time.Unix(ts, 0).Format("02.01.2006 15:04")
	},
	"word": func(st string) string {
		return map[string]string{"ok": "In Ordnung", "warning": "Hinweis", "error": "Handlungsbedarf", "unknown": "Keine Werte", "cancelled": "Abgebrochen"}[st]
	},
	"sev": func(s string) string {
		if s == "critical" {
			return "error"
		}
		return "warning"
	},
	"years": func(h int64) string {
		switch {
		case h < 0:
			return "–"
		case h < 8766:
			return fmt.Sprintf("%d Monate", h/730)
		}
		return strings.Replace(fmt.Sprintf("%.1f Jahre", float64(h)/8766), ".", ",", 1)
	},
}

var monthlyTmpl = template.Must(template.New("m").Funcs(monthlyFuncs).Parse(`<!doctype html>
<html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Monatsbericht {{.R.Title}} · {{.R.Host}}</title>
<style>
:root { --ink:#111827; --muted:#6b7280; --line:#e5e7eb; --soft:#f9fafb; --brand:#1f3d6e; --c600:#1f73d6; }
* { box-sizing:border-box; }
body { margin:0; background:#f3f4f6; color:var(--ink); font:14px/1.5 "Space Grotesk",-apple-system,"Segoe UI",Roboto,Helvetica,Arial,sans-serif; }
.page { max-width:1100px; margin:24px auto; background:#fff; border:1px solid var(--line); border-radius:4px; }
header { display:flex; justify-content:space-between; align-items:center; gap:12px; padding:18px 28px; border-bottom:1px solid var(--line); }
.brand { display:flex; align-items:center; gap:10px; font-weight:700; font-size:19px; color:var(--brand); }
.logo { width:28px; height:28px; border-radius:3px; background:#0d1e35; color:#bce0ff; display:grid; place-items:center; font-weight:700; }
.meta { font-size:12px; color:var(--muted); text-align:right; font-family:Menlo,Consolas,monospace; }
.band { padding:22px 28px; border-left:6px solid; }
.band.ok { background:#dcfce7; border-color:#16a34a; color:#14532d; } .band.warning { background:#fef3c7; border-color:#d97706; color:#78350f; }
.band.error { background:#fee2e2; border-color:#dc2626; color:#7f1d1d; }
.band .k { font-size:11px; font-weight:700; letter-spacing:.08em; text-transform:uppercase; opacity:.85; }
.band h1 { margin:4px 0 0; font-size:24px; line-height:1.25; } .band p { margin:6px 0 0; }
section { padding:20px 28px 0; }
h2 { font-size:16px; margin:0 0 10px; }
.facts { display:grid; grid-template-columns:repeat(auto-fit,minmax(200px,1fr)); gap:10px 24px; }
.fact { border-bottom:1px solid #f3f4f6; padding:4px 0; } .fact span { display:block; color:var(--muted); font-size:12px; } .fact b { font-weight:600; }
table { width:100%; border-collapse:collapse; font-size:13px; }
th { text-align:left; font-size:11px; text-transform:uppercase; letter-spacing:.04em; color:var(--muted); font-weight:600; padding:8px 10px; border-bottom:1px solid var(--line); background:var(--soft); white-space:nowrap; }
td { padding:8px 10px; border-bottom:1px solid #f3f4f6; vertical-align:top; }
.pill { display:inline-block; padding:2px 8px; border-radius:2px; font-size:11px; font-weight:700; text-transform:uppercase; white-space:nowrap; }
.c-ok { background:#dcfce7; color:#166534; } .c-warning { background:#fef3c7; color:#78350f; } .c-error { background:#fee2e2; color:#991b1b; } .c-unknown, .c-cancelled { background:#f3f4f6; color:#374151; }
.tag { font-family:Menlo,Consolas,monospace; font-size:11px; background:#f3f4f6; color:#374151; padding:0 5px; border-radius:2px; }
.sub { color:var(--muted); font-size:12px; } .nowrap { white-space:nowrap; } .num { font-variant-numeric:tabular-nums; } .err { color:#b91c1c; }
.empty { color:var(--muted); font-size:13px; margin:0; }
footer { margin-top:24px; padding:16px 28px; border-top:1px solid var(--line); color:var(--muted); font-size:12px; display:flex; justify-content:space-between; gap:12px; flex-wrap:wrap; }
.tools { max-width:1100px; margin:16px auto 0; display:flex; gap:8px; justify-content:flex-end; }
.tools a, .tools button { font:700 13px inherit; font-family:inherit; padding:7px 14px; border-radius:3px; border:1px solid #d1d5db; background:#fff; color:#374151; text-decoration:none; cursor:pointer; }
.tools .primary { background:var(--c600); color:#fff; border-color:var(--c600); }
@media print { body { background:#fff; } .page { margin:0; border:0; max-width:none; } .tools { display:none; } tr { break-inside:avoid; } }
</style></head><body>
{{if .Tools}}<div class="tools"><button type="button" onclick="window.print()">Drucken / als PDF</button><a class="primary" href="?month={{.R.Month}}&amp;download=1">HTML herunterladen</a></div>{{end}}
<div class="page">
<header><div class="brand"><span class="logo">↓</span>PK95 ProxBackup</div><div class="meta">Monatsbericht<br>{{.R.Host}}</div></header>
<div class="band {{.R.Status}}"><div class="k">{{word .R.Status}} · Datensicherung</div><h1>Monatsbericht {{.R.Title}}</h1><p>{{.R.Headline}}</p></div>
<section><div class="facts">
  <div class="fact"><span>Zeitraum</span><b>{{day .R.From}} – {{day .Last}}</b></div>
  <div class="fact"><span>Sicherungsläufe</span><b>{{.R.BackupRuns}}, davon {{.R.BackupRunsOK}} ohne Fehler</b></div>
  <div class="fact"><span>Gesicherte Server</span><b>{{len .R.Guests}}</b></div>
  <div class="fact"><span>USB-Kopien</span><b>{{.Copies}}{{if .R.USB}} auf {{len .R.USB}} Platten{{end}}</b></div>
  <div class="fact"><span>Restore-Tests</span><b>{{len .R.RestoreTests}}, davon {{.TestsOK}} bestanden</b></div>
  <div class="fact"><span>Konfiguration der Appliance</span><b>{{if .R.ConfigBackupOn}}gesichert, zuletzt {{day .R.ConfigBackup}}{{else}}wird nicht gesichert{{end}}</b></div>
</div></section>

<section><h2>Server</h2>{{if .R.Guests}}<div style="overflow-x:auto"><table>
<thead><tr><th>Server</th><th>Status</th><th class="num">Erfolgreich</th><th class="num">Fehlgeschlagen</th><th>Zuletzt gesichert</th><th>Hinweis</th></tr></thead><tbody>
{{range .R.Guests}}<tr><td><span class="tag">{{.Kind}}</span> <b>{{.ID}}</b> {{.Name}}</td><td><span class="pill c-{{.Status}}">{{word .Status}}</span></td>
<td class="num">{{.OK}}</td><td class="num">{{if .Failed}}<span class="err">{{.Failed}}</span>{{else}}0{{end}}</td><td class="nowrap num">{{dayTime .LastOK}}</td><td class="{{if .LastError}}err{{end}}">{{.LastError}}</td></tr>{{end}}
</tbody></table></div>{{else}}<p class="empty">In diesem Monat wurde keine Sicherung aufgezeichnet.</p>{{end}}</section>

{{if .R.USB}}<section><h2>USB-Wechselplatten</h2><div style="overflow-x:auto"><table>
<thead><tr><th>Platte</th><th>Satz</th><th>Status</th><th class="num">Kopien</th><th class="num">Fehlgeschlagen</th><th>Zuletzt</th><th>Zustand der Platte</th></tr></thead><tbody>
{{range .R.USB}}<tr><td><b>{{.Label}}</b></td><td>{{.Set}}</td><td><span class="pill c-{{.Status}}">{{word .Status}}</span></td><td class="num">{{.Copies}}</td><td class="num">{{.Failed}}</td><td class="nowrap num">{{dayTime .LastOK}}</td>
<td>{{if .Health}}<span class="pill c-{{.Health}}">{{word .Health}}</span> <span class="sub">{{.HealthDetail}}</span>{{else}}<span class="sub">nicht geprüft</span>{{end}}</td></tr>{{end}}
</tbody></table></div></section>{{end}}

<section><h2>Restore-Tests</h2>{{if .R.RestoreTests}}<table>
<thead><tr><th>Datum</th><th>Plan</th><th>Ergebnis</th><th class="num">Server gestartet</th></tr></thead><tbody>
{{range .R.RestoreTests}}<tr><td class="nowrap num">{{dayTime .Start}}</td><td>{{.Plan}}</td><td><span class="pill c-{{.Status}}">{{word .Status}}</span></td><td class="num">{{.OK}} von {{.All}}</td></tr>{{end}}
</tbody></table>{{else}}<p class="empty">In diesem Monat lief kein Restore-Test. Ein regelmäßiger Test zeigt, dass sich die Sicherungen auch wirklich starten lassen.</p>{{end}}</section>

{{if .R.Disks}}<section><h2>Festplatten (Stand {{day .R.Built}})</h2><div style="overflow-x:auto"><table>
<thead><tr><th>Platte</th><th>Server</th><th>Verwendung</th><th>Zustand</th><th>Befund</th><th class="num">In Betrieb</th></tr></thead><tbody>
{{range .R.Disks}}<tr><td><b>{{.Label}}</b><div class="sub">{{.Serial}}</div></td><td>{{.Host}}</td><td>{{.Use}}</td><td><span class="pill c-{{.Status}}">{{word .Status}}</span></td>
<td>{{range .Problems}}<div class="err">{{.}}</div>{{else}}<span class="sub">{{if .Note}}{{.Note}}{{else}}keine Fehler{{end}}</span>{{end}}</td><td class="nowrap num">{{years .Smart.PowerOnHours}}</td></tr>{{end}}
</tbody></table></div></section>{{end}}

{{if .R.Servers}}<section><h2>Backup-Server</h2><table>
<thead><tr><th>Server</th><th>Version</th><th>Neustart</th><th class="num">Updates verfügbar</th></tr></thead><tbody>
{{range .R.Servers}}<tr><td><b>{{.Name}}</b></td><td>{{.Running}}{{if .Available}} <span class="sub">(verfügbar: {{.Available}})</span>{{end}}</td>
<td>{{if .RestartNeeded}}<span class="pill c-warning">Dienste neu laden</span>{{else if .RebootNeeded}}<span class="pill c-warning">Neustart nötig</span>{{else}}<span class="sub">nicht nötig</span>{{end}}</td><td class="num">{{.Updates}}</td></tr>{{end}}
</tbody></table></section>{{end}}

{{if .R.Archives}}<section><h2>Archiv-Platten</h2><table>
<thead><tr><th>Platte</th><th>Aufbewahrt</th><th class="num">Server</th><th>Zuletzt angesteckt</th></tr></thead><tbody>
{{range .R.Archives}}<tr><td><b>{{.Name}}</b></td><td>{{.Location}}</td><td class="num">{{.Servers}}</td><td class="nowrap num {{if .Stale}}err{{end}}">{{day .LastSeen}}{{if .Stale}} – bitte einmal anstecken und prüfen{{end}}</td></tr>{{end}}
</tbody></table></section>{{end}}

<section><h2>Hinweise und Fehler im Monat</h2>{{if .R.Problems}}<table>
<thead><tr><th>Art</th><th>Meldung</th><th>Quelle</th><th class="num">Anzahl</th><th>Zuletzt</th></tr></thead><tbody>
{{range .R.Problems}}<tr><td><span class="pill c-{{sev .Severity}}">{{if eq .Severity "critical"}}Fehler{{else}}Warnung{{end}}</span></td><td>{{.Title}}</td><td>{{.Source}}</td><td class="num">{{.Count}}</td><td class="nowrap num">{{dayTime .Last}}</td></tr>{{end}}
</tbody></table>{{else}}<p class="empty">Keine Warnungen und Fehler.</p>{{end}}</section>

<footer><span>Erstellt am {{dayTime .R.Built}} von PK95 ProxBackup {{.Version}}</span><span>{{.R.Host}}</span></footer>
</div></body></html>`))

func (s *Server) renderMonthly(rep core.MonthlyReport, tools bool) []byte {
	copies, passed := 0, 0
	for _, u := range rep.USB {
		copies += u.Copies
	}
	for _, t := range rep.RestoreTests {
		if t.Status == "ok" {
			passed++
		}
	}
	var b bytes.Buffer
	_ = monthlyTmpl.Execute(&b, map[string]any{"R": rep, "Tools": tools, "Copies": copies, "TestsOK": passed, "Last": rep.To - 1, "Version": s.C.Version})
	return b.Bytes()
}

func (s *Server) monthlyReport(w http.ResponseWriter, r *http.Request) {
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
	download := r.URL.Query().Get("download") == "1"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; script-src 'unsafe-inline'")
	if download {
		w.Header().Set("Content-Disposition", `attachment; filename="Monatsbericht-`+rep.Month+`.html"`)
	}
	_, _ = w.Write(s.renderMonthly(rep, !download))
}

func (s *Server) sendMonthly(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	if month == "" {
		month = core.PreviousMonth(time.Now())
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := s.C.SendMonthlyReport(ctx, month); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	ok(w)
}

func (s *Server) getMonthly(w http.ResponseWriter, r *http.Request) {
	m := s.C.Monthly()
	writeJSON(w, http.StatusOK, map[string]any{"enabled": m.Enabled, "to": m.To, "last_sent": m.LastSent, "previous": core.PreviousMonth(time.Now())})
}

func (s *Server) putMonthly(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool   `json:"enabled"`
		To      string `json:"to"`
	}
	if !decode(w, r, &req) {
		return
	}
	for _, a := range strings.FieldsFunc(req.To, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		if !strings.Contains(a, "@") {
			fieldErrs(w, map[string]string{"to": "Ungültige Adresse: " + a})
			return
		}
	}
	if err := s.C.SetMonthly(req.Enabled, req.To); err != nil {
		fail(w, err)
		return
	}
	s.getMonthly(w, r)
}
