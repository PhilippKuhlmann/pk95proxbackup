package api

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/hostexec"
	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
	"pk95proxbackup/internal/shares"
	"pk95proxbackup/internal/store"
)

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	var ov core.Overview
	if r.URL.Query().Get("fresh") == "1" {
		ov = s.C.BuildOverview(r.Context())
	} else {
		ov = s.C.CachedOverview(r.Context())
	}
	evs, _ := s.C.St.Events(r.Context(), "", 0, 8)
	out := map[string]any{"overview": ov, "events": evs}
	// The newest finished restore test, for the status line of the dashboard.
	for _, run := range s.C.RestoreTestRuns() {
		if run.Status == "running" {
			continue
		}
		okN := 0
		for _, x := range run.Results {
			if x.Status == "ok" {
				okN++
			}
		}
		out["restore_test"] = map[string]any{"plan": run.PlanName, "status": run.Status, "end": run.End, "ok": okN, "total": len(run.Results)}
		break
	}
	// Disks with problems, archive disks that lay unchecked too long.
	type diskIssue struct {
		Status  string `json:"status"`
		Label   string `json:"label"`
		Host    string `json:"host"`
		Use     string `json:"use"`
		Problem string `json:"problem"`
		Present bool   `json:"present"`
	}
	disks := []diskIssue{}
	counts := map[string]int{}
	for _, h := range s.C.DiskHealths() {
		counts[h.Status]++
		if h.Status == "error" || h.Status == "warning" {
			p := ""
			if len(h.Problems) > 0 {
				p = h.Problems[0]
			}
			disks = append(disks, diskIssue{Status: h.Status, Label: core.DiskLabel(h.DiskHealth), Host: h.Host, Use: h.Use, Problem: p, Present: h.Present})
		}
	}
	out["disk_health"] = map[string]any{"counts": counts, "issues": disks}
	stale := []map[string]any{}
	if list, err := s.C.Archives(); err == nil {
		for _, a := range list {
			if a.Status == "running" {
				continue
			}
			if last := s.C.ArchiveLastSeen(a); last > 0 && time.Since(time.Unix(last, 0)) > core.ArchiveCheckAfter {
				stale = append(stale, map[string]any{"id": a.ID, "name": a.Name, "last_seen": last})
			}
		}
	}
	out["stale_archives"] = stale
	up := s.C.Update()
	out["update"] = map[string]any{"available": up.Available, "latest": up.Latest, "current": up.Current}
	sb := s.C.SelfBackup()
	out["selfbackup"] = map[string]any{"enabled": sb.Enabled, "has_key": sb.HasKey, "last_ok": sb.LastOK, "last_error": sb.LastError}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) running(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tasks": s.C.RunningTasks(r.Context()), "time": time.Now().Unix()})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	evs, err := s.C.St.Events(r.Context(), q.Get("severity"), before, 100)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": evs, "more": len(evs) == 100})
}

func (s *Server) system(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.C.Version, "hostname": host, "ips": core.LocalIPs(), "ssh_key": hostexec.PublicKey(s.C.Signer),
		"local_pbs": core.HasLocalPBS(), "user": currentUser(r).Username, "time": time.Now().Unix(),
	})
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	ns := s.C.NotifySettings()
	hasPass, hasToken, hasTG := ns.SMTPPass != "", ns.HookToken != "", ns.TelegramToken != ""
	ns.SMTPPass, ns.HookToken, ns.TelegramToken = "", "", ""
	writeJSON(w, http.StatusOK, map[string]any{"notify": ns, "has_smtp_password": hasPass, "has_hook_token": hasToken, "has_telegram_token": hasTG,
		"access": s.C.Access(), "seen_proxy": s.C.SeenProxy(), "public_url": s.C.PublicURL(), "client_ip": s.clientIP(r), "remote_ip": remoteIP(r)})
}

