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

// Emergency handbook: settings, status, the printable document.

var handbookFuncs = template.FuncMap{
	"day":     monthlyFuncs["day"],
	"dayTime": monthlyFuncs["dayTime"],
	"bytes":   humanBytes,
	"join":    strings.Join,
	"role": func(r string) string {
		return map[string]string{"admin": "Administrator", "operator": "Bediener", "files": "Dateien wiederherstellen", "viewer": "Nur lesen"}[r]
	},
	"guard": func(g string) string {
		return map[string]string{"protected": "an", "open": "aus", "user": "nicht möglich", "unknown": "unbekannt"}[g]
	},
	"https": func(ip string) string {
		if strings.Contains(ip, ":") {
			return "https://[" + ip + "]"
		}
		return "https://" + ip
	},
}

var handbookTmpl = template.Must(template.New("h").Funcs(handbookFuncs).Parse(`<!doctype html>
<html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Notfallhandbuch · {{.H.Host}} · {{day .H.Built}}</title>
<style>
:root { --ink:#111827; --muted:#6b7280; --line:#e5e7eb; --soft:#f9fafb; --brand:#1f3d6e; --c600:#1f73d6; }
* { box-sizing:border-box; }
body { margin:0; background:#f3f4f6; color:var(--ink); font:14px/1.5 "Space Grotesk",-apple-system,"Segoe UI",Roboto,Helvetica,Arial,sans-serif; }
.page { max-width:1100px; margin:24px auto; background:#fff; border:1px solid var(--line); border-radius:4px; }
header { display:flex; justify-content:space-between; align-items:center; gap:12px; padding:18px 28px; border-bottom:1px solid var(--line); }
.brand { display:flex; align-items:center; gap:10px; font-weight:700; font-size:19px; color:var(--brand); }
.logo { width:28px; height:28px; border-radius:3px; background:#0d1e35; color:#bce0ff; display:grid; place-items:center; font-weight:700; }
.meta { font-size:12px; color:var(--muted); text-align:right; font-family:Menlo,Consolas,monospace; }
.band { padding:22px 28px; border-left:6px solid #1f73d6; background:#e0efff; color:#0d2a4f; }
.band .k { font-size:11px; font-weight:700; letter-spacing:.08em; text-transform:uppercase; opacity:.85; }
.band h1 { margin:4px 0 0; font-size:24px; line-height:1.25; } .band p { margin:6px 0 0; }
section { padding:20px 28px 0; break-inside:auto; }
h2 { font-size:16px; margin:0 0 10px; padding-top:4px; border-top:2px solid var(--ink); }
h3 { font-size:14px; margin:14px 0 6px; }
.facts { display:grid; grid-template-columns:repeat(auto-fit,minmax(220px,1fr)); gap:10px 24px; }
.fact { border-bottom:1px solid #f3f4f6; padding:4px 0; } .fact span { display:block; color:var(--muted); font-size:12px; } .fact b { font-weight:600; }
table { width:100%; border-collapse:collapse; font-size:12.5px; }
th { text-align:left; font-size:11px; text-transform:uppercase; letter-spacing:.04em; color:var(--muted); font-weight:600; padding:7px 8px; border-bottom:1px solid var(--line); background:var(--soft); white-space:nowrap; }
td { padding:7px 8px; border-bottom:1px solid #f3f4f6; vertical-align:top; }
.mono, code { font-family:Menlo,Consolas,monospace; font-size:11.5px; word-break:break-all; }
pre.cmd { font-family:Menlo,Consolas,monospace; font-size:11.5px; background:#f3f4f6; border:1px solid var(--line); border-radius:3px; padding:8px 10px; white-space:pre-wrap; word-break:break-all; margin:6px 0; }
.text { white-space:pre-wrap; border:1px solid var(--line); border-radius:3px; padding:10px 12px; background:var(--soft); min-height:44px; }
.blank { color:var(--muted); font-style:italic; }
.steps { margin:0; padding-left:20px; } .steps li { margin:3px 0; }
.case { border:1px solid var(--line); border-radius:4px; padding:10px 14px; margin:0 0 10px; break-inside:avoid; }
.case b.t { display:block; font-size:14px; margin-bottom:4px; }
.sub { color:var(--muted); font-size:12px; } .nowrap { white-space:nowrap; } .num { font-variant-numeric:tabular-nums; } .err { color:#b91c1c; font-weight:600; }
.warn { background:#fef3c7; color:#78350f; border-left:4px solid #d97706; padding:8px 12px; margin:0 0 10px; }
.empty { color:var(--muted); font-size:13px; margin:0; }
footer { margin-top:24px; padding:16px 28px; border-top:1px solid var(--line); color:var(--muted); font-size:12px; display:flex; justify-content:space-between; gap:12px; flex-wrap:wrap; }
.tools { max-width:1100px; margin:16px auto 0; display:flex; gap:8px; justify-content:flex-end; }
.tools a, .tools button { font:700 13px inherit; font-family:inherit; padding:7px 14px; border-radius:3px; border:1px solid #d1d5db; background:#fff; color:#374151; text-decoration:none; cursor:pointer; }
.tools .primary { background:var(--c600); color:#fff; border-color:var(--c600); }
@media print { body { background:#fff; font-size:12px; } .page { margin:0; border:0; max-width:none; } .tools { display:none; } tr, .case { break-inside:avoid; } h2 { break-after:avoid; } }
</style></head><body>
{{if .Tools}}<div class="tools"><button type="button" onclick="window.print()">Drucken / als PDF</button><a class="primary" href="?download=1">HTML herunterladen</a></div>{{end}}
<div class="page">
<header><div class="brand"><span class="logo">↓</span>PK95 ProxBackup</div><div class="meta">Notfallhandbuch<br>{{.H.Host}}</div></header>
<div class="band"><div class="k">Datensicherung · vertraulich</div><h1>Notfallhandbuch – Stand {{day .H.Built}}</h1>
<p>Ausgedruckt außer Haus aufbewahren. Das Wiederherstellungs-Passwort der Appliance und die Papierschlüssel stehen bewusst nicht hier – sie gehören in einen eigenen, verschlossenen Umschlag an einem anderen Ort.</p></div>
{{if .H.Problems}}<section><div class="warn"><b>Beim Erstellen nicht erreichbar:</b> {{join .H.Problems "; "}} – die Angaben dazu fehlen oder sind unvollständig.</div></section>{{end}}

<section><h2>1. Im Notfall</h2>
<h3>Ansprechpartner</h3><div class="text">{{if .H.Settings.Contacts}}{{.H.Settings.Contacts}}{{else}}<span class="blank">Noch nicht eingetragen – wer wird angerufen (Name, Telefon, Dienstleister)?</span>{{end}}</div>
<h3>Reihenfolge beim Wiederherstellen</h3><div class="text">{{if .H.Settings.Order}}{{.H.Settings.Order}}{{else}}<span class="blank">Noch nicht eingetragen – welche Server zuerst (z. B. Domänencontroller, Datenbank, Dateiserver)?</span>{{end}}</div>
<h3>Wo liegt was</h3><div class="text">{{if .H.Settings.Notes}}{{.H.Settings.Notes}}{{else}}<span class="blank">Noch nicht eingetragen – wo liegen Wechselplatten, Umschlag mit Passwort und Papierschlüssel, Zugangsdaten?</span>{{end}}</div>
</section>

<section><h2>2. Was tun, wenn …</h2>
<div class="case"><b class="t">… ein Server (VM/Container) kaputt ist oder Dateien fehlen</b><ol class="steps">
<li>Appliance öffnen: {{range $i, $ip := .H.IPs}}{{if $i}} oder {{end}}<code>{{https $ip}}</code>{{else}}<code>https://{{.H.Host}}</code>{{end}}</li>
<li>Wiederherstellen → Server wählen → Stand wählen (der Tag vor dem Fehler).</li>
<li>„Dateien und Ordner“ lädt einzelne Dateien; „Ganze VM“ spielt sie als neue VM neben dem Original oder ersetzt es.</li></ol></div>
<div class="case"><b class="t">… ein Proxmox-VE-Knoten ausgefallen ist</b><ol class="steps">
<li>Die Gäste lassen sich auf einem anderen Knoten aus der Sicherung starten (Wiederherstellen → Ziel-Knoten wählen).</li>
<li>Knoten neu aufbauen: Proxmox VE in derselben Version mit gleichem Namen und gleicher IP installieren. In der Appliance unter Proxmox-VE-Hosts → Knoten stehen Dateien, Befehle und Anleitung.</li></ol></div>
<div class="case"><b class="t">… die Appliance selbst ausgefallen ist</b><ol class="steps">
<li>Auf einem Proxmox-VE-Knoten in der Shell eine neue Appliance anlegen:<pre class="cmd">bash -c "$(curl -fsSL https://raw.githubusercontent.com/{{.Repo}}/main/install/proxmox.sh)"</pre></li>
<li>Bei der Ersteinrichtung „Aus einer Sicherung wiederherstellen“ wählen. Die Sicherung der Konfiguration liegt {{if .H.SelfBackup.Enabled}}in: {{join .H.SelfBackup.Targets ", "}} (zuletzt {{day .H.SelfBackup.LastOK}}){{else}}<span class="err">nirgends – die Sicherung der Appliance ist ausgeschaltet</span>{{end}}.</li>
<li>Wiederherstellungs-Passwort aus dem Umschlag eingeben. Danach sind Verbindungen, Pläne und Konten zurück; Datastore-Platten wieder anschließen.</li></ol></div>
<div class="case"><b class="t">… alles verloren ist (Brand, Diebstahl, Verschlüsselungstrojaner)</b><ol class="steps">
<li>Bei Erpressung zuerst: Netzwerk trennen, nichts löschen, nichts bezahlen, Ansprechpartner anrufen.</li>
<li>Neue Hardware, Proxmox VE installieren, dann die Appliance wie oben anlegen.</li>
<li>Eine Kopie außer Haus holen: Wechsel- oder Archiv-Platte (Abschnitt 7){{range .H.Servers}}{{if not .Local}}, Backup-Server „{{.Name}}“{{end}}{{end}}. Platte anstecken und unter Wechselplatten → „Fremde Platte einlesen“ einbinden.</li>
{{if .H.Keys}}<li>Verschlüsselte Sicherungen brauchen ihren Schlüssel: Papierschlüssel einscannen (Einstellungen → Verschlüsselung → Importieren) und den Fingerabdruck mit Abschnitt 8 vergleichen.</li>{{end}}
<li>Server in der Reihenfolge aus Abschnitt 1 zurückspielen; vor dem Einspielen prüfen, ob die Sicherung vor dem Befall liegt (Übersicht → Auffällige Sicherungen).</li></ol></div>
</section>

<section><h2>3. Appliance</h2><div class="facts">
<div class="fact"><span>Name</span><b>{{.H.Host}}</b></div>
<div class="fact"><span>Adresse</span><b>{{range $i, $ip := .H.IPs}}{{if $i}}, {{end}}{{https $ip}}{{else}}–{{end}}</b></div>
<div class="fact"><span>Version</span><b>{{.H.Version}}</b></div>
<div class="fact"><span>Sicherung der Konfiguration</span><b>{{if .H.SelfBackup.Enabled}}täglich, zuletzt {{day .H.SelfBackup.LastOK}}{{else}}<span class="err">ausgeschaltet</span>{{end}}</b></div>
</div></section>

<section><h2>4. Backup-Server und Datastores</h2>{{range .H.Servers}}
<h3>{{.Name}}{{if .Local}} <span class="sub">(in der Appliance)</span>{{end}}</h3>
<div class="facts"><div class="fact"><span>Adresse</span><b class="mono">https://{{.Address}}:{{.Port}}</b></div>
<div class="fact"><span>Fingerabdruck</span><b class="mono">{{if .Fingerprint}}{{.Fingerprint}}{{else}}–{{end}}</b></div></div>
{{if .Stores}}<table><thead><tr><th>Datastore</th><th>Art</th><th>Ort</th><th class="num">Belegt</th></tr></thead><tbody>
{{range .Stores}}<tr><td><b>{{.Name}}</b></td><td>{{.Kind}}</td><td class="mono">{{.Where}}</td><td class="num nowrap">{{if .Total}}{{bytes .Used}} von {{bytes .Total}}{{else}}–{{end}}</td></tr>{{end}}
</tbody></table>{{end}}{{else}}<p class="empty">Kein Backup-Server verbunden.</p>{{end}}</section>

<section><h2>5. Proxmox VE</h2>{{range .H.Clusters}}
<h3>{{.Name}} <span class="sub mono">{{.Address}}</span></h3>
<table><thead><tr><th>Knoten</th><th>Adresse</th><th>Host-Sicherung</th></tr></thead><tbody>
{{range .Nodes}}<tr><td><b>{{.Name}}</b></td><td class="mono">{{.IP}}</td><td>{{if .HostBackup}}zuletzt {{day .HostBackup}}{{else if .HostOn}}<span class="err">noch keine</span>{{else}}<span class="sub">aus</span>{{end}}</td></tr>{{end}}
</tbody></table>
{{if .Storages}}<table style="margin-top:8px"><thead><tr><th>Speicher</th><th>Ziel</th><th>Verschlüsselt</th><th>Löschschutz</th></tr></thead><tbody>
{{range .Storages}}<tr><td><b>{{.Name}}</b></td><td>{{.Target}}</td><td>{{if .Encrypted}}ja{{if .KeyName}} – Schlüssel „{{.KeyName}}“{{end}}{{else}}nein{{end}}</td><td>{{guard .Guard}}</td></tr>{{end}}
</tbody></table>{{end}}{{else}}<p class="empty">Kein Proxmox VE verbunden.</p>{{end}}</section>

<section><h2>6. Wo liegt welcher Server</h2>{{if .H.Guests}}<table>
<thead><tr><th>ID</th><th>Name</th><th>Knoten</th><th>Sicherungen auf</th><th>USB-Platten</th><th>Letzte Sicherung</th><th>Restore-Test</th></tr></thead><tbody>
{{range .H.Guests}}<tr><td class="num"><b>{{.ID}}</b> <span class="sub">{{.Type}}</span></td><td>{{.Name}}{{if not .InJob}} <span class="err">(in keinem Job)</span>{{end}}</td><td>{{.Node}}</td>
<td>{{join .Fixed ", "}}{{if .Archive}}<div class="sub">Archiv: {{join .Archive ", "}}</div>{{end}}</td><td>{{join .USB ", "}}</td>
<td class="nowrap num">{{day .Last}}</td><td class="nowrap num">{{day .LastTest}}</td></tr>{{end}}
</tbody></table>{{else}}<p class="empty">Keine VMs und Container gefunden.</p>{{end}}</section>

<section><h2>7. Wechsel- und Archiv-Platten</h2>{{range .H.Rotations}}
<h3>{{.Name}} <span class="sub">– {{.Rhythm}}, angesteckt an {{.Server}}</span></h3>
<table><thead><tr><th>Platte (Aufschrift)</th><th>Datastore</th><th>Dran am</th></tr></thead><tbody>
{{range .Disks}}<tr><td><b>{{.Label}}</b></td><td class="mono">{{.Store}}</td><td>{{if .Plan}}{{.Plan}}{{else}}<span class="sub">im Wechsel</span>{{end}}</td></tr>{{end}}
</tbody></table>{{end}}
{{if .H.Archives}}<h3>Archiv-Platten</h3><table><thead><tr><th>Platte</th><th>Aufbewahrt</th><th>Inhalt</th><th>Erstellt</th></tr></thead><tbody>
{{range .H.Archives}}<tr><td><b>{{.Name}}</b> <span class="sub mono">{{.Store}}</span></td><td>{{if .Location}}{{.Location}}{{else}}<span class="sub">nicht angegeben</span>{{end}}</td><td>{{.Servers}}</td><td class="nowrap num">{{day .Created}}</td></tr>{{end}}
</tbody></table>{{end}}
{{if not (or .H.Rotations .H.Archives)}}<p class="empty">Keine Wechsel- oder Archiv-Platten eingerichtet.</p>{{end}}</section>

<section><h2>8. Schlüssel</h2>{{if .H.Keys}}<p class="sub" style="margin:0 0 6px">Nur die Fingerabdrücke – zum Vergleichen beim Import. Die Schlüssel selbst liegen als Papierschlüssel im Umschlag.</p>
<table><thead><tr><th>Schlüssel</th><th>Fingerabdruck</th><th>Für</th><th>Kopie außer Haus</th></tr></thead><tbody>
{{range .H.Keys}}<tr><td><b>{{.Name}}</b></td><td class="mono">{{.Fingerprint}}</td><td>{{join .Storages ", "}}</td><td>{{if .Saved}}bestätigt am {{day .Saved}}{{else}}<span class="err">fehlt</span>{{end}}</td></tr>{{end}}
</tbody></table>{{else}}<p class="empty">Die Sicherungen sind nicht verschlüsselt – im Notfall wird kein Schlüssel gebraucht.</p>{{end}}</section>

{{if .H.Shares}}<section><h2>9. Freigaben</h2><table><thead><tr><th>Freigabe</th><th>Art</th><th>Quelle</th><th>Eingehängt auf</th><th>Datastore</th></tr></thead><tbody>
{{range .H.Shares}}<tr><td><b>{{.Name}}</b></td><td>{{.Type}}</td><td class="mono">{{.Source}}</td><td>{{.Server}}</td><td>{{.Store}}</td></tr>{{end}}
</tbody></table></section>{{end}}

<section><h2>{{if .H.Shares}}10{{else}}9{{end}}. Konten der Appliance</h2><table><thead><tr><th>Benutzer</th><th>Rolle</th><th>Zwei-Faktor</th></tr></thead><tbody>
{{range .H.Accounts}}<tr><td><b>{{.Username}}</b></td><td>{{role .Role}}</td><td>{{if .TOTP}}Pflicht{{else}}<span class="sub">freiwillig</span>{{end}}</td></tr>{{end}}
</tbody></table><p class="sub">Passwörter stehen nicht in diesem Dokument.</p></section>

<footer><span>Erstellt am {{dayTime .H.Built}} von PK95 ProxBackup {{.H.Version}}</span><span>Enthält keine Passwörter und keine Schlüssel</span></footer>
</div></body></html>`))

