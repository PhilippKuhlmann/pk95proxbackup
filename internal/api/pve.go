package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/prox"
)

var (
	storageNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{1,39}$`)
	vmidListRe    = regexp.MustCompile(`^\d+(,\d+)*$`)
)

func (s *Server) listPVE(w http.ResponseWriter, r *http.Request) {
	list, err := s.C.Clusters()
	if err != nil {
		fail(w, err)
		return
	}
	for i := range list {
		list[i].Secret = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"clusters": list})
}

func (s *Server) savePVE(w http.ResponseWriter, r *http.Request) {
	var req core.PVECluster
	if !decode(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	var cur core.PVECluster
	if id != "" {
		var err error
		if cur, err = s.C.Cluster(id); err != nil {
			fail(w, err)
			return
		}
	}
	req.Name, req.Host, req.TokenID = strings.TrimSpace(req.Name), strings.TrimSpace(req.Host), strings.TrimSpace(req.TokenID)
	errs := map[string]string{}
	if req.Name == "" {
		errs["name"] = "Bitte einen Namen angeben."
	}
	if !hostRe.MatchString(req.Host) {
		errs["host"] = "Adresse oder Hostname eines Knotens angeben."
	}
	if req.Port == 0 {
		req.Port = 8006
	}
	if !authIDRe.MatchString(req.TokenID) {
		errs["token_id"] = "Form: benutzer@realm!tokenname, z. B. root@pam!pk95"
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
	if fieldErrs(w, errs) {
		return
	}
	req.Fingerprint = prox.FormatFingerprint(req.Fingerprint)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	pve := s.C.PVEClient(req)
	if _, err := pve.Version(ctx); err != nil {
		fieldErrs(w, map[string]string{"secret": "Verbindung fehlgeschlagen: " + err.Error()})
		return
	}
	if perms, err := pve.Permissions(ctx); err == nil {
		if missing := prox.MissingPrivs(perms); len(missing) > 0 {
			fieldErrs(w, map[string]string{"token_id": permHint(req.TokenID, missing)})
			return
		}
	}
	if id == "" {
		req.ID = core.NewID(4)
		s.C.Event("info", req.Name, "Proxmox VE eingebunden", req.Host)
	} else {
		req.ID = id
	}
	if err := s.C.St.Put(core.KindPVE, req.ID, req); err != nil {
		fail(w, err)
		return
	}
	s.C.RequestRefresh()
	writeJSON(w, http.StatusOK, map[string]any{"id": req.ID})
}

func (s *Server) deletePVE(w http.ResponseWriter, r *http.Request) {
	if err := s.C.St.Delete(core.KindPVE, r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	s.C.RequestRefresh()
	ok(w)
}

type storageView struct {
	prox.Storage
	PBSServer string `json:"pbs_server,omitempty"` // id of the matching Backup Server
	PBSName   string `json:"pbs_name,omitempty"`
}

// matchServer finds the Backup Server a PVE storage points to.
func (s *Server) matchServer(st prox.Storage) (core.PBSServer, bool) {
	servers, _ := s.C.Servers()
	for _, srv := range servers {
		if prox.NormalizeFingerprint(st.Fingerprint) != "" && prox.NormalizeFingerprint(st.Fingerprint) == prox.NormalizeFingerprint(srv.Fingerprint) {
			return srv, true
		}
		if st.Server != "" && (st.Server == srv.Host || st.Server == srv.PublicHost) && !srv.Local {
			return srv, true
		}
		if srv.Local && st.Server == srv.PublicHost && st.Server != "" {
			return srv, true
		}
	}
	return core.PBSServer{}, false
}

func (s *Server) pveStorages(w http.ResponseWriter, r *http.Request) {
	pve, _, err := s.C.PVE(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	list, err := pve.Storages(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out := []storageView{}
	for _, st := range list {
		v := storageView{Storage: st}
		if st.Type == "pbs" {
			if srv, ok := s.matchServer(st); ok {
				v.PBSServer, v.PBSName = srv.ID, srv.Name
			}
		}
		out = append(out, v)
	}
	nodes, _ := pve.Nodes(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"storages": out, "nodes": nodes})
}

func (s *Server) addPVEStorage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PBS       string `json:"pbs"`
		Store     string `json:"store"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	}
	if !decode(w, r, &req) {
		return
	}
	pve, cl, err := s.C.PVE(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	pbs, srv, err := s.C.PBS(req.PBS)
	errs := map[string]string{}
	if err != nil {
		errs["pbs"] = "Backup-Server wählen."
	}
	if req.Store == "" {
		errs["store"] = "Datastore wählen."
	}
	if !storageNameRe.MatchString(req.Name) {
		errs["name"] = "Name des Speichers in Proxmox VE: Buchstabe am Anfang, dann Buchstaben, Ziffern, - _ ."
	}
	if err == nil && srv.Address() == "" {
		errs["pbs"] = "Für diesen Server ist keine Adresse für Proxmox VE hinterlegt (Verbindungen → Server bearbeiten)."
	}
	if fieldErrs(w, errs) {
		return
	}
	ctx := r.Context()
	// A dedicated token per cluster that may only back up into (and restore from) this datastore.
	user := "pk95-pve@pbs"
	if err := pbs.EnsureUser(ctx, user, "PK95 ProxBackup: Zugang für Proxmox VE"); err != nil {
		fail(w, fmt.Errorf("Benutzer auf dem Backup-Server anlegen: %w", err))
		return
	}
	tokenID, secret, err := pbs.NewToken(ctx, user, "pve-"+cl.ID, "PK95 ProxBackup: "+cl.Name)
	if err != nil {
		fail(w, fmt.Errorf("Token anlegen: %w", err))
		return
	}
	for _, path := range []string{"/datastore/" + req.Store} {
		if err := pbs.SetACL(ctx, path, "DatastorePowerUser", tokenID); err != nil {
			fail(w, fmt.Errorf("Rechte setzen: %w", err))
			return
		}
	}
	// The user needs the same rights: a token never has more than its user.
	if err := pbs.SetACL(ctx, "/datastore/"+req.Store, "DatastorePowerUser", user); err != nil {
		fail(w, fmt.Errorf("Rechte setzen: %w", err))
		return
	}
	port := srv.Port
	if port == 0 {
		port = 8007
	}
	if err := pve.AddPBSStorage(ctx, req.Name, srv.Address(), port, req.Store, tokenID, secret, srv.Fingerprint, req.Namespace); err != nil {
		fail(w, fmt.Errorf("Speicher in Proxmox VE eintragen: %w", err))
		return
	}
	s.C.Event("info", cl.Name, "Backup-Speicher "+req.Name+" eingetragen", srv.Name+" / "+req.Store)
	s.C.RequestRefresh()
	ok(w)
}

