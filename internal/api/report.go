package api

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"time"

	"pk95proxbackup/internal/core"
)

type reportRow struct {
	core.RestoreTestResult
	Kind, StatusWord, StatusClass, BackupAt, StartAt, RestoreEndAt, EndAt, RestoreDur, BootDur, TotalDur, Size string
	Image                                                                                                      template.URL
}

var reportTmpl = template.Must(template.New("r").Parse(`<!doctype html>
<html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Restore-Test {{.Run.PlanName}} · {{.Start}}</title>
<style>
:root { --ink:#111827; --muted:#6b7280; --line:#e5e7eb; --soft:#f9fafb; --brand:#1f3d6e; --c600:#1f73d6; }
* { box-sizing:border-box; }
body { margin:0; background:#f3f4f6; color:var(--ink); font:14px/1.5 "Space Grotesk",-apple-system,"Segoe UI",Roboto,Helvetica,Arial,sans-serif; }
.page { max-width:1180px; margin:24px auto; background:#fff; border:1px solid var(--line); border-radius:4px; }
header { display:flex; justify-content:space-between; align-items:center; gap:12px; padding:18px 28px; border-bottom:1px solid var(--line); }
.brand { display:flex; align-items:center; gap:10px; font-weight:700; font-size:19px; color:var(--brand); }
.logo { width:28px; height:28px; border-radius:3px; background:#0d1e35; color:#bce0ff; display:grid; place-items:center; font-weight:700; }
.meta { font-size:12px; color:var(--muted); text-align:right; font-family:Menlo,Consolas,monospace; }
.band { padding:22px 28px; border-left:6px solid; }
.band.ok { background:#dcfce7; border-color:#16a34a; color:#14532d; } .band.warning { background:#fef3c7; border-color:#d97706; color:#78350f; }
.band.error { background:#fee2e2; border-color:#dc2626; color:#7f1d1d; } .band.cancelled { background:#fef3c7; border-color:#d97706; color:#78350f; }
.band .k { font-size:11px; font-weight:700; letter-spacing:.08em; text-transform:uppercase; opacity:.85; }
.band h1 { margin:4px 0 0; font-size:24px; line-height:1.25; }
.band p { margin:6px 0 0; }
section { padding:20px 28px 0; }
h2 { font-size:16px; margin:0 0 10px; }
.facts { display:grid; grid-template-columns:repeat(auto-fit,minmax(220px,1fr)); gap:10px 24px; }
.fact { border-bottom:1px solid #f3f4f6; padding:4px 0; } .fact span { display:block; color:var(--muted); font-size:12px; } .fact b { font-weight:600; }
.chips { display:flex; gap:8px; flex-wrap:wrap; margin-top:14px; }
.chip { padding:3px 10px; border-radius:2px; font-size:12px; font-weight:700; }
.c-ok { background:#dcfce7; color:#166534; } .c-warning { background:#fef3c7; color:#78350f; } .c-error { background:#fee2e2; color:#991b1b; } .c-skipped { background:#fef3c7; color:#78350f; }
table { width:100%; border-collapse:collapse; font-size:13px; }
th { text-align:left; font-size:11px; text-transform:uppercase; letter-spacing:.04em; color:var(--muted); font-weight:600; padding:8px 10px; border-bottom:1px solid var(--line); background:var(--soft); white-space:nowrap; }
td { padding:10px; border-bottom:1px solid #f3f4f6; vertical-align:top; }
.pill { display:inline-block; padding:2px 8px; border-radius:2px; font-size:11px; font-weight:700; text-transform:uppercase; white-space:nowrap; }
.tag { font-family:Menlo,Consolas,monospace; font-size:11px; background:#f3f4f6; color:#374151; padding:0 5px; border-radius:2px; }
.sub { color:var(--muted); font-size:12px; } .nowrap { white-space:nowrap; } .num { font-variant-numeric:tabular-nums; }
.err { color:#b91c1c; }
.shots { display:grid; grid-template-columns:repeat(auto-fill,minmax(340px,1fr)); gap:16px; }
figure { margin:0; border:1px solid var(--line); border-radius:3px; overflow:hidden; background:var(--soft); }
figure img { display:block; width:100%; height:auto; background:#000; }
figcaption { padding:8px 10px; font-size:12px; }
footer { margin-top:24px; padding:16px 28px; border-top:1px solid var(--line); color:var(--muted); font-size:12px; display:flex; justify-content:space-between; gap:12px; flex-wrap:wrap; }
.tools { max-width:1180px; margin:16px auto 0; display:flex; gap:8px; justify-content:flex-end; }
.tools a, .tools button { font:700 13px inherit; font-family:inherit; padding:7px 14px; border-radius:3px; border:1px solid #d1d5db; background:#fff; color:#374151; text-decoration:none; cursor:pointer; }
.tools .primary { background:var(--c600); color:#fff; border-color:var(--c600); }
@media print { body { background:#fff; } .page { margin:0; border:0; max-width:none; } .tools { display:none; } tr, figure { break-inside:avoid; } }
</style></head><body>
{{if .Tools}}<div class="tools"><button type="button" onclick="window.print()">Drucken / als PDF</button><a class="primary" href="?run={{.Run.ID}}&amp;download=1">HTML herunterladen</a></div>{{end}}
<div class="page">
<header><div class="brand"><span class="logo">↓</span>PK95 ProxBackup</div><div class="meta">Restore-Test-Bericht<br>{{.Host}}</div></header>
<div class="band {{.Run.Status}}"><div class="k">{{.Word}} · Wiederherstellungstest</div><h1>{{.Title}}</h1><p>{{.Summary}}</p></div>
<section><div class="facts">
  <div class="fact"><span>Plan</span><b>{{.Run.PlanName}}</b></div>
  <div class="fact"><span>Proxmox VE</span><b>{{or .Run.Cluster "–"}}</b></div>
  <div class="fact"><span>Getestet auf</span><b>{{or .Run.Node "–"}}{{if .Run.Storage}} / {{.Run.Storage}}{{end}}</b></div>
  <div class="fact"><span>Sicherungen aus</span><b>{{or .Run.Source "–"}}</b></div>
  <div class="fact"><span>Beginn</span><b>{{.Start}}</b></div>
  <div class="fact"><span>Ende</span><b>{{.End}}</b></div>
  <div class="fact"><span>Gesamtdauer</span><b>{{.Total}}</b></div>
  <div class="fact"><span>Gestartet von</span><b>{{.Run.By}}</b></div>
</div>
<div class="chips">{{range .Counts}}<span class="chip c-{{.Class}}">{{.N}} {{.Word}}</span>{{end}}</div>
{{if .Run.Error}}<p class="err"><b>Fehler:</b> {{.Run.Error}}</p>{{end}}
</section>
<section><h2>Ergebnis je Server</h2><div style="overflow-x:auto"><table>
<thead><tr><th>Server</th><th>Status</th><th>Sicherung vom</th><th>Test-ID</th><th>Restore</th><th>Start</th><th>Gesamt</th><th>Größe</th><th>Ergebnis</th></tr></thead><tbody>
{{range .Rows}}<tr>
  <td><span class="tag">{{.Kind}}</span> <b>{{.VMID}}</b> {{.Name}}</td>
  <td><span class="pill c-{{.StatusClass}}">{{.StatusWord}}</span></td>
  <td class="nowrap num">{{.BackupAt}}</td>
  <td class="num">{{if .TestID}}{{.TestID}}{{else}}–{{end}}</td>
  <td class="nowrap num">{{.RestoreDur}}{{if .StartAt}}<div class="sub">{{.StartAt}} – {{.RestoreEndAt}}</div>{{end}}</td>
  <td class="nowrap num">{{.BootDur}}</td>
  <td class="nowrap num">{{.TotalDur}}{{if .EndAt}}<div class="sub">bis {{.EndAt}}</div>{{end}}</td>
  <td class="nowrap num">{{.Size}}</td>
  <td><div {{if eq .Status "error"}}class="err"{{end}}>{{.Info}}</div>{{if .OS}}<div class="sub">System: {{.OS}}</div>{{end}}{{if .Hostname}}<div class="sub">Hostname: {{.Hostname}}</div>{{end}}{{if .Notes}}<div class="sub">Isoliert: {{.Notes}}</div>{{end}}</td>
</tr>{{end}}
</tbody></table></div></section>
{{if .Shots}}<section><h2>Bildschirm nach dem Start</h2><div class="shots">{{range .Shots}}<figure><img src="{{.Image}}" alt="Screenshot {{.VMID}}"><figcaption><b>{{.VMID}} {{.Name}}</b> · <span class="pill c-{{.StatusClass}}">{{.StatusWord}}</span></figcaption></figure>{{end}}</div></section>{{end}}
<section><p class="sub">So wurde getestet: Jeder Server wurde aus seiner neuesten Sicherung unter einer eigenen Test-ID wiederhergestellt, mit getrenntem Netzwerk und ohne durchgereichte Geräte gestartet, geprüft und anschließend wieder gelöscht. Die laufenden Server waren davon nicht betroffen.</p></section>
<footer><span>Erstellt am {{.Now}} von PK95 ProxBackup {{.Version}}</span><span>Lauf {{.Run.ID}}</span></footer>
</div></body></html>`))