func (s *Server) handbookDocument(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	h := s.C.BuildHandbook(ctx)
	download := r.URL.Query().Get("download") == "1"
	var b bytes.Buffer
	if err := handbookTmpl.Execute(&b, map[string]any{"H": h, "Tools": !download, "Repo": core.UpdateRepo}); err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; script-src 'unsafe-inline'")
	if download {
		w.Header().Set("Content-Disposition", `attachment; filename="Notfallhandbuch-`+time.Now().Format("2006-01-02")+`.html"`)
	}
	_, _ = w.Write(b.Bytes())
}

func (s *Server) getHandbook(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	h := s.C.BuildHandbook(ctx)
	writeJSON(w, http.StatusOK, map[string]any{"contacts": h.Settings.Contacts, "order": h.Settings.Order, "notes": h.Settings.Notes,
		"status": h.Status, "problems": h.Problems, "counts": map[string]int{"servers": len(h.Servers), "clusters": len(h.Clusters), "guests": len(h.Guests),
			"rotations": len(h.Rotations), "archives": len(h.Archives), "keys": len(h.Keys)}})
}

func (s *Server) putHandbook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Contacts string `json:"contacts"`
		Order    string `json:"order"`
		Notes    string `json:"notes"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.C.SetHandbookTexts(req.Contacts, req.Order, req.Notes); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.getHandbook(w, r)
}

func (s *Server) handbookPrinted(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	st, err := s.C.MarkHandbookPrinted(ctx, currentUser(r).Username)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}