func (s *Server) deletePVEStorage(w http.ResponseWriter, r *http.Request) {
	pve, cl, err := s.C.PVE(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if err := pve.DeleteStorage(r.Context(), r.PathValue("storage")); err != nil {
		fail(w, err)
		return
	}
	s.C.Event("warning", cl.Name, "Backup-Speicher "+r.PathValue("storage")+" ausgetragen", "Die Sicherungen auf dem Backup-Server bleiben erhalten.")
	ok(w)
}

func (s *Server) nodeStorages(w http.ResponseWriter, r *http.Request) {
	pve, _, err := s.C.PVE(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	list, err := pve.NodeStorages(r.Context(), r.PathValue("node"), r.URL.Query().Get("content"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"storages": list})
}

// ---- backup jobs ----

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	pve, _, err := s.C.PVE(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	jobs, err := pve.BackupJobs(r.Context())
	if err != nil {
		if prox.IsStatus(err, 403) {
			if perms, perr := pve.Permissions(r.Context()); perr == nil {
				if missing := prox.MissingPrivs(perms); len(missing) > 0 {
					cl, _ := s.C.Cluster(r.PathValue("id"))
					writeErr(w, http.StatusBadGateway, permHint(cl.TokenID, missing))
					return
				}
			}
		}
		fail(w, err)
		return
	}
	storages, _ := pve.Storages(r.Context())
	guests, _ := pve.Guests(r.Context())
	nodes, _ := pve.Nodes(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "storages": storages, "guests": guests, "nodes": nodes})
}

type jobReq struct {
	Schedule      string `json:"schedule"`
	Storage       string `json:"storage"`
	Mode          string `json:"mode"`
	Selection     string `json:"selection"` // all, include, exclude
	VMIDs         string `json:"vmids"`
	Node          string `json:"node"`
	Enabled       bool   `json:"enabled"`
	Comment       string `json:"comment"`
	NotesTemplate string `json:"notes_template"`
	PruneBackups  string `json:"prune_backups"`
}

var pruneRe = regexp.MustCompile(`^(keep-(all|last|hourly|daily|weekly|monthly|yearly)=\d+)(,keep-(all|last|hourly|daily|weekly|monthly|yearly)=\d+)*$`)

func (s *Server) saveJob(w http.ResponseWriter, r *http.Request) {
	var req jobReq
	if !decode(w, r, &req) {
		return
	}
	pve, cl, err := s.C.PVE(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	req.VMIDs = strings.ReplaceAll(strings.TrimSpace(req.VMIDs), " ", "")
	errs := map[string]string{}
	if strings.TrimSpace(req.Schedule) == "" || !schedRe.MatchString(req.Schedule) {
		errs["schedule"] = "Zeitplan angeben, z. B. 21:00, mon..fri 22:30 oder sat 02:00."
	}
	if req.Storage == "" {
		errs["storage"] = "Ziel-Speicher wählen."
	}
	if req.Mode != "snapshot" && req.Mode != "suspend" && req.Mode != "stop" {
		req.Mode = "snapshot"
	}
	if req.Selection != "all" && req.Selection != "exclude" && req.Selection != "include" {
		errs["selection"] = "Auswahl treffen."
	}
	if req.Selection != "all" && !vmidListRe.MatchString(req.VMIDs) {
		errs["vmids"] = "Mindestens eine VM bzw. einen Container wählen."
	}
	if req.PruneBackups != "" && !pruneRe.MatchString(req.PruneBackups) {
		errs["prune_backups"] = "Form: keep-last=3,keep-daily=7"
	}
	if fieldErrs(w, errs) {
		return
	}
	p := prox.Params{"schedule": req.Schedule, "storage": req.Storage, "mode": req.Mode, "enabled": req.Enabled}
	var del []string
	set := func(k, v string) {
		if v == "" {
			del = append(del, k)
		} else {
			p[k] = v
		}
	}
	switch req.Selection {
	case "all":
		p["all"] = true
		del = append(del, "vmid", "exclude")
	case "exclude":
		p["all"] = true
		p["exclude"] = req.VMIDs
		del = append(del, "vmid")
	case "include":
		p["vmid"] = req.VMIDs
		del = append(del, "all", "exclude")
	}
	set("node", req.Node)
	set("comment", req.Comment)
	set("notes-template", req.NotesTemplate)
	set("prune-backups", req.PruneBackups)
	job := r.PathValue("job")
	if job == "" {
		for k := range p {
			if p[k] == "" {
				delete(p, k)
			}
		}
		if req.Selection == "include" {
			delete(p, "all")
		}
		err = pve.CreateBackupJob(r.Context(), p)
	} else {
		if len(del) > 0 {
			p["delete"] = strings.Join(del, ",")
		}
		err = pve.UpdateBackupJob(r.Context(), job, p)
	}
	if err != nil {
		fail(w, err)
		return
	}
	s.C.Event("info", cl.Name, "Sicherungsjob gespeichert", req.Schedule+" → "+req.Storage)
	s.C.RequestRefresh()
	ok(w)
}

func (s *Server) deleteJob(w http.ResponseWriter, r *http.Request) {
	pve, cl, err := s.C.PVE(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if err := pve.DeleteBackupJob(r.Context(), r.PathValue("job")); err != nil {
		fail(w, err)
		return
	}
	s.C.Event("warning", cl.Name, "Sicherungsjob gelöscht", r.PathValue("job"))
	s.C.RequestRefresh()
	ok(w)
}

// runJob starts a backup job now: one vzdump per node with the guests of that node.
func (s *Server) runJob(w http.ResponseWriter, r *http.Request) {
	pve, _, err := s.C.PVE(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ctx := r.Context()
	jobs, err := pve.BackupJobs(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	var job *prox.BackupJob
	for i := range jobs {
		if jobs[i].ID == r.PathValue("job") {
			job = &jobs[i]
		}
	}
	if job == nil {
		writeErr(w, http.StatusNotFound, "Job nicht gefunden.")
		return
	}
	guests, err := pve.Guests(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	include := idSet(job.VMID)
	exclude := idSet(job.Exclude)
	perNode := map[string][]string{}
	for _, g := range guests {
		if g.Template != 0 || (job.Node != "" && g.Node != job.Node) {
			continue
		}
		id := strconv.Itoa(g.VMID)
		if job.All != 0 && !exclude[id] || job.All == 0 && include[id] {
			perNode[g.Node] = append(perNode[g.Node], id)
		}
	}
	if len(perNode) == 0 {
		writeErr(w, http.StatusConflict, "Der Job umfasst keine VM und keinen Container.")
		return
	}
	var upids []string
	var errs []string
	for node, ids := range perNode {
		p := prox.Params{"vmid": strings.Join(ids, ","), "storage": job.Storage, "mode": job.Mode, "notes-template": emptyNil(job.NotesTemplate)}
		if job.PruneBackups != "" {
			p["prune-backups"] = job.PruneBackups
		}
		upid, err := pve.Vzdump(ctx, node, p)
		if err != nil {
			errs = append(errs, node+": "+err.Error())
			continue
		}
		upids = append(upids, upid)
	}
	if len(upids) == 0 {
		writeErr(w, http.StatusBadGateway, strings.Join(errs, "; "))
		return
	}
	s.C.RequestRefresh()
	writeJSON(w, http.StatusOK, map[string]any{"upids": upids, "errors": errs})
}

// permHint explains missing privileges of a PVE token with the command that fixes them.
func permHint(tokenID string, missing []string) string {
	user, _, _ := strings.Cut(tokenID, "!")
	cmd := "pveum acl modify / --tokens '" + tokenID + "' --roles Administrator"
	if user != "root@pam" {
		// A token never has more rights than its user.
		cmd = "pveum acl modify / --users '" + user + "' --roles Administrator && " + cmd
	}
	return "Dem Token fehlen Rechte in Proxmox VE (" + strings.Join(missing, ", ") + "). Er sieht sonst keine VMs und Jobs. " +
		"Auf einem Knoten ausführen: " + cmd + " – ein Token hat nie mehr Rechte als sein Benutzer."
}

func emptyNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func idSet(list string) map[string]bool {
	m := map[string]bool{}
	for _, x := range strings.Split(list, ",") {
		if x = strings.TrimSpace(x); x != "" {
			m[x] = true
		}
	}
	return m
}

// ---- guests ----

type backupRef struct {
	Server     string `json:"server"`
	ServerName string `json:"server_name"`
	Store      string `json:"store"`
	Last       int64  `json:"last"`
	Count      int    `json:"count"`
}

type guestView struct {
	prox.Guest
	Cluster     string      `json:"cluster"`
	ClusterName string      `json:"cluster_name"`
	InJob       bool        `json:"in_job"`
	Backups     []backupRef `json:"backups"`
	LastBackup  int64       `json:"last_backup"`
}

func (s *Server) guests(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	clusters, _ := s.C.Clusters()
	servers, _ := s.C.Servers()
	var mu sync.Mutex
	var wg sync.WaitGroup
	backups := map[string][]backupRef{} // "vm/100" → refs
	var errs []string
	for _, srv := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pbs := s.C.PBSClient(srv)
			usage, err := pbs.Usage(ctx)
			if err != nil {
				mu.Lock()
				errs = append(errs, srv.Name+": "+err.Error())
				mu.Unlock()
				return
			}
			for _, u := range usage {
				if u.MountStatus == "notmounted" {
					continue
				}
				groups, err := pbs.Groups(ctx, u.Store)
				if err != nil {
					continue
				}
				mu.Lock()
				for _, g := range groups {
					k := g.Type + "/" + g.ID
					backups[k] = append(backups[k], backupRef{Server: srv.ID, ServerName: srv.Name, Store: u.Store, Last: g.LastBackup, Count: g.Count})
				}
				mu.Unlock()
			}
		}()
	}
	out := []guestView{}
	for _, cl := range clusters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pve := s.C.PVEClient(cl)
			list, err := pve.Guests(ctx)
			if err != nil {
				mu.Lock()
				errs = append(errs, cl.Name+": "+err.Error())
				mu.Unlock()
				return
			}
			if perms, err := pve.Permissions(ctx); err == nil {
				if missing := prox.MissingPrivs(perms); len(missing) > 0 {
					mu.Lock()
					errs = append(errs, cl.Name+": "+permHint(cl.TokenID, missing))
					mu.Unlock()
				}
			}
			nb := map[int]bool{}
			if nbs, err := pve.NotBackedUp(ctx); err == nil {
				for _, g := range nbs {
					nb[g.VMID] = true
				}
			}
			mu.Lock()
			for _, g := range list {
				if g.Template != 0 {
					continue
				}
				out = append(out, guestView{Guest: g, Cluster: cl.ID, ClusterName: cl.Name, InJob: !nb[g.VMID]})
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	for i := range out {
		typ := "vm"
		if out[i].Type == "lxc" {
			typ = "ct"
		}
		out[i].Backups = backups[typ+"/"+strconv.Itoa(out[i].VMID)]
		if out[i].Backups == nil {
			out[i].Backups = []backupRef{}
		}
		// The appliance's own server first, then the newest backup.
		bs := out[i].Backups
		sort.SliceStable(bs, func(a, b int) bool {
			if (bs[a].Server == "local") != (bs[b].Server == "local") {
				return bs[a].Server == "local"
			}
			return bs[a].Last > bs[b].Last
		})
		for _, b := range out[i].Backups {
			if b.Last > out[i].LastBackup {
				out[i].LastBackup = b.Last
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ClusterName != out[j].ClusterName {
			return out[i].ClusterName < out[j].ClusterName
		}
		return out[i].VMID < out[j].VMID
	})
	writeJSON(w, http.StatusOK, map[string]any{"guests": out, "errors": errs})
}

func (s *Server) backupGuest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string `json:"cluster"`
		Node    string `json:"node"`
		VMID    int    `json:"vmid"`
		Storage string `json:"storage"`
		Mode    string `json:"mode"`
		Notes   string `json:"notes"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Storage == "" {
		fieldErrs(w, map[string]string{"storage": "Ziel-Speicher wählen."})
		return
	}
	pve, cl, err := s.C.PVE(req.Cluster)
	if err != nil {
		fail(w, err)
		return
	}
	if req.Mode == "" {
		req.Mode = "snapshot"
	}
	upid, err := pve.Vzdump(r.Context(), req.Node, prox.Params{"vmid": req.VMID, "storage": req.Storage, "mode": req.Mode, "notes-template": emptyNil(req.Notes)})
	if err != nil {
		fail(w, err)
		return
	}
	s.C.Event("info", cl.Name, fmt.Sprintf("Sicherung von %d gestartet", req.VMID), "Ziel: "+req.Storage)
	s.C.RequestRefresh()
	writeJSON(w, http.StatusOK, map[string]any{"upid": upid, "source": "pve:" + cl.ID})
}

func (s *Server) guestSnapshots(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pbs, _, err := s.C.PBS(q.Get("pbs"))
	if err != nil {
		fail(w, err)
		return
	}
	snaps, err := pbs.Snapshots(r.Context(), q.Get("store"), "")
	if err != nil {
		fail(w, err)
		return
	}
	out := []prox.Snapshot{}
	for _, sn := range snaps {
		if sn.Type == q.Get("type") && sn.ID == q.Get("bid") {
			out = append(out, sn)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time > out[j].Time })
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": out})
}

func (s *Server) restore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string `json:"cluster"`
		Node    string `json:"node"`
		PBS     string `json:"pbs"`
		Store   string `json:"store"`
		Type    string `json:"type"` // vm, ct
		ID      string `json:"bid"`
		Time    int64  `json:"time"`
		VMID    int    `json:"vmid"`
		Storage string `json:"storage"`
		Start   bool   `json:"start"`
	}
	if !decode(w, r, &req) {
		return
	}
	pve, cl, err := s.C.PVE(req.Cluster)
	if err != nil {
		fail(w, err)
		return
	}
	srv, err := s.C.Server(req.PBS)
	if err != nil {
		fail(w, err)
		return
	}
	ctx := r.Context()
	errs := map[string]string{}
	if req.Node == "" {
		errs["node"] = "Ziel-Knoten wählen."
	}
	if req.Storage == "" {
		errs["storage"] = "Ziel-Speicher für die Disks wählen."
	}
	if req.VMID < 100 {
		errs["vmid"] = "Neue ID ab 100."
	}
	if fieldErrs(w, errs) {
		return
	}
	// The backup must be reachable through a PBS storage of this cluster.
	storages, err := pve.Storages(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	pveStore := ""
	for _, st := range storages {
		if st.Type != "pbs" || st.Datastore != req.Store {
			continue
		}
		if m, ok := s.matchServer(st); ok && m.ID == srv.ID {
			pveStore = st.Storage
			break
		}
	}
	if pveStore == "" {
		writeErr(w, http.StatusConflict, "Der Datastore "+req.Store+" ist in „"+cl.Name+"“ nicht als Speicher eingetragen. Unter Proxmox VE → Backup-Speicher eintragen und dann wiederherstellen.")
		return
	}
	archive := fmt.Sprintf("%s:backup/%s/%s/%s", pveStore, req.Type, req.ID, time.Unix(req.Time, 0).UTC().Format("2006-01-02T15:04:05Z"))
	typ := "qemu"
	if req.Type == "ct" {
		typ = "lxc"
	}
	upid, err := pve.Restore(ctx, req.Node, typ, req.VMID, archive, req.Storage, req.Start)
	if err != nil {
		fail(w, err)
		return
	}
	s.C.Event("info", cl.Name, fmt.Sprintf("Wiederherstellung als %d gestartet", req.VMID), archive)
	s.C.RequestRefresh()
	writeJSON(w, http.StatusOK, map[string]any{"upid": upid, "source": "pve:" + cl.ID})
}