func (s *Server) putAccess(w http.ResponseWriter, r *http.Request) {
	var req core.Access
	if !decode(w, r, &req) {
		return
	}
	errs := map[string]string{}
	req.PublicURL = strings.TrimRight(strings.TrimSpace(req.PublicURL), "/")
	if req.PublicURL != "" {
		u, err := url.Parse(req.PublicURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || (u.Path != "" && u.Path != "/") {
			errs["public_url"] = "Vollständige Adresse wie https://backup.example.org"
		}
	}
	var proxies []string
	for _, p := range req.TrustedProxies {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if net.ParseIP(p) == nil {
			if _, _, err := net.ParseCIDR(p); err != nil {
				errs["trusted_proxies"] = p + " ist keine IP-Adresse."
			}
		}
		proxies = append(proxies, p)
	}
	req.TrustedProxies = proxies
	if fieldErrs(w, errs) {
		return
	}
	if err := s.C.St.SetSetting("access", req); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) readNotify(w http.ResponseWriter, r *http.Request) (notify.Settings, bool) {
	var req struct {
		Notify notify.Settings `json:"notify"`
	}
	if !decode(w, r, &req) {
		return req.Notify, false
	}
	n := req.Notify
	cur := s.C.NotifySettings()
	if n.SMTPPass == "" {
		n.SMTPPass = cur.SMTPPass
	}
	if n.HookToken == "" {
		n.HookToken = cur.HookToken
	}
	n.TelegramToken, n.TelegramChat = strings.TrimSpace(n.TelegramToken), strings.TrimSpace(n.TelegramChat)
	if n.TelegramToken == "" {
		n.TelegramToken = cur.TelegramToken
	}
	errs := map[string]string{}
	if n.MinSeverity != "info" && n.MinSeverity != "warning" && n.MinSeverity != "critical" {
		n.MinSeverity = "warning"
	}
	if n.SMTPPort == 0 {
		n.SMTPPort = 587
	}
	if n.MailEnabled {
		if n.SMTPHost == "" || !hostRe.MatchString(n.SMTPHost) {
			errs["smtp_host"] = "SMTP-Server angeben."
		}
		if !strings.Contains(n.MailFrom, "@") {
			errs["mail_from"] = "Absenderadresse angeben."
		}
		if !strings.Contains(n.MailTo, "@") {
			errs["mail_to"] = "Mindestens einen Empfänger angeben."
		}
	}
	if n.HookEnabled && !strings.HasPrefix(n.HookURL, "http://") && !strings.HasPrefix(n.HookURL, "https://") {
		errs["hook_url"] = "URL mit http:// oder https:// angeben."
	}
	if n.TelegramEnabled {
		if !notify.ValidTelegramToken(n.TelegramToken) {
			errs["telegram_token"] = "Bot-Token von @BotFather angeben, z. B. 123456789:AA…"
		}
		if !notify.ValidTelegramChat(n.TelegramChat) {
			errs["telegram_chat"] = "Chat-ID angeben oder mit „Chat-ID ermitteln“ auslesen."
		}
	}
	if fieldErrs(w, errs) {
		return n, false
	}
	return n, true
}

// telegramChats reads the chats that wrote to the bot, to pick the chat id.
func (s *Server) telegramChats(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &req) {
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		token = s.C.NotifySettings().TelegramToken
	}
	if !notify.ValidTelegramToken(token) {
		fieldErrs(w, map[string]string{"telegram_token": "Zuerst den Bot-Token von @BotFather eintragen."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	chats, err := notify.TelegramChats(ctx, token)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": chats})
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	n, good := s.readNotify(w, r)
	if !good {
		return
	}
	if err := s.C.St.SetSetting("notify", n); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) testNotify(w http.ResponseWriter, r *http.Request) {
	n, good := s.readNotify(w, r)
	if !good {
		return
	}
	if !n.MailEnabled && !n.HookEnabled && !n.TelegramEnabled {
		writeErr(w, http.StatusBadRequest, "Keinen Weg eingeschaltet.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	status := r.URL.Query().Get("status")
	if err := notify.Send(ctx, n, s.sampleMessage(status)); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	ok(w)
}

// sampleMessage is a backup report with made-up guests: for the test mail and the preview.
func (s *Server) sampleMessage(status string) notify.Message {
	now := time.Now()
	rows := []notify.Row{
		{Kind: "VM", ID: "100", Name: "dc01", Status: "ok", Duration: "1 Min. 5 s", Size: "32.00 GiB"},
		{Kind: "VM", ID: "101", Name: "fileserver", Status: "ok", Duration: "6 Min. 41 s", Size: "420.00 GiB"},
		{Kind: "CT", ID: "110", Name: "pihole", Status: "ok", Duration: "21 s", Size: "4.00 GiB"},
	}
	sev := "info"
	title := "Testnachricht: Datensicherung erfolgreich – alle 3 Server wurden gesichert"
	body := "Die Datensicherung war erfolgreich: alle 3 Server wurden gesichert. Sie müssen nichts tun."
	d := &notify.Details{Status: "ok", RowsTitle: "Gesicherte Server", Link: "/sicherungen"}
	forward := " Bitte leiten Sie diese Nachricht an Ihren IT-Betreuer weiter."
	switch status {
	case "warning":
		rows = append([]notify.Row{{Kind: "VM", ID: "112", Name: "test-win11", Status: "error", Info: "unable to find configuration file for VM 112"}}, rows...)
		sev, d.Status = "warning", "warning"
		title = "Testnachricht: Datensicherung unvollständig – 3 von 4 Servern gesichert"
		body = "3 von 4 Servern wurden gesichert. Nicht gesichert: test-win11 (112)." + forward
		d.Log = []string{"ERROR: Backup of VM 112 failed - unable to find configuration file for VM 112", "INFO: Backup job finished with errors"}
	case "error":
		rows = []notify.Row{{Kind: "VM", ID: "100", Name: "dc01", Status: "error", Info: "backup connect failed: No route to host"},
			{Kind: "VM", ID: "101", Name: "fileserver", Status: "error", Info: "backup connect failed: No route to host"}}
		sev, d.Status = "critical", "error"
		title = "Testnachricht: Datensicherung fehlgeschlagen"
		body = "Die Datensicherung hat nicht funktioniert, keiner der 2 Server wurde gesichert." + forward
		d.Log = []string{"ERROR: Backup of VM 100 failed - backup connect failed: command error: error trying to connect: tcp connect error: No route to host", "TASK ERROR: job errors"}
	case "restoretest":
		return s.sampleRestoreTest()
	case "usb":
		rows = []notify.Row{{Kind: "VM", ID: "100", Name: "dc01", Status: "ok", Time: now.Add(-6 * time.Hour).Unix(), Size: "32.0 GiB"},
			{Kind: "VM", ID: "101", Name: "fileserver", Status: "ok", Time: now.Add(-6 * time.Hour).Unix(), Size: "420 GiB"},
			{Kind: "CT", ID: "110", Name: "pihole", Status: "ok", Time: now.Add(-6 * time.Hour).Unix(), Size: "4.0 GiB"}}
		d = &notify.Details{Status: "ok", RowsTitle: "Diese Server sind auf der Festplatte gesichert", Link: "/wechselplatten", Rows: rows,
			Facts: []notify.Fact{{Label: "Festplatte", Value: "„USB1“"}, {Label: "Beginn", Value: now.Add(-89 * time.Minute).Format("02.01.2006 um 15:04 Uhr")},
				{Label: "Fertig", Value: now.Format("02.01.2006 um 15:04 Uhr")}, {Label: "Dauer", Value: "1 Std. 29 Min. (davon Kopie 1 Std. 27 Min.)"},
				{Label: "Kopiert", Value: "487,49 GiB mit durchschnittlich 96 MiB/s"}, {Label: "Enthält", Value: "3 Server"}, {Label: "Festplatte belegt", Value: "24 % (456 GiB von 1,8 TiB)"}}}
		return s.C.Message("info", "USB-Sicherung", "Testnachricht: USB-Sicherung fertig – Festplatte „USB1“ kann abgezogen werden",
			"Alle Daten wurden erfolgreich auf die Festplatte „USB1“ gesichert. Sie können die Festplatte jetzt abziehen und an einem sicheren Ort aufbewahren, am besten außer Haus.", d)
	}
	d.Rows = rows
	d.Facts = []notify.Fact{{Label: "System", Value: "PVE (Beispiel)"}, {Label: "Beginn", Value: now.Add(-9 * time.Minute).Format("02.01.2006 um 15:04 Uhr")},
		{Label: "Fertig", Value: now.Format("02.01.2006 um 15:04 Uhr")}, {Label: "Dauer", Value: "8 Min. 7 s"}, {Label: "Datenmenge", Value: "456 GiB"},
		{Label: "Gesichert nach", Value: "pbs-backup"}}
	return s.C.Message(sev, "Datensicherung", title, body, d)
}

func (s *Server) previewMail(w http.ResponseWriter, r *http.Request) {
	m := s.sampleMessage(r.URL.Query().Get("status"))
	if !s.C.NotifySettings().MailLinks {
		m.URL = "" // as the recipients will see it
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:")
	_, _ = w.Write([]byte(notify.HTMLPreview(m)))
}

// sampleScreen draws a made-up login screen for the restore test example.
func sampleScreen(label string, bg color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 800, 500))
	for y := 0; y < 500; y++ {
		for x := 0; x < 800; x++ {
			c := bg
			if x > 250 && x < 550 && y > 170 && y < 330 {
				c = color.RGBA{240, 240, 240, 255}
			}
			if x > 280 && x < 520 && y > 250 && y < 270 {
				c = color.RGBA{180, 180, 180, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	_ = label
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 75})
	return b.Bytes()
}

func (s *Server) sampleRestoreTest() notify.Message {
	now := time.Now()
	shots := map[string][]byte{"vm-100.png": sampleScreen("dc01", color.RGBA{0, 90, 158, 255}), "vm-101.png": sampleScreen("fileserver", color.RGBA{40, 40, 48, 255})}
	run := core.RestoreTestRun{ID: now.Format("20060102-150405"), PlanName: "Wöchentlicher Test (Beispiel)", By: "Zeitplan", Status: "ok",
		Start: now.Add(-14 * time.Minute).Unix(), End: now.Unix(), Cluster: "PVE (Beispiel)", Node: "pve1", Storage: "local-zfs", Source: "PBS / backup",
		Results: []core.RestoreTestResult{
			{Type: "vm", VMID: "100", Name: "dc01", Status: "ok", TestID: 990000, BackupTime: now.Add(-9 * time.Hour).Unix(), Started: now.Add(-14 * time.Minute).Unix(),
				RestoreEnd: now.Add(-11 * time.Minute).Unix(), Finished: now.Add(-8 * time.Minute).Unix(), Restore: 182, Boot: 64, DiskBytes: 64 << 30,
				OS: "Windows Server 2022 Standard", Hostname: "DC01", Info: "Betriebssystem antwortet", Notes: "Netzwerk getrennt", Screenshot: "vm-100.png"},
			{Type: "vm", VMID: "101", Name: "fileserver", Status: "ok", TestID: 990001, BackupTime: now.Add(-9 * time.Hour).Unix(), Started: now.Add(-8 * time.Minute).Unix(),
				RestoreEnd: now.Add(-4 * time.Minute).Unix(), Finished: now.Add(-2 * time.Minute).Unix(), Restore: 236, Boot: 41, DiskBytes: 420 << 30,
				OS: "Debian GNU/Linux 13 (trixie)", Hostname: "fileserver", Info: "Betriebssystem antwortet", Notes: "Netzwerk getrennt; hostpci0 entfernt", Screenshot: "vm-101.png"},
			{Type: "ct", VMID: "110", Name: "pihole", Status: "ok", TestID: 990002, BackupTime: now.Add(-9 * time.Hour).Unix(), Started: now.Add(-2 * time.Minute).Unix(),
				RestoreEnd: now.Add(-110 * time.Second).Unix(), Finished: now.Unix(), Restore: 10, Boot: 95, DiskBytes: 8 << 30, Info: "Container läuft", Notes: "Netzwerk getrennt"},
		}}
	d := &notify.Details{Status: "ok", RowsTitle: "Getestete Server",
		Facts: []notify.Fact{{Label: "System", Value: run.Cluster}, {Label: "Getestet auf", Value: "pve1 / local-zfs"}, {Label: "Sicherungen aus", Value: run.Source},
			{Label: "Beginn", Value: time.Unix(run.Start, 0).Format("02.01.2006 um 15:04 Uhr")}, {Label: "Dauer", Value: "14 Min. 0 s"}},
		Note: "Der vollständige Bericht mit allen Zeiten und Bildern hängt als Datei an (im Browser öffnen, drucken oder ablegen)."}
	var files []notify.Attachment
	for _, r := range run.Results {
		kind := map[string]string{"vm": "VM", "ct": "CT"}[r.Type]
		info := r.Info
		if r.OS != "" {
			info += " (" + r.OS + ")"
		}
		d.Rows = append(d.Rows, notify.Row{Kind: kind, ID: r.VMID, Name: r.Name, Status: "ok", Size: humanBytes(r.DiskBytes), Info: info,
			Duration: fmt.Sprintf("Restore %s · Start %s", secs(r.Restore), secs(r.Boot))})
		if b := shots[r.Screenshot]; b != nil {
			cid := "shot-" + r.VMID + "@pk95"
			files = append(files, notify.Attachment{Name: r.Type + "-" + r.VMID + ".jpg", ContentType: "image/jpeg", Data: b, ContentID: cid})
			d.Images = append(d.Images, notify.Image{CID: cid, Caption: r.VMID + " " + r.Name + " – " + r.Info, Status: "ok"})
		}
	}
	report, _ := s.renderReport(run, func(f string) (string, []byte) { return "image/jpeg", shots[f] }, false)
	files = append(files, notify.Attachment{Name: "Restore-Test-" + run.ID + ".html", ContentType: "text/html; charset=utf-8", Data: report})
	m := s.C.Message("info", "Wiederherstellungstest", "Testnachricht: Wiederherstellungstest erfolgreich – alle 3 Server lassen sich aus der Sicherung starten",
		"Alle 3 Server wurden testweise aus der Sicherung wiederhergestellt und gestartet. Ihre Sicherungen sind einsatzbereit. Sie müssen nichts tun.", d)
	m.Attachments = files
	return m
}

// ---- shares ----

func (s *Server) listShares(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"shares": s.C.ShareStates(r.Context())})
}

func (s *Server) readShare(w http.ResponseWriter, r *http.Request, id string) (shares.Share, core.PBSServer, bool) {
	var req shares.Share
	if !decode(w, r, &req) {
		return req, core.PBSServer{}, false
	}
	if id != "" {
		var cur shares.Share
		if err := s.C.St.Get(core.KindShare, id, &cur); err != nil {
			fail(w, err)
			return req, core.PBSServer{}, false
		}
		req.ID, req.Store, req.ServerID = cur.ID, cur.Store, cur.ServerID
		if req.Password == "" {
			req.Password = cur.Password
		}
	}
	errs := req.Validate()
	srv, err := s.C.Server(req.ServerID)
	if err != nil {
		errs["server_id"] = "Backup-Server wählen."
	} else if !srv.Local && !srv.SSHEnabled {
		errs["server_id"] = "Für diesen Server ist kein SSH-Zugang eingerichtet (Verbindungen → Server bearbeiten)."
	}
	if id == "" {
		var x shares.Share
		if s.C.St.Get(core.KindShare, req.ID, &x) == nil {
			errs["id"] = "Diese Kennung ist schon vergeben."
		}
	}
	if fieldErrs(w, errs) {
		return req, srv, false
	}
	return req, srv, true
}

func (s *Server) saveShare(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sh, srv, good := s.readShare(w, r, id)
	if !good {
		return
	}
	run, err := s.C.Runner(srv)
	if err != nil {
		fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Minute)
	defer cancel()
	if err := shares.Install(ctx, run, &sh); err != nil {
		// Keep the definition so it can be fixed; the unit is in place.
		_ = s.C.St.Put(core.KindShare, sh.ID, sh)
		writeErr(w, http.StatusBadGateway, "Gespeichert, aber: "+err.Error())
		return
	}
	if err := s.C.St.Put(core.KindShare, sh.ID, sh); err != nil {
		fail(w, err)
		return
	}
	verb := "eingerichtet"
	if id != "" {
		verb = "geändert"
	}
	s.C.Event("info", "Freigaben", "Freigabe "+sh.Name+" "+verb, sh.Source+" → "+srv.Name+":"+sh.MountPoint())
	s.C.RequestRefresh()
	ok(w)
}

func (s *Server) testShare(w http.ResponseWriter, r *http.Request) {
	sh, srv, good := s.readShare(w, r, "")
	if !good {
		// readShare rejects taken ids; a test of an existing share is fine.
		return
	}
	if of := r.URL.Query().Get("of"); of != "" && sh.Password == "" {
		var cur shares.Share
		if s.C.St.Get(core.KindShare, of, &cur) == nil {
			sh.Password = cur.Password
		}
	}
	run, err := s.C.Runner(srv)
	if err != nil {
		fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if err := shares.Test(ctx, run, &sh); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	ok(w)
}

func (s *Server) deleteShare(w http.ResponseWriter, r *http.Request) {
	var sh shares.Share
	if err := s.C.St.Get(core.KindShare, r.PathValue("id"), &sh); err != nil {
		fail(w, err)
		return
	}
	if sh.Store != "" {
		writeErr(w, http.StatusConflict, "Auf der Freigabe liegt der Datastore "+sh.Store+". Zuerst den Datastore entfernen.")
		return
	}
	if srv, err := s.C.Server(sh.ServerID); err == nil {
		if run, err := s.C.Runner(srv); err == nil {
			if err := shares.Remove(r.Context(), run, &sh); err != nil {
				fail(w, err)
				return
			}
		}
	}
	_ = s.C.St.Delete(core.KindShare, sh.ID)
	s.C.Event("info", "Freigaben", "Freigabe "+sh.Name+" entfernt", "Die Daten auf der Freigabe bleiben erhalten.")
	s.C.RequestRefresh()
	ok(w)
}

func (s *Server) shareAction(w http.ResponseWriter, r *http.Request) {
	var sh shares.Share
	if err := s.C.St.Get(core.KindShare, r.PathValue("id"), &sh); err != nil {
		fail(w, err)
		return
	}
	srv, err := s.C.Server(sh.ServerID)
	if err != nil {
		fail(w, err)
		return
	}
	run, err := s.C.Runner(srv)
	if err != nil {
		fail(w, err)
		return
	}
	switch r.PathValue("action") {
	case "mount":
		err = shares.Mount(r.Context(), run, &sh, true)
	case "unmount":
		if sh.Store != "" {
			pbs := s.C.PBSClient(srv)
			// Keep PBS from writing while the share is away.
			_ = pbs.UpdateDatastore(r.Context(), sh.Store, prox.Params{"maintenance-mode": "offline"})
		}
		err = shares.Mount(r.Context(), run, &sh, false)
	default:
		writeErr(w, http.StatusNotFound, "Unbekannte Aktion.")
		return
	}
	if err == nil && r.PathValue("action") == "mount" && sh.Store != "" {
		_ = s.C.PBSClient(srv).UpdateDatastore(r.Context(), sh.Store, prox.Params{"delete": "maintenance-mode"})
	}
	if err != nil {
		fail(w, err)
		return
	}
	s.C.RequestRefresh()
	ok(w)
}

// ---- rotation ----

func (s *Server) listRotations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"rotations": s.C.RotationStates(r.Context())})
}

func (s *Server) saveRotation(w http.ResponseWriter, r *http.Request) {
	var req core.Rotation
	if !decode(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	if id != "" {
		cur, err := s.C.Rotation(id)
		if err != nil {
			fail(w, err)
			return
		}
		req.ID, req.Disks, req.CheckPlan = cur.ID, cur.Disks, cur.CheckPlan
		if req.ServerID != cur.ServerID && len(cur.Disks) > 0 {
			fieldErrs(w, map[string]string{"server_id": "Der Server lässt sich nur ändern, solange keine Platte im Satz ist."})
			return
		}
	} else {
		req.ID = core.NewID(3)
		req.Disks = []core.RotDisk{}
	}
	errs := req.Validate()
	if _, err := s.C.Server(req.ServerID); err != nil && errs["server_id"] == "" {
		errs["server_id"] = "Backup-Server wählen."
	}
	if fieldErrs(w, errs) {
		return
	}
	if err := s.C.ApplyRotation(r.Context(), req); err != nil {
		fail(w, err)
		return
	}
	if err := s.C.St.Put(core.KindRotation, req.ID, req); err != nil {
		fail(w, err)
		return
	}
	s.C.RequestRefresh()
	writeJSON(w, http.StatusOK, map[string]any{"id": req.ID})
}

func (s *Server) saveRotationPlan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CheckPlan bool `json:"check_plan"`
		Disks     []struct {
			Key   string `json:"key"`
			Label string `json:"label"`
			Days  []int  `json:"days"`
			Week  string `json:"week"`
		} `json:"disks"`
	}
	if !decode(w, r, &req) {
		return
	}
	plan := map[string]core.RotDisk{}
	for _, d := range req.Disks {
		if len([]rune(strings.TrimSpace(d.Label))) > 40 {
			fieldErrs(w, map[string]string{"plan": "Namen höchstens 40 Zeichen."})
			return
		}
		plan[d.Key] = core.RotDisk{Label: d.Label, Days: d.Days, Week: d.Week}
	}
	_, msg, err := s.C.SetPlan(r.PathValue("id"), req.CheckPlan, plan)
	if err != nil {
		fail(w, err)
		return
	}
	if msg != "" {
		fieldErrs(w, map[string]string{"plan": msg})
		return
	}
	ok(w)
}

func (s *Server) deleteRotation(w http.ResponseWriter, r *http.Request) {
	rot, err := s.C.Rotation(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	for _, d := range rot.Disks {
		if err := s.C.RemoveRotationDisk(r.Context(), rot, d); err != nil {
			fail(w, err)
			return
		}
	}
	_ = s.C.St.Delete(core.KindRotation, rot.ID)
	s.C.Event("warning", "Wechselplatten", "Satz „"+rot.Name+"“ entfernt", "Die Datastores auf den Platten bleiben erhalten.")
	s.C.RequestRefresh()
	ok(w)
}

func (s *Server) addRotationDisk(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Label   string `json:"label"`
		Store   string `json:"store"` // existing removable datastore …
		Disk    string `json:"disk"`  // … or a disk to prepare
		FS      string `json:"fs"`
		Confirm string `json:"confirm"` // disk name typed again
	}
	if !decode(w, r, &req) {
		return
	}
	rot, err := s.C.Rotation(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	errs := map[string]string{}
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		errs["label"] = "Bezeichnung angeben, z. B. „Montag“ oder „Woche A“ – sie steht am besten auch auf der Platte."
	}
	if !storeRe.MatchString(req.Store) {
		errs["store"] = "Datastore-Name: 3–32 Zeichen, Buchstaben, Ziffern, - und _."
	}
	for _, d := range rot.Disks {
		if d.Store == req.Store {
			errs["store"] = "Diese Platte ist schon im Satz."
		}
		if strings.EqualFold(d.Label, req.Label) {
			errs["label"] = "Diese Bezeichnung gibt es schon."
		}
	}
	if req.Disk != "" && req.Confirm != req.Disk {
		errs["confirm"] = "Zur Bestätigung den Namen der Platte (" + req.Disk + ") eintippen."
	}
	if fieldErrs(w, errs) {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Minute)
	defer cancel()
	pbs, _, err := s.C.PBS(rot.ServerID)
	if err != nil {
		fail(w, err)
		return
	}
	if req.Disk != "" {
		if err := s.C.PrepareDisk(ctx, rot.ServerID, req.Disk, req.Store, req.FS, true); err != nil {
			fail(w, err)
			return
		}
	} else {
		cfgs, err := pbs.Datastores(ctx)
		if err != nil {
			fail(w, err)
			return
		}
		found := false
		for _, c := range cfgs {
			if c.Name == req.Store {
				found = true
				if c.BackingDevice == "" {
					fieldErrs(w, map[string]string{"store": "Das ist kein Wechsel-Datastore (removable)."})
					return
				}
			}
		}
		if !found {
			fieldErrs(w, map[string]string{"store": "Datastore nicht gefunden."})
			return
		}
	}
	d := core.RotDisk{Key: core.NewID(2), Store: req.Store, Label: req.Label, Added: time.Now().Unix()}
	rot.Disks = append(rot.Disks, d)
	if err := s.C.ApplyRotation(ctx, rot); err != nil {
		fail(w, err)
		return
	}
	if err := s.C.St.Put(core.KindRotation, rot.ID, rot); err != nil {
		fail(w, err)
		return
	}
	s.C.Event("info", "Wechselplatten", "Platte "+d.Label+" zu „"+rot.Name+"“ hinzugefügt", "Datastore "+d.Store)
	s.C.RequestRefresh()
	ok(w)
}

func (s *Server) findDisk(w http.ResponseWriter, r *http.Request) (core.Rotation, core.RotDisk, bool) {
	rot, err := s.C.Rotation(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return rot, core.RotDisk{}, false
	}
	for _, d := range rot.Disks {
		if d.Key == r.PathValue("key") {
			return rot, d, true
		}
	}
	writeErr(w, http.StatusNotFound, "Platte nicht im Satz.")
	return rot, core.RotDisk{}, false
}

func (s *Server) removeRotationDisk(w http.ResponseWriter, r *http.Request) {
	rot, d, good := s.findDisk(w, r)
	if !good {
		return
	}
	if err := s.C.RemoveRotationDisk(r.Context(), rot, d); err != nil {
		fail(w, err)
		return
	}
	keep := rot.Disks[:0]
	for _, x := range rot.Disks {
		if x.Key != d.Key {
			keep = append(keep, x)
		}
	}
	rot.Disks = keep
	_ = s.C.St.Put(core.KindRotation, rot.ID, rot)
	s.C.Event("info", "Wechselplatten", "Platte "+d.Label+" aus „"+rot.Name+"“ entfernt", "Der Datastore "+d.Store+" und die Daten auf der Platte bleiben erhalten.")
	s.C.RequestRefresh()
	ok(w)
}

func (s *Server) rotationDiskAction(w http.ResponseWriter, r *http.Request) {
	rot, d, good := s.findDisk(w, r)
	if !good {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()
	var err error
	switch r.PathValue("action") {
	case "run":
		err = s.C.RunRotationDisk(ctx, rot, d)
	case "eject":
		err = s.C.EjectRotationDisk(ctx, rot, d)
	case "rebuild":
		// Formatting and creating the datastore can take a while: in the background.
		go func() {
			bctx, bcancel := context.WithTimeout(context.Background(), time.Hour)
			defer bcancel()
			if err := s.C.RebuildDisk(bctx, rot, d, false); err != nil {
				s.C.EventDetails("critical", "USB-Sicherung", "Festplatte „"+d.Label+"“ konnte nicht neu aufgesetzt werden", err.Error(), nil)
			}
		}()
	default:
		writeErr(w, http.StatusNotFound, "Unbekannte Aktion.")
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

// ---- USB passthrough to this VM ----

var smbiosUUIDRe = regexp.MustCompile(`uuid=([0-9a-fA-F-]{36})`)

type selfVM struct {
	Cluster     string   `json:"cluster"`
	ClusterName string   `json:"cluster_name"`
	Node        string   `json:"node"`
	VMID        int      `json:"vmid"`
	Name        string   `json:"name"`
	USB         []string `json:"usb"` // current usbN entries "usb0=host=2-1"
}

// findSelf looks for the VM whose SMBIOS UUID matches this machine.
func (s *Server) findSelf(ctx context.Context) (*selfVM, error) {
	uuid := ""
	if s.C.SelfUUID != nil {
		uuid = s.C.SelfUUID()
	}
	if uuid == "" {
		return nil, fmt.Errorf("die SMBIOS-UUID dieser Maschine ist nicht lesbar – läuft die Appliance in einer Proxmox-VM?")
	}
	clusters, _ := s.C.Clusters()
	if len(clusters) == 0 {
		return nil, fmt.Errorf("zuerst unter Verbindungen den Proxmox VE eintragen, auf dem diese VM läuft")
	}
	for _, cl := range clusters {
		pve := s.C.PVEClient(cl)
		guests, err := pve.Guests(ctx)
		if err != nil {
			continue
		}
		for _, g := range guests {
			if g.Type != "qemu" {
				continue
			}
			cfg, err := pve.VMConfig(ctx, g.Node, g.VMID)
			if err != nil {
				continue
			}
			sm, _ := cfg["smbios1"].(string)
			m := smbiosUUIDRe.FindStringSubmatch(sm)
			if m == nil || !strings.EqualFold(m[1], uuid) {
				continue
			}
			v := &selfVM{Cluster: cl.ID, ClusterName: cl.Name, Node: g.Node, VMID: g.VMID, Name: g.Name, USB: []string{}}
			for k, val := range cfg {
				if strings.HasPrefix(k, "usb") {
					v.USB = append(v.USB, k+"="+fmt.Sprint(val))
				}
			}
			sort.Strings(v.USB)
			return v, nil
		}
	}
	return nil, fmt.Errorf("diese VM wurde in keinem eingetragenen Proxmox VE gefunden")
}

func (s *Server) passthroughInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	self, err := s.findSelf(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error()})
		return
	}
	pve, _, err := s.C.PVE(self.Cluster)
	if err != nil {
		fail(w, err)
		return
	}
	devs, err := pve.USBDevices(ctx, self.Node)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"self": self, "error": "USB-Geräte des Hosts nicht lesbar: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"self": self, "devices": devs})
}

func (s *Server) passthrough(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Port   string `json:"port"`   // "2-1.3" (bus-port) for port passthrough
		Remove string `json:"remove"` // "usb1"
	}
	if !decode(w, r, &req) {
		return
	}
	self, err := s.findSelf(r.Context())
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	pve, cl, err := s.C.PVE(self.Cluster)
	if err != nil {
		fail(w, err)
		return
	}
	if req.Remove != "" {
		if !regexp.MustCompile(`^usb\d{1,2}$`).MatchString(req.Remove) {
			writeErr(w, http.StatusBadRequest, "Ungültig.")
			return
		}
		if err := pve.SetVMConfig(r.Context(), self.Node, self.VMID, prox.Params{"delete": req.Remove}); err != nil {
			fail(w, err)
			return
		}
		ok(w)
		return
	}
	if !regexp.MustCompile(`^\d+-[\d.]+$`).MatchString(req.Port) {
		writeErr(w, http.StatusBadRequest, "Ungültiger USB-Anschluss.")
		return
	}
	used := map[string]bool{}
	for _, u := range self.USB {
		k, v, _ := strings.Cut(u, "=")
		used[k] = true
		if strings.Contains(v, "host="+req.Port) {
			writeErr(w, http.StatusConflict, "Dieser Anschluss ist schon durchgereicht ("+k+").")
			return
		}
	}
	slot := ""
	for i := 0; i < 14; i++ {
		if !used["usb"+strconv.Itoa(i)] {
			slot = "usb" + strconv.Itoa(i)
			break
		}
	}
	if slot == "" {
		writeErr(w, http.StatusConflict, "Alle USB-Plätze der VM sind belegt.")
		return
	}
	if err := pve.SetVMConfig(r.Context(), self.Node, self.VMID, prox.Params{slot: "host=" + req.Port + ",usb3=1"}); err != nil {
		fail(w, err)
		return
	}
	s.C.Event("info", cl.Name, "USB-Anschluss "+req.Port+" an die Appliance durchgereicht", fmt.Sprintf("VM %d (%s), %s. Jede Platte an diesem Anschluss erscheint in der Appliance.", self.VMID, self.Name, slot))
	ok(w)
}

var _ = store.ErrNotFound
