package api

import (
	"context"
	"fmt"
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
	writeJSON(w, http.StatusOK, map[string]any{"overview": ov, "events": evs})
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
	hasPass, hasToken := ns.SMTPPass != "", ns.HookToken != ""
	ns.SMTPPass, ns.HookToken = "", ""
	writeJSON(w, http.StatusOK, map[string]any{"notify": ns, "has_smtp_password": hasPass, "has_hook_token": hasToken,
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
	if fieldErrs(w, errs) {
		return n, false
	}
	return n, true
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
	if !n.MailEnabled && !n.HookEnabled {
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
		{Kind: "VM", ID: "100", Name: "dc01", Status: "ok", Duration: "00:01:05", Size: "32.00 GiB", Info: "inkrementell, 94 % unverändert"},
		{Kind: "VM", ID: "101", Name: "fileserver", Status: "ok", Duration: "00:06:41", Size: "420.00 GiB", Info: "inkrementell, 98 % unverändert"},
		{Kind: "CT", ID: "110", Name: "pihole", Status: "ok", Duration: "00:00:21", Size: "4.00 GiB"},
	}
	sev, title, body := "info", "Testnachricht: Sicherung erfolgreich: 3 Gäste", "3 von 3 VMs/Containern gesichert."
	d := &notify.Details{Status: "ok", RowsTitle: "Gesicherte VMs und Container", Link: "/sicherungen"}
	switch status {
	case "warning":
		rows = append([]notify.Row{{Kind: "VM", ID: "112", Name: "test-win11", Status: "error", Info: "unable to find configuration file for VM 112"}}, rows...)
		sev, d.Status, title, body = "warning", "warning", "Testnachricht: Sicherung mit Fehlern: 3 von 4 gesichert", "3 von 4 VMs/Containern gesichert."
		d.Log = []string{"ERROR: Backup of VM 112 failed - unable to find configuration file for VM 112", "INFO: Backup job finished with errors"}
	case "error":
		rows = []notify.Row{{Kind: "VM", ID: "100", Name: "dc01", Status: "error", Info: "backup connect failed: command error: error trying to connect: tcp connect error: No route to host"},
			{Kind: "VM", ID: "101", Name: "fileserver", Status: "error", Info: "backup connect failed: command error: error trying to connect: tcp connect error: No route to host"}}
		sev, d.Status, title, body = "critical", "error", "Testnachricht: Sicherung fehlgeschlagen: 2 Gäste", "0 von 2 VMs/Containern gesichert."
		d.Log = []string{"ERROR: Backup of VM 100 failed - backup connect failed: command error: error trying to connect: tcp connect error: No route to host", "TASK ERROR: job errors"}
	}
	d.Rows = rows
	d.Facts = []notify.Fact{{Label: "Proxmox VE", Value: "Cluster (Beispiel)"}, {Label: "Knoten", Value: "pve1, pve2"}, {Label: "Ziel", Value: "pbs-backup"},
		{Label: "Modus", Value: "Snapshot"}, {Label: "Beginn", Value: now.Add(-9 * time.Minute).Format("02.01.2006 15:04")}, {Label: "Dauer", Value: "8 Min. 7 s"}}
	return s.C.Message(sev, "Beispiel", title, body, d)
}

func (s *Server) previewMail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:")
	_, _ = w.Write([]byte(notify.HTML(s.sampleMessage(r.URL.Query().Get("status")))))
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
		req.ID, req.Disks = cur.ID, cur.Disks
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
