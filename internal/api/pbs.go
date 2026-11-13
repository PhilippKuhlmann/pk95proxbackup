package api

import (
	"context"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/hostexec"
	"pk95proxbackup/internal/prox"
	"pk95proxbackup/internal/shares"
	"pk95proxbackup/internal/store"
)

var (
	authIDRe = regexp.MustCompile(`^[^\s@!]+@[A-Za-z0-9._-]+![A-Za-z0-9._-]+$`)
	storeRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{2,31}$`)
	hostRe   = regexp.MustCompile(`^[A-Za-z0-9.\-:\[\]]+$`)
	pathRe   = regexp.MustCompile(`^/[A-Za-z0-9._/\-]+$`)
	schedRe  = regexp.MustCompile(`^[A-Za-z0-9 :*,./\-]*$`)
)

func (s *Server) probe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.Host = strings.TrimSpace(req.Host)
	if !hostRe.MatchString(req.Host) {
		fieldErrs(w, map[string]string{"host": "Adresse oder Hostname angeben."})
		return
	}
	fp, err := prox.FetchFingerprint(r.Context(), strings.Trim(req.Host, "[]"), req.Port)
	if err != nil {
		fieldErrs(w, map[string]string{"host": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fingerprint": fp})
}

func (s *Server) listPBS(w http.ResponseWriter, r *http.Request) {
	list, err := s.C.Servers()
	if err != nil {
		fail(w, err)
		return
	}
	for i := range list {
		list[i].Secret = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": list, "ssh_key": hostexec.PublicKey(s.C.Signer), "local_pbs": core.HasLocalPBS()})
}

func (s *Server) savePBS(w http.ResponseWriter, r *http.Request) {
	var req core.PBSServer
	if !decode(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	var cur core.PBSServer
	if id != "" {
		var err error
		if cur, err = s.C.Server(id); err != nil {
			fail(w, err)
			return
		}
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Host = strings.TrimSpace(req.Host)
	req.AuthID = strings.TrimSpace(req.AuthID)
	req.PublicHost = strings.TrimSpace(req.PublicHost)
	errs := map[string]string{}
	if req.Name == "" {
		errs["name"] = "Bitte einen Namen angeben."
	}
	if req.PublicHost != "" && !hostRe.MatchString(req.PublicHost) {
		errs["public_host"] = "Ungültige Adresse."
	}
	if cur.Local {
		// Only name and public address of the built-in server can change.
		if fieldErrs(w, errs) {
			return
		}
		cur.Name, cur.PublicHost = req.Name, req.PublicHost
		if err := s.C.St.Put(core.KindPBS, cur.ID, cur); err != nil {
			fail(w, err)
			return
		}
		s.C.RequestRefresh()
		ok(w)
		return
	}
	if !hostRe.MatchString(req.Host) {
		errs["host"] = "Adresse oder Hostname angeben."
	}
	if req.Port == 0 {
		req.Port = 8007
	}
	if !authIDRe.MatchString(req.AuthID) {
		errs["auth_id"] = "Form: benutzer@realm!tokenname, z. B. root@pam!pk95"
	}
	if req.Secret == "" {
		req.Secret = cur.Secret
	}
	if req.Secret == "" {
		errs["secret"] = "Secret des API-Tokens angeben."
	}
	if prox.NormalizeFingerprint(req.Fingerprint) == "" {
		errs["fingerprint"] = "Fingerabdruck abrufen oder eintragen."
	}
	if req.SSHEnabled && req.SSHHost != "" && !hostRe.MatchString(req.SSHHost) {
		errs["ssh_host"] = "Ungültige Adresse."
	}
	if fieldErrs(w, errs) {
		return
	}
	req.Fingerprint = prox.FormatFingerprint(req.Fingerprint)
	if req.SSHHost != cur.SSHHost || req.Host != cur.Host {
		req.SSHHostKey = ""
	} else {
		req.SSHHostKey = cur.SSHHostKey
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if _, err := s.C.PBSClient(req).Version(ctx); err != nil {
		fieldErrs(w, map[string]string{"secret": "Verbindung fehlgeschlagen: " + err.Error()})
		return
	}
	if _, err := s.C.PBSClient(req).Datastores(ctx); err != nil {
		fieldErrs(w, map[string]string{"auth_id": "Der Token darf die Datastores nicht lesen: " + err.Error() + ". Dem Token die Rolle Admin auf / geben."})
		return
	}
	if id == "" {
		req.ID = core.NewID(4)
	} else {
		req.ID = id
	}
	req.Local = false
	if err := s.C.St.Put(core.KindPBS, req.ID, req); err != nil {
		fail(w, err)
		return
	}
	if id == "" {
		s.C.Event("info", req.Name, "Backup-Server eingebunden", req.Host)
	}
	s.C.RequestRefresh()
	writeJSON(w, http.StatusOK, map[string]any{"id": req.ID})
}

func (s *Server) deletePBS(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sh, _ := s.C.Shares()
	for _, x := range sh {
		if x.ServerID == id {
			writeErr(w, http.StatusConflict, "Zuerst die Freigabe „"+x.Name+"“ dieses Servers entfernen.")
			return
		}
	}
	rots, _ := s.C.Rotations()
	for _, x := range rots {
		if x.ServerID == id || x.SourceServer == id {
			writeErr(w, http.StatusConflict, "Zuerst den Wechselplatten-Satz „"+x.Name+"“ entfernen.")
			return
		}
	}
	if err := s.C.St.Delete(core.KindPBS, id); err != nil {
		fail(w, err)
		return
	}
	s.C.RequestRefresh()
	ok(w)
}

func (s *Server) testSSH(w http.ResponseWriter, r *http.Request) {
	srv, err := s.C.Server(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	run, err := s.C.Runner(srv)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := run.Run(r.Context(), "proxmox-backup-manager", "versions")
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	missing := []string{}
	for _, tool := range []string{"mount.nfs", "mount.cifs"} {
		if _, err := run.Run(r.Context(), "sh", "-c", "command -v "+tool+" || test -x /sbin/"+tool); err != nil {
			missing = append(missing, tool)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"output": strings.TrimSpace(out), "missing": missing})
}

// ---- datastores ----

type datastoreView struct {
	prox.DatastoreConfig
	prox.DatastoreUsage
	Removable  bool      `json:"removable"`
	Share      string    `json:"share,omitempty"`
	Prune      *prox.Job `json:"prune,omitempty"`
	Verify     *prox.Job `json:"verify,omitempty"`
	Rotation   string    `json:"rotation,omitempty"`
	GCSchedule string    `json:"gc_schedule"`
}

func pruneJobID(store string) string  { return core.JobID("pk95-prune", store) }
func verifyJobID(store string) string { return core.JobID("pk95-verify", store) }

func (s *Server) listDatastores(w http.ResponseWriter, r *http.Request) {
	pbs, srv, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ctx := r.Context()
	cfgs, err := pbs.Datastores(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	usage := map[string]prox.DatastoreUsage{}
	if us, err := pbs.Usage(ctx); err == nil {
		for _, u := range us {
			usage[u.Store] = u
		}
	}
	prunes := map[string]prox.Job{}
	if js, err := pbs.Jobs(ctx, "prune"); err == nil {
		for _, j := range js {
			prunes[j.ID] = j
		}
	}
	verifies := map[string]prox.Job{}
	if js, err := pbs.Jobs(ctx, "verify"); err == nil {
		for _, j := range js {
			verifies[j.ID] = j
		}
	}
	shareBy := map[string]string{}
	if sh, err := s.C.Shares(); err == nil {
		for _, x := range sh {
			if x.ServerID == srv.ID && x.Store != "" {
				shareBy[x.Store] = x.Name
			}
		}
	}
	rotBy := map[string]string{}
	if rots, err := s.C.Rotations(); err == nil {
		for _, x := range rots {
			if x.ServerID == srv.ID {
				for _, d := range x.Disks {
					rotBy[d.Store] = x.Name + " · " + d.Label
				}
			}
		}
	}
	out := []datastoreView{}
	for _, c := range cfgs {
		v := datastoreView{DatastoreConfig: c, DatastoreUsage: usage[c.Name], Removable: c.BackingDevice != "", Share: shareBy[c.Name], Rotation: rotBy[c.Name], GCSchedule: c.GCSchedule}
		v.Store = c.Name
		if j, ok := prunes[pruneJobID(c.Name)]; ok {
			v.Prune = &j
		}
		if j, ok := verifies[verifyJobID(c.Name)]; ok {
			v.Verify = &j
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"datastores": out, "shell": srv.Local || srv.SSHEnabled})
}

type retention struct {
	KeepLast    int    `json:"keep_last"`
	KeepDaily   int    `json:"keep_daily"`
	KeepWeekly  int    `json:"keep_weekly"`
	KeepMonthly int    `json:"keep_monthly"`
	KeepYearly  int    `json:"keep_yearly"`
	PruneSched  string `json:"prune_schedule"`
	VerifySched string `json:"verify_schedule"`
	GCSched     string `json:"gc_schedule"`
	Comment     string `json:"comment"`
}

func (rt retention) any() bool {
	return rt.KeepLast+rt.KeepDaily+rt.KeepWeekly+rt.KeepMonthly+rt.KeepYearly > 0
}

func (rt retention) validate(errs map[string]string) {
	for k, v := range map[string]string{"prune_schedule": rt.PruneSched, "verify_schedule": rt.VerifySched, "gc_schedule": rt.GCSched} {
		if !schedRe.MatchString(v) {
			errs[k] = "Zeitplan wie in PBS, z. B. daily, sat 02:00 oder *-*-* 22:00."
		}
	}
	for _, v := range []int{rt.KeepLast, rt.KeepDaily, rt.KeepWeekly, rt.KeepMonthly, rt.KeepYearly} {
		if v < 0 || v > 10000 {
			errs["keep_last"] = "Werte von 0 bis 10000."
		}
	}
}

// applyRetention writes GC schedule, prune job and verify job of a datastore.
func (s *Server) applyRetention(ctx context.Context, pbs *prox.PBS, store string, rt retention) error {
	if err := pbs.UpdateDatastore(ctx, store, prox.Params{"gc-schedule": rt.GCSched, "comment": rt.Comment, "delete": deleteIfEmpty(map[string]string{"gc-schedule": rt.GCSched, "comment": rt.Comment})}); err != nil {
		return err
	}
	prunes, _ := pbs.Jobs(ctx, "prune")
	pid := pruneJobID(store)
	exists := false
	for _, j := range prunes {
		if j.ID == pid {
			exists = true
		}
	}
	if rt.any() {
		sched := rt.PruneSched
		if sched == "" {
			sched = "daily"
		}
		p := prox.Params{"store": store, "schedule": sched, "comment": "PK95 Aufbewahrung"}
		var del []string
		for k, v := range map[string]int{"keep-last": rt.KeepLast, "keep-daily": rt.KeepDaily, "keep-weekly": rt.KeepWeekly, "keep-monthly": rt.KeepMonthly, "keep-yearly": rt.KeepYearly} {
			if v > 0 {
				p[k] = v
			} else {
				del = append(del, k)
			}
		}
		if exists {
			if len(del) > 0 {
				p["delete"] = del
			}
			delete(p, "store")
			if err := pbs.UpdateJob(ctx, "prune", pid, p); err != nil {
				return err
			}
		} else {
			p["id"] = pid
			if err := pbs.CreateJob(ctx, "prune", p); err != nil {
				return err
			}
		}
	} else if exists {
		if err := pbs.DeleteJob(ctx, "prune", pid); err != nil {
			return err
		}
	}
	verifies, _ := pbs.Jobs(ctx, "verify")
	vid := verifyJobID(store)
	vexists := false
	for _, j := range verifies {
		if j.ID == vid {
			vexists = true
		}
	}
	switch {
	case rt.VerifySched != "" && vexists:
		return pbs.UpdateJob(ctx, "verify", vid, prox.Params{"schedule": rt.VerifySched})
	case rt.VerifySched != "":
		return pbs.CreateJob(ctx, "verify", prox.Params{"id": vid, "store": store, "schedule": rt.VerifySched, "ignore-verified": true, "outdated-after": 30, "comment": "PK95 Prüfung"})
	case vexists:
		return pbs.DeleteJob(ctx, "verify", vid)
	}
	return nil
}

func deleteIfEmpty(m map[string]string) any {
	var del []string
	for k, v := range m {
		if v == "" {
			del = append(del, k)
		}
	}
	if len(del) == 0 {
		return nil
	}
	sort.Strings(del)
	return del
}

func (s *Server) createDatastore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Kind    string `json:"kind"` // disk, share, path
		Disk    string `json:"disk"`
		FS      string `json:"fs"`
		ShareID string `json:"share_id"`
		Path    string `json:"path"`
		retention
	}
	if !decode(w, r, &req) {
		return
	}
	pbs, srv, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	errs := map[string]string{}
	if !storeRe.MatchString(req.Name) {
		errs["name"] = "3–32 Zeichen: Buchstaben, Ziffern, - und _ (Anfang Buchstabe/Ziffer)."
	}
	req.validate(errs)
	path := ""
	var share shares.Share
	switch req.Kind {
	case "disk":
		if req.Disk == "" {
			errs["disk"] = "Platte wählen."
		}
	case "share":
		if err := s.C.St.Get(core.KindShare, req.ShareID, &share); err != nil || share.ServerID != srv.ID {
			errs["share_id"] = "Freigabe dieses Servers wählen."
		} else if share.Store != "" {
			errs["share_id"] = "Auf dieser Freigabe liegt schon der Datastore " + share.Store + "."
		} else {
			path = share.MountPoint() + "/" + req.Name
		}
	case "path":
		if !pathRe.MatchString(req.Path) || strings.Contains(req.Path, "..") {
			errs["path"] = "Absoluter Pfad, z. B. /srv/backup/" + req.Name
		}
		path = req.Path
	default:
		errs["kind"] = "Art wählen."
	}
	if fieldErrs(w, errs) {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Minute)
	defer cancel()
	if req.Kind == "share" {
		run, err := s.C.Runner(srv)
		if err != nil {
			fail(w, err)
			return
		}
		if st := shares.Status(ctx, run, &share); !st.Mounted {
			writeErr(w, http.StatusConflict, "Die Freigabe „"+share.Name+"“ ist nicht eingehängt.")
			return
		}
	}
	if req.Kind == "disk" {
		err = s.C.PrepareDisk(ctx, srv.ID, req.Disk, req.Name, req.FS, false)
	} else {
		err = pbs.CreateDatastore(ctx, prox.Params{"name": req.Name, "path": path, "comment": req.Comment, "gc-schedule": req.GCSched})
	}
	if err != nil {
		fail(w, err)
		return
	}
	if req.Kind == "share" {
		share.Store = req.Name
		_ = s.C.St.Put(core.KindShare, share.ID, share)
	}
	if err := s.applyRetention(ctx, pbs, req.Name, req.retention); err != nil {
		writeErr(w, http.StatusBadGateway, "Datastore angelegt, aber Aufbewahrung nicht gespeichert: "+err.Error())
		return
	}
	s.C.Event("info", srv.Name, "Datastore "+req.Name+" angelegt", path)
	s.C.RequestRefresh()
	ok(w)
}

func (s *Server) updateDatastore(w http.ResponseWriter, r *http.Request) {
	var req retention
	if !decode(w, r, &req) {
		return
	}
	errs := map[string]string{}
	req.validate(errs)
	if fieldErrs(w, errs) {
		return
	}
	pbs, _, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.applyRetention(r.Context(), pbs, r.PathValue("store"), req); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) deleteDatastore(w http.ResponseWriter, r *http.Request) {
	pbs, srv, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	name := r.PathValue("store")
	rots, _ := s.C.Rotations()
	for _, x := range rots {
		for _, d := range x.Disks {
			if x.ServerID == srv.ID && d.Store == name {
				writeErr(w, http.StatusConflict, "Der Datastore gehört zum Wechselplatten-Satz „"+x.Name+"“. Zuerst dort die Platte entfernen.")
				return
			}
		}
	}
	destroy := r.URL.Query().Get("destroy") == "1"
	ctx := context.WithoutCancel(r.Context())
	for _, kind := range []string{"prune", "verify"} {
		id := pruneJobID(name)
		if kind == "verify" {
			id = verifyJobID(name)
		}
		_ = pbs.DeleteJob(ctx, kind, id)
	}
	if err := pbs.DeleteDatastore(ctx, name, destroy); err != nil {
		fail(w, err)
		return
	}
	if sh, err := s.C.Shares(); err == nil {
		for _, x := range sh {
			if x.ServerID == srv.ID && x.Store == name {
				x.Store = ""
				_ = s.C.St.Put(core.KindShare, x.ID, x)
			}
		}
	}
	msg := "Konfiguration entfernt, Daten bleiben erhalten."
	if destroy {
		msg = "Inklusive aller Sicherungen gelöscht."
	}
	s.C.Event("warning", srv.Name, "Datastore "+name+" entfernt", msg)
	s.C.RequestRefresh()
	ok(w)
}

func (s *Server) datastoreAction(w http.ResponseWriter, r *http.Request) {
	pbs, _, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	st := r.PathValue("store")
	var upid string
	switch r.PathValue("action") {
	case "gc":
		upid, err = pbs.GC(r.Context(), st)
	case "verify":
		upid, err = pbs.Verify(r.Context(), st)
	case "mount":
		upid, err = pbs.Mount(r.Context(), st)
	case "unmount":
		upid, err = pbs.Unmount(r.Context(), st)
	default:
		writeErr(w, http.StatusNotFound, "Unbekannte Aktion.")
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	s.C.RequestRefresh()
	writeJSON(w, http.StatusOK, map[string]any{"upid": upid})
}

func (s *Server) snapshots(w http.ResponseWriter, r *http.Request) {
	pbs, _, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	snaps, err := pbs.Snapshots(r.Context(), r.PathValue("store"), r.URL.Query().Get("ns"))
	if err != nil {
		fail(w, err)
		return
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].Time > snaps[j].Time })
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snaps})
}

func (s *Server) deleteSnapshot(w http.ResponseWriter, r *http.Request) {
	pbs, srv, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	q := r.URL.Query()
	t, _ := strconv.ParseInt(q.Get("time"), 10, 64)
	if err := pbs.DeleteSnapshot(r.Context(), r.PathValue("store"), q.Get("ns"), q.Get("type"), q.Get("bid"), t); err != nil {
		fail(w, err)
		return
	}
	s.C.Event("info", srv.Name, "Sicherung gelöscht", q.Get("type")+"/"+q.Get("bid")+" vom "+time.Unix(t, 0).Format("02.01.2006 15:04"))
	ok(w)
}

func (s *Server) disks(w http.ResponseWriter, r *http.Request) {
	pbs, _, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	disks, err := pbs.Disks(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"disks": disks})
}

func (s *Server) prepareDisk(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Disk    string `json:"disk"`
		Name    string `json:"name"`
		FS      string `json:"fs"`
		Confirm string `json:"confirm"`
	}
	if !decode(w, r, &req) {
		return
	}
	errs := map[string]string{}
	if !storeRe.MatchString(req.Name) {
		errs["name"] = "3–32 Zeichen: Buchstaben, Ziffern, - und _."
	}
	if req.Confirm != req.Disk || req.Disk == "" {
		errs["confirm"] = "Zur Bestätigung den Namen der Platte eintippen."
	}
	if fieldErrs(w, errs) {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Minute)
	defer cancel()
	if err := s.C.PrepareDisk(ctx, r.PathValue("id"), req.Disk, req.Name, req.FS, true); err != nil {
		fail(w, err)
		return
	}
	s.C.RequestRefresh()
	ok(w)
}

// ---- sync jobs ----

func (s *Server) listSync(w http.ResponseWriter, r *http.Request) {
	pbs, _, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	jobs, err := pbs.Jobs(r.Context(), "sync")
	if err != nil {
		fail(w, err)
		return
	}
	remotes, _ := pbs.Remotes(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "remotes": remotes})
}

func (s *Server) saveSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Store          string `json:"store"`
		SourceServer   string `json:"source_server"`
		SourceStore    string `json:"source_store"`
		Schedule       string `json:"schedule"`
		RemoveVanished bool   `json:"remove_vanished"`
		TransferLast   int    `json:"transfer_last"`
		Comment        string `json:"comment"`
		SourceHost     string `json:"source_host"`
	}
	if !decode(w, r, &req) {
		return
	}
	pbs, srv, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	errs := map[string]string{}
	if req.Store == "" {
		errs["store"] = "Ziel-Datastore wählen."
	}
	if req.SourceServer == "" || req.SourceStore == "" {
		errs["source_store"] = "Quelle wählen."
	}
	if req.SourceServer == srv.ID && req.SourceStore == req.Store {
		errs["source_store"] = "Quelle und Ziel sind gleich."
	}
	if !schedRe.MatchString(req.Schedule) {
		errs["schedule"] = "Zeitplan wie in PBS, z. B. daily oder *-*-* 23:00."
	}
	if req.TransferLast < 0 || req.TransferLast > 1000 {
		errs["transfer_last"] = "0 (alle) bis 1000."
	}
	if fieldErrs(w, errs) {
		return
	}
	remote, err := s.C.RemoteFor(r.Context(), pbs, srv, req.SourceServer, req.SourceStore, strings.TrimSpace(req.SourceHost))
	if err != nil {
		if strings.Contains(err.Error(), "Adresse der Quelle") {
			fieldErrs(w, map[string]string{"source_host": err.Error()})
			return
		}
		fail(w, err)
		return
	}
	p := prox.Params{"store": req.Store, "remote-store": req.SourceStore, "remove-vanished": req.RemoveVanished,
		"schedule": req.Schedule, "comment": req.Comment}
	if remote != "" {
		p["remote"] = remote
	}
	if req.TransferLast > 0 {
		p["transfer-last"] = req.TransferLast
	}
	job := r.PathValue("job")
	if job == "" {
		p["id"] = core.JobID("pk95-sync", core.NewID(3))
		err = pbs.CreateJob(r.Context(), "sync", p)
	} else {
		var del []string
		if remote == "" {
			del = append(del, "remote")
		}
		if req.TransferLast == 0 {
			del = append(del, "transfer-last")
		}
		if req.Schedule == "" {
			del = append(del, "schedule")
			delete(p, "schedule")
		}
		if req.Comment == "" {
			del = append(del, "comment")
			delete(p, "comment")
		}
		if len(del) > 0 {
			p["delete"] = del
		}
		err = pbs.UpdateJob(r.Context(), "sync", job, p)
	}
	if err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) deleteSync(w http.ResponseWriter, r *http.Request) {
	pbs, _, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if strings.HasPrefix(r.PathValue("job"), "pk95r-") {
		writeErr(w, http.StatusConflict, "Dieser Job gehört zu einem Wechselplatten-Satz und wird dort verwaltet.")
		return
	}
	if err := pbs.DeleteJob(r.Context(), "sync", r.PathValue("job")); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) runSync(w http.ResponseWriter, r *http.Request) {
	pbs, _, err := s.C.PBS(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if err := pbs.RunJob(r.Context(), "sync", r.PathValue("job")); err != nil {
		fail(w, err)
		return
	}
	s.C.RequestRefresh()
	ok(w)
}

// ---- tasks ----

func (s *Server) taskLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	src, upid := q.Get("source"), q.Get("upid")
	kind, id, _ := strings.Cut(src, ":")
	var lines []prox.TaskLine
	var st prox.TaskStatus
	var err error
	switch kind {
	case "pbs":
		var pbs *prox.PBS
		if pbs, _, err = s.C.PBS(id); err == nil {
			if lines, err = pbs.TaskLog(r.Context(), upid); err == nil {
				st, err = pbs.TaskStatus(r.Context(), upid)
			}
		}
	case "pve":
		var pve *prox.PVE
		if pve, _, err = s.C.PVE(id); err == nil {
			if lines, err = pve.TaskLog(r.Context(), upid); err == nil {
				st, err = pve.TaskStatus(r.Context(), upid)
			}
		}
	default:
		writeErr(w, http.StatusBadRequest, "Unbekannte Quelle.")
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "status": st})
}

var _ = store.ErrNotFound

// checkSyncSource lets the target connect to the source of a copy (network, certificate, login).
func (s *Server) checkSyncSource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SourceServer string `json:"source_server"`
		SourceStore  string `json:"source_store"`
		SourceHost   string `json:"source_host"`
	}
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	writeJSON(w, http.StatusOK, s.C.CheckRemote(ctx, r.PathValue("id"), req.SourceServer, req.SourceStore, strings.TrimSpace(req.SourceHost)))
}
