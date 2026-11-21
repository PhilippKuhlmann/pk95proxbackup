package api

import (
	"bytes"
	"context"
	"html/template"
	"net/http"
	"strings"
	"time"

	"pk95proxbackup/internal/core"
)

// Proof of backup: printable document for a period and a choice of guests.

var proofFuncs = template.FuncMap{
	"day":     monthlyFuncs["day"],
	"dayTime": monthlyFuncs["dayTime"],
	"word":    monthlyFuncs["word"],
	"pct": func(a, b int) int {
		if b == 0 {
			return 0
		}
		return a * 100 / b
	},
	"kindName": func(k string) string {
		return map[string]string{"fixed": "Backup-Server", "cloud": "Cloud (außer Haus)", "usb": "USB-Wechselplatte", "archive": "Archiv-Platte"}[k]
	},
	"testWord": func(s string) string {
		return map[string]string{"ok": "bestanden", "warning": "mit Hinweis", "error": "fehlgeschlagen"}[s]
	},
}

var proofTmpl = template.Must(template.New("p").Funcs(proofFuncs).Parse(`<!doctype html>
<html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sicherungsnachweis {{day .R.From}} – {{day .Last}}{{if .R.For}} · {{.R.For}}{{end}}</title>
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
table { width:100%; border-collapse:collapse; font-size:12.5px; }
th { text-align:left; font-size:11px; text-transform:uppercase; letter-spacing:.04em; color:var(--muted); font-weight:600; padding:7px 8px; border-bottom:1px solid var(--line); background:var(--soft); white-space:nowrap; }
td { padding:7px 8px; border-bottom:1px solid #f3f4f6; vertical-align:top; }
.pill { display:inline-block; padding:2px 8px; border-radius:2px; font-size:11px; font-weight:700; text-transform:uppercase; white-space:nowrap; }
.c-ok { background:#dcfce7; color:#166534; } .c-warning { background:#fef3c7; color:#78350f; } .c-error { background:#fee2e2; color:#991b1b; }
.tag { font-family:Menlo,Consolas,monospace; font-size:11px; background:#f3f4f6; color:#374151; padding:0 5px; border-radius:2px; }
.sub { color:var(--muted); font-size:12px; } .nowrap { white-space:nowrap; } .num { font-variant-numeric:tabular-nums; } .err { color:#b91c1c; }
.note { background:#fef3c7; color:#78350f; border-left:4px solid #d97706; padding:8px 12px; }
.sign { display:grid; grid-template-columns:1fr 1fr; gap:40px; margin-top:36px; }
.sign div { border-top:1px solid var(--ink); padding-top:4px; font-size:12px; color:var(--muted); }
footer { margin-top:24px; padding:16px 28px; border-top:1px solid var(--line); color:var(--muted); font-size:12px; display:flex; justify-content:space-between; gap:12px; flex-wrap:wrap; }
.tools { max-width:1100px; margin:16px auto 0; display:flex; gap:8px; justify-content:flex-end; }
.tools a, .tools button { font:700 13px inherit; font-family:inherit; padding:7px 14px; border-radius:3px; border:1px solid #d1d5db; background:#fff; color:#374151; text-decoration:none; cursor:pointer; }
.tools .primary { background:var(--c600); color:#fff; border-color:var(--c600); }
@media print { body { background:#fff; font-size:12px; } .page { margin:0; border:0; max-width:none; } .tools { display:none; } tr { break-inside:avoid; } }
</style></head><body>
{{if .Tools}}<div class="tools"><button type="button" onclick="window.print()">Drucken / als PDF</button><a class="primary" href="{{.Download}}">HTML herunterladen</a></div>{{end}}
<div class="page">
<header><div class="brand"><span class="logo">↓</span>PK95 ProxBackup</div><div class="meta">Sicherungsnachweis<br>{{.R.Host}}</div></header>
<div class="band {{.R.Status}}"><div class="k">Nachweis der Datensicherung{{if .R.For}} · {{.R.For}}{{end}}</div><h1>{{day .R.From}} bis {{day .Last}}</h1><p>{{.R.Headline}}</p></div>
{{if .R.Partial}}<section><div class="note">Laufdaten liegen ab dem {{day .R.Partial}} vor (die Appliance bewahrt sie sechs Monate auf). Für die Zeit davor zählen nur die heute vorhandenen Sicherungsstände.</div></section>{{end}}
<section><div class="facts">
  <div class="fact"><span>Zeitraum</span><b>{{.R.Days}} Tage</b></div>
  <div class="fact"><span>Gesicherte Server</span><b>{{len .R.Guests}}</b></div>
  <div class="fact"><span>Sicherungsläufe</span><b>{{.R.Runs}}, davon {{.R.RunsOK}} ohne Fehler</b></div>
  <div class="fact"><span>Kopie außerhalb des Backup-Servers</span><b>{{if .R.Offsite}}ja{{else}}nein{{end}}</b></div>
  <div class="fact"><span>Restore-Tests im Zeitraum</span><b>{{.R.Tests}}</b></div>
</div></section>

<section><h2>Je Server</h2>{{if .R.Guests}}<div style="overflow-x:auto"><table>
<thead><tr><th>Server</th><th>Ergebnis</th><th class="num">Erfolgreich</th><th class="num">Fehlgeschlagen</th><th class="num">Tage mit Sicherung</th><th class="num">Längste Lücke</th><th>Zuletzt gesichert</th><th>Restore-Test</th></tr></thead><tbody>
{{range .R.Guests}}<tr><td><span class="tag">{{.Kind}}</span> <b>{{.ID}}</b> {{.Name}}{{if .Gone}}<div class="sub">nicht mehr in Proxmox VE</div>{{end}}</td>
<td><span class="pill c-{{.Status}}">{{word .Status}}</span></td>
<td class="num">{{.OK}}</td><td class="num">{{if .Failed}}<span class="err">{{.Failed}}</span>{{else}}0{{end}}</td>
<td class="num">{{.Days}} von {{$.R.Days}} ({{pct .Days $.R.Days}} %)</td><td class="num">{{if .MaxGap}}{{.MaxGap}} {{if eq .MaxGap 1}}Tag{{else}}Tage{{end}}{{else}}–{{end}}</td>
<td class="nowrap num">{{dayTime .LastOK}}</td>
<td>{{if .TestAt}}{{testWord .TestOK}} am {{day .TestAt}}{{if .TestNote}}<div class="sub">{{.TestNote}}</div>{{end}}{{else}}<span class="sub">keiner im Zeitraum</span>{{end}}</td></tr>{{end}}
</tbody></table></div>{{else}}<p class="sub">Für diese Auswahl liegen im Zeitraum keine Sicherungen vor.</p>{{end}}</section>

<section><h2>Wo die Sicherungen heute liegen</h2><div style="overflow-x:auto"><table>
<thead><tr><th>Server</th><th>Ort</th><th>Art</th><th class="num">Stände</th><th>Neuester Stand</th></tr></thead><tbody>
{{range .R.Guests}}{{$g := .}}{{range $i, $c := .Copies}}<tr><td>{{if not $i}}<span class="tag">{{$g.Kind}}</span> <b>{{$g.ID}}</b> {{$g.Name}}{{end}}</td><td>{{$c.Where}}</td><td>{{kindName $c.Kind}}</td><td class="num">{{$c.Count}}</td>
<td class="nowrap num">{{dayTime $c.Last}}{{if $c.Seen}}<div class="sub">Platte zuletzt gelesen {{day $c.Seen}}</div>{{end}}</td></tr>{{else}}<tr><td><span class="tag">{{$g.Kind}}</span> <b>{{$g.ID}}</b> {{$g.Name}}</td><td colspan="4" class="err">keine Sicherung gefunden</td></tr>{{end}}{{end}}
</tbody></table></div></section>

<section><h2>So ist der Nachweis entstanden</h2><p class="sub" style="margin:0">Die Appliance zeichnet jeden Sicherungslauf von Proxmox VE mit dem Ergebnis je Server auf. „Tage mit Sicherung“ zählt die Kalendertage mit mindestens einer erfolgreichen Sicherung, „längste Lücke“ die längste Zeit ohne eine. Die Stände unter „Wo die Sicherungen heute liegen“ sind die am {{day .R.Built}} vorhandenen; ältere sind nach der eingestellten Aufbewahrung gelöscht. Restore-Tests spielen eine Sicherung als Test-Server zurück und prüfen, ob er startet.</p>
<div class="sign"><div>Ort, Datum</div><div>Unterschrift{{if .R.By}} ({{.R.By}}){{end}}</div></div></section>

<footer><span>Erstellt am {{dayTime .R.Built}} von PK95 ProxBackup {{.Version}}</span><span>{{.R.Host}}</span></footer>
</div></body></html>`))