func secs(n int64) string {
	if n <= 0 {
		return "–"
	}
	if n < 60 {
		return fmt.Sprintf("%d s", n)
	}
	if n < 3600 {
		return fmt.Sprintf("%d:%02d min", n/60, n%60)
	}
	return fmt.Sprintf("%d:%02d:%02d h", n/3600, n%3600/60, n%60)
}

func clock(ts int64, day bool) string {
	if ts == 0 {
		return ""
	}
	if day {
		return time.Unix(ts, 0).Format("02.01.2006 15:04")
	}
	return time.Unix(ts, 0).Format("15:04:05")
}

func (s *Server) restoreTestReport(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("run")
	var run *core.RestoreTestRun
	for _, x := range s.C.RestoreTestRuns() {
		if x.ID == id {
			x := x
			run = &x
		}
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "Lauf nicht gefunden.")
		return
	}
	download := r.URL.Query().Get("download") == "1"
	b, err := s.renderReport(*run, func(file string) (string, []byte) {
		data, err := os.ReadFile(s.C.ScreenshotPath(run.ID, file))
		if err != nil {
			return "", nil
		}
		return "image/png", data
	}, !download)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; script-src 'unsafe-inline'")
	if download {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="restore-test-%s.html"`, fileNameRe.FindString(run.ID)))
	}
	_, _ = w.Write(b)
}

// renderReport renders the stand-alone report; images delivers the picture of a result.
func (s *Server) renderReport(run core.RestoreTestRun, images func(file string) (string, []byte), tools bool) ([]byte, error) {
	status := map[string][2]string{"ok": {"Erfolgreich", "ok"}, "warning": {"Hinweis", "warning"}, "error": {"Fehler", "error"},
		"skipped": {"Übersprungen", "skipped"}, "running": {"Läuft", "warning"}, "pending": {"Wartet", "warning"}, "cancelled": {"Abgebrochen", "warning"}}
	var rows, shots []reportRow
	counts := map[string]int{}
	for _, res := range run.Results {
		st := status[res.Status]
		counts[res.Status]++
		kind := "VM"
		if res.Type == "ct" {
			kind = "CT"
		}
		total := int64(0)
		if res.Started > 0 && res.Finished > 0 {
			total = res.Finished - res.Started
		}
		row := reportRow{RestoreTestResult: res, Kind: kind, StatusWord: st[0], StatusClass: st[1], BackupAt: clock(res.BackupTime, true),
			StartAt: clock(res.Started, false), RestoreEndAt: clock(res.RestoreEnd, false), EndAt: clock(res.Finished, false),
			RestoreDur: secs(res.Restore), BootDur: secs(res.Boot), TotalDur: secs(total), Size: humanBytes(res.DiskBytes)}
		if res.DiskBytes == 0 {
			row.Size = "–"
		}
		if res.Screenshot != "" {
			if ctype, b := images(res.Screenshot); len(b) > 0 {
				row.Image = template.URL("data:" + ctype + ";base64," + base64.StdEncoding.EncodeToString(b))
				shots = append(shots, row)
			}
		}
		rows = append(rows, row)
	}
	type count struct {
		N           int
		Word, Class string
	}
	var cs []count
	for _, k := range []struct{ key, word string }{{"ok", "erfolgreich"}, {"warning", "mit Hinweis"}, {"error", "fehlgeschlagen"}, {"skipped", "übersprungen"}} {
		if counts[k.key] > 0 {
			cs = append(cs, count{counts[k.key], k.word, k.key})
		}
	}
	total := len(run.Results)
	title, summary := "", ""
	switch run.Status {
	case "ok":
		title = fmt.Sprintf("Alle %d Server lassen sich aus der Sicherung starten", total)
		if total == 1 {
			title = "Der Server lässt sich aus der Sicherung starten"
		}
		summary = "Die Sicherungen sind einsatzbereit."
	case "running":
		title, summary = "Der Test läuft noch", fmt.Sprintf("%d von %d Servern sind fertig.", counts["ok"]+counts["warning"]+counts["error"]+counts["skipped"], total)
	case "cancelled":
		title, summary = "Test abgebrochen", fmt.Sprintf("%d von %d Servern wurden bis dahin erfolgreich getestet.", counts["ok"], total)
	default:
		title = fmt.Sprintf("%d von %d Servern ohne Probleme", counts["ok"], total)
		summary = "Bei den übrigen Servern gab es Fehler oder Hinweise, Details unten."
	}
	end := run.End
	if end == 0 {
		end = time.Now().Unix()
	}
	host, _ := os.Hostname()
	data := map[string]any{"Run": &run, "Rows": rows, "Shots": shots, "Counts": cs, "Title": title, "Summary": summary,
		"Word": status[run.Status][0], "Start": clock(run.Start, true), "End": clock(run.End, true), "Total": secs(end - run.Start),
		"Host": host, "Version": s.C.Version, "Now": time.Now().Format("02.01.2006 15:04"), "Tools": tools}
	var b bytes.Buffer
	if err := reportTmpl.Execute(&b, data); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