// proofDocument renders the proof for ?from=YYYY-MM-DD&to=YYYY-MM-DD&guests=vm/101,…&for=…
func (s *Server) proofDocument(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err1 := time.ParseInLocation("2006-01-02", q.Get("from"), time.Local)
	to, err2 := time.ParseInLocation("2006-01-02", q.Get("to"), time.Local)
	if err1 != nil || err2 != nil {
		writeErr(w, http.StatusBadRequest, "Zeitraum als von=JJJJ-MM-TT und bis=JJJJ-MM-TT angeben.")
		return
	}
	var guests []string
	for _, g := range strings.Split(q.Get("guests"), ",") {
		if g = strings.TrimSpace(g); g != "" {
			guests = append(guests, g)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	rep, err := s.C.BuildProof(ctx, core.ProofRequest{From: from.Unix(), To: to.AddDate(0, 0, 1).Unix(), Guests: guests, For: q.Get("for"), By: currentUser(r).Username})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	download := q.Get("download") == "1"
	dq := r.URL.Query()
	dq.Set("download", "1")
	var b bytes.Buffer
	if err := proofTmpl.Execute(&b, map[string]any{"R": rep, "Last": rep.To - 1, "Tools": !download, "Download": "?" + dq.Encode(), "Version": s.C.Version}); err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; script-src 'unsafe-inline'")
	if download {
		w.Header().Set("Content-Disposition", `attachment; filename="Sicherungsnachweis-`+q.Get("from")+`-bis-`+q.Get("to")+`.html"`)
	}
	_, _ = w.Write(b.Bytes())
}
