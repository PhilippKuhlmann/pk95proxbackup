package api

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"pk95proxbackup/internal/store"
)

// Accounts with roles:
//   - viewer (Nur lesen): sees everything, changes nothing, cannot read backup contents or keys
//   - files (Dateien wiederherstellen): like viewer, plus fetching single files out of backups –
//     for customers who get a deleted file back themselves
//   - operator (Bediener): day-to-day work – back up now, restore, run restore tests and
//     copies, plug USB disks in and out, check and free space
//   - admin: everything, including settings, connections, keys and accounts
// Every change and every access to sensitive data goes to the audit log.

var roleRank = map[string]int{"viewer": 1, "files": 2, "operator": 3, "admin": 4}

// roleNames are the German names of the roles.
var roleNames = map[string]string{"viewer": "Nur lesen", "files": "Dateien wiederherstellen", "operator": "Bediener", "admin": "Administrator"}

// routeRole overrides the default (GET: viewer, everything else: admin).
var routeRole = map[string]string{
	// own account
	"POST /api/account/password":      "viewer",
	"POST /api/account/totp/setup":    "viewer",
	"POST /api/account/totp/enable":   "viewer",
	"POST /api/account/totp/disable":  "viewer",
	"POST /api/account/totp/recovery": "viewer",
	"POST /api/restoretests/schedule": "viewer", // only computes dates
	// day-to-day work
	"POST /api/health/check":            "operator",
	"POST /api/pbs/{id}/health":         "operator",
	"POST /api/guests/backup":           "operator",
	"POST /api/restore":                 "operator",
	"POST /api/pve/{id}/jobs/{job}/run": "operator",
	"POST /api/pbs/{id}/sync/{job}/run": "operator",
	"POST /api/restoretests/{id}/run":   "operator",
	"POST /api/restoretests/cancel":     "operator",
	"POST /api/anomalies/{id}/ack":      "operator",
	"POST /api/archives/{id}/check":     "operator",
	"POST /api/archives/{id}/eject":     "operator",
	"POST /api/shares/{id}/{action}":    "operator",
	// gc, verify, mount, unmount; rotation: run, eject (rebuild formats: admin, see auth)
	"POST /api/pbs/{id}/datastores/{store}/{action}": "operator",
	"POST /api/rotations/{id}/disks/{key}/{action}":  "operator",
	// reading backup contents and secrets
	"GET /api/restore/files":       "files",
	"GET /api/restore/download":    "files",
	"GET /api/enckeys/{key}":       "admin",
	"GET /api/enckeys/{key}/paper": "admin",
	"GET /api/selfbackup/download": "admin",
	"GET /api/settings":            "admin",
	"GET /api/users":               "admin",
	"GET /api/audit":               "admin",
}

// auditedReads are GET routes worth a line in the audit log.
var auditedReads = map[string]bool{
	"GET /api/restore/download": true, "GET /api/enckeys/{key}": true, "GET /api/enckeys/{key}/paper": true, "GET /api/selfbackup/download": true,
}

// totpFree are the routes an account that must set up two-factor sign-in may still use.
var totpFree = map[string]bool{
	"GET /api/account/totp": true, "POST /api/account/totp/setup": true, "POST /api/account/totp/enable": true,
	"POST /api/account/password": true, "GET /api/system": true,
}

func requiredRole(pattern string) string {
	if r, ok := routeRole[pattern]; ok {
		return r
	}
	if strings.HasPrefix(pattern, "GET ") {
		return "viewer"
	}
	return "admin"
}

// mustSetUpTOTP reports an account that has to set up two-factor sign-in first.
func (s *Server) mustSetUpTOTP(u store.User) bool {
	return u.TOTPRequired && !loadTOTP(s.C.St, u.ID).Enabled
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// auth checks session, CSRF header, role and two-factor duty, and writes the audit log.
func (s *Server) auth(pattern string, next http.HandlerFunc) http.Handler {
	audited := !strings.HasPrefix(pattern, "GET ") || auditedReads[pattern]
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "Bitte anmelden.")
			return
		}
		u, err := s.C.St.SessionUser(c.Value)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "Sitzung abgelaufen, bitte neu anmelden.")
			return
		}
		// Mutating requests must come from our own page (custom header forces a CORS preflight).
		if r.Method != http.MethodGet && r.Header.Get("X-PK95") != "1" {
			writeErr(w, http.StatusForbidden, "Anfrage abgelehnt.")
			return
		}
		if s.mustSetUpTOTP(u) && !totpFree[pattern] {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "Für dieses Konto ist die Zwei-Faktor-Anmeldung vorgeschrieben – bitte zuerst einrichten.", "totp_required": true})
			return
		}
		role := u.Role
		if roleRank[role] == 0 {
			role = "viewer"
		}
		need := requiredRole(pattern)
		// Rebuilding a rotation disk formats it: administrators only.
		if pattern == "POST /api/rotations/{id}/disks/{key}/{action}" && (r.PathValue("action") == "rebuild" || r.PathValue("action") == "adopt") {
			need = "admin"
		}
		if roleRank[role] < roleRank[need] {
			s.audit(r, u.Username, pattern, http.StatusForbidden)
			writeErr(w, http.StatusForbidden, "Dafür fehlen die Rechte – angemeldet als „"+roleNames[role]+"“, nötig ist „"+roleNames[need]+"“.")
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, u)
		if !audited {
			next(w, r.WithContext(ctx))
			return
		}
		sw := &statusWriter{ResponseWriter: w}
		next(sw, r.WithContext(ctx))
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		s.audit(r, u.Username, pattern, sw.status)
	})
}

// actionNames word the routes for the audit log.
var actionNames = map[string]string{
	"POST /api/login": "Anmeldung", "POST /api/logout": "Abmeldung",
	"PUT /api/settings": "Benachrichtigungen geändert", "POST /api/settings/test": "Testnachricht", "PUT /api/settings/access": "Zugriff geändert",
	"POST /api/account/password": "Eigenes Passwort geändert", "POST /api/account/totp/enable": "Zwei-Faktor eingeschaltet",
	"POST /api/account/totp/disable": "Zwei-Faktor ausgeschaltet", "POST /api/account/totp/recovery": "Neue Wiederherstellungscodes",
	"PUT /api/update": "Aktualisierung eingestellt", "POST /api/update/install": "Aktualisierung installiert",
	"POST /api/pbs/{id}/packages": "Paketlisten aktualisiert", "POST /api/pbs/{id}/upgrade": "Systemupdates gestartet", "POST /api/pbs/{id}/reboot": "Backup-Server neu gestartet", "POST /api/anomalies/{id}/ack": "Auffällige Sicherung als geprüft markiert",
	"PUT /api/pbs/{id}/autoupdate": "Automatische Updates eingestellt", "POST /api/pbs/{id}/ssh": "SSH-Zugang eingerichtet",
	"PUT /api/selfbackup": "Sicherung der Appliance eingestellt", "POST /api/selfbackup/run": "Sicherung der Appliance gestartet",
	"GET /api/selfbackup/download": "Sicherung der Appliance heruntergeladen", "POST /api/selfbackup/restore": "Appliance wiederhergestellt",
	"POST /api/pbs": "Backup-Server verbunden", "PUT /api/pbs/{id}": "Backup-Server geändert", "DELETE /api/pbs/{id}": "Backup-Server entfernt",
	"POST /api/pbs/{id}/datastores": "Datastore angelegt", "PUT /api/pbs/{id}/datastores/{store}": "Datastore geändert",
	"DELETE /api/pbs/{id}/datastores/{store}": "Datastore entfernt", "POST /api/pbs/{id}/datastores/{store}/{action}": "Datastore-Aktion",
	"DELETE /api/pbs/{id}/datastores/{store}/snapshots": "Sicherung gelöscht", "PUT /api/pbs/{id}/datastores/{store}/protected": "Sicherung geschützt/freigegeben",
	"PUT /api/protect": "Löschschutz geändert", "PUT /api/protect/storages/{cluster}/{storage}": "Löschrecht von Proxmox VE geändert",
	"POST /api/pbs/{id}/s3": "S3-Ziel eingetragen", "DELETE /api/pbs/{id}/s3/{ep}": "S3-Ziel ausgetragen", "PUT /api/pbs/{id}/s3/{ep}/limit": "S3-Upload begrenzt",
	"POST /api/pbs/{id}/traffic": "Bandbreite begrenzt", "PUT /api/pbs/{id}/traffic/{name}": "Bandbreite geändert", "DELETE /api/pbs/{id}/traffic/{name}": "Bandbreiten-Grenze entfernt",
	"POST /api/pbs/{id}/sync": "Kopierjob angelegt", "PUT /api/pbs/{id}/sync/{job}": "Kopierjob geändert", "DELETE /api/pbs/{id}/sync/{job}": "Kopierjob gelöscht",
	"POST /api/pbs/{id}/sync/{job}/run": "Kopierjob gestartet",
	"POST /api/pve":                     "Proxmox VE verbunden", "PUT /api/pve/{id}": "Proxmox VE geändert", "DELETE /api/pve/{id}": "Proxmox VE entfernt",
	"POST /api/pve/{id}/storages": "Backup-Speicher eingetragen", "DELETE /api/pve/{id}/storages/{storage}": "Backup-Speicher ausgetragen",
	"PUT /api/pve/{id}/storages/{storage}/key": "Verschlüsselung geändert", "POST /api/enckeys": "Schlüssel angelegt/importiert",
	"GET /api/enckeys/{key}": "Schlüssel angezeigt", "GET /api/enckeys/{key}/paper": "Papierschlüssel geöffnet", "POST /api/enckeys/{key}/delete": "Schlüssel gelöscht",
	"POST /api/pve/{id}/jobs": "Sicherungsjob angelegt", "PUT /api/pve/{id}/jobs/{job}": "Sicherungsjob geändert", "DELETE /api/pve/{id}/jobs/{job}": "Sicherungsjob gelöscht",
	"POST /api/pve/{id}/jobs/{job}/run": "Sicherungsjob gestartet", "PUT /api/pve/{id}/mail": "Mails zu Sicherungsjobs umgestellt",
	"POST /api/guests/backup": "Gast gesichert", "POST /api/restore": "Wiederherstellung gestartet", "GET /api/restore/download": "Dateien aus Sicherung geladen",
	"POST /api/restoretests": "Restore-Test angelegt", "PUT /api/restoretests/{id}": "Restore-Test geändert", "DELETE /api/restoretests/{id}": "Restore-Test gelöscht",
	"POST /api/restoretests/{id}/run": "Restore-Test gestartet", "POST /api/restoretests/cancel": "Restore-Test abgebrochen",
	"POST /api/shares": "Freigabe angelegt", "PUT /api/shares/{id}": "Freigabe geändert", "DELETE /api/shares/{id}": "Freigabe entfernt", "POST /api/shares/{id}/{action}": "Freigabe-Aktion",
	"POST /api/rotations": "Wechselplatten-Satz angelegt", "PUT /api/rotations/{id}": "Wechselplatten-Satz geändert", "DELETE /api/rotations/{id}": "Wechselplatten-Satz gelöscht",
	"POST /api/rotations/{id}/disks/{key}/{action}": "Wechselplatte", "POST /api/rotations/{id}/disks": "Wechselplatte hinzugefügt", "DELETE /api/rotations/{id}/disks/{key}": "Wechselplatte entfernt",
	"POST /api/archives": "Archiv-Platte angelegt", "DELETE /api/archives/{id}": "Archiv-Platte entfernt", "POST /api/archives/import": "Fremde Platte eingelesen",
	"POST /api/users": "Konto angelegt", "PUT /api/users/{id}": "Konto geändert", "DELETE /api/users/{id}": "Konto gelöscht",
	"POST /api/users/{id}/password": "Passwort eines Kontos gesetzt", "POST /api/users/{id}/totp-reset": "Zwei-Faktor eines Kontos zurückgesetzt",
}

var pathValueNames = []string{"id", "store", "job", "storage", "key", "action", "name", "ep", "cluster"}

// audit writes a line to the audit log.
func (s *Server) audit(r *http.Request, user, pattern string, status int) {
	action := actionNames[pattern]
	if action == "" {
		action = pattern
	}
	var target []string
	for _, n := range pathValueNames {
		if v := r.PathValue(n); v != "" {
			target = append(target, v)
		}
	}
	if q := r.URL.Query(); q.Get("bid") != "" {
		target = append(target, q.Get("type")+"/"+q.Get("bid"))
	}
	_ = s.C.St.AddAudit(store.AuditEntry{TS: time.Now().Unix(), User: user, IP: s.clientIP(r), Action: action, Target: strings.Join(target, " · "), Status: status})
}

// ---- accounts ----

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{1,63}$`)

type userView struct {
	store.User
	TOTP      bool `json:"totp"`
	Me        bool `json:"me"`
	MustSetUp bool `json:"must_set_up"`
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.C.St.Users()
	if err != nil {
		fail(w, err)
		return
	}
	me := currentUser(r)
	out := []userView{}
	for _, u := range users {
		t := loadTOTP(s.C.St, u.ID).Enabled
		out = append(out, userView{User: u, TOTP: t, Me: u.ID == me.ID, MustSetUp: u.TOTPRequired && !t})
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out, "roles": roleNames})
}

func otherAdmins(users []store.User, except int64) int {
	n := 0
	for _, u := range users {
		if u.Role == "admin" && u.ID != except {
			n++
		}
	}
	return n
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username     string `json:"username"`
		Password     string `json:"password"`
		Role         string `json:"role"`
		TOTPRequired bool   `json:"totp_required"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	errs := map[string]string{}
	if !usernameRe.MatchString(req.Username) {
		errs["username"] = "2–64 Zeichen: Buchstaben, Ziffern, . _ @ -"
	} else if _, err := s.C.St.UserByName(req.Username); err == nil {
		errs["username"] = "Dieses Konto gibt es schon."
	}
	if msg := validPassword(req.Password); msg != "" {
		errs["password"] = msg
	}
	if roleRank[req.Role] == 0 {
		errs["role"] = "Rolle wählen."
	}
	if fieldErrs(w, errs) {
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(w, err)
		return
	}
	id, err := s.C.St.CreateUserRole(req.Username, string(hash), req.Role, req.TOTPRequired)
	if err != nil {
		fail(w, err)
		return
	}
	s.C.Event("info", "Appliance", "Konto „"+req.Username+"“ angelegt", "Rolle „"+roleNames[req.Role]+"“ (von "+currentUser(r).Username+").")
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) userFromPath(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusNotFound, "Konto nicht gefunden.")
		return store.User{}, false
	}
	u, err := s.C.St.UserByID(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "Konto nicht gefunden.")
		return u, false
	}
	return u, true
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Role         string `json:"role"`
		TOTPRequired bool   `json:"totp_required"`
	}
	if !decode(w, r, &req) {
		return
	}
	u, good := s.userFromPath(w, r)
	if !good {
		return
	}
	if roleRank[req.Role] == 0 {
		fieldErrs(w, map[string]string{"role": "Rolle wählen."})
		return
	}
	if u.Role == "admin" && req.Role != "admin" {
		users, _ := s.C.St.Users()
		if otherAdmins(users, u.ID) == 0 {
			writeErr(w, http.StatusConflict, "Das ist das letzte Administrator-Konto – mindestens eines muss bleiben.")
			return
		}
	}
	if err := s.C.St.SetUserRole(u.ID, req.Role, req.TOTPRequired); err != nil {
		fail(w, err)
		return
	}
	if u.Role != req.Role {
		s.C.Event("warning", "Appliance", "Rolle von „"+u.Username+"“ geändert", "„"+roleNames[u.Role]+"“ → „"+roleNames[req.Role]+"“ (von "+currentUser(r).Username+").")
	}
	ok(w)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	u, good := s.userFromPath(w, r)
	if !good {
		return
	}
	if u.ID == currentUser(r).ID {
		writeErr(w, http.StatusConflict, "Das eigene Konto lässt sich nicht löschen.")
		return
	}
	if u.Role == "admin" {
		users, _ := s.C.St.Users()
		if otherAdmins(users, u.ID) == 0 {
			writeErr(w, http.StatusConflict, "Das ist das letzte Administrator-Konto.")
			return
		}
	}
	if err := s.C.St.DeleteUser(u.ID); err != nil {
		fail(w, err)
		return
	}
	_ = s.C.St.DeleteSetting(TOTPKey(u.ID))
	s.C.Event("warning", "Appliance", "Konto „"+u.Username+"“ gelöscht", "von "+currentUser(r).Username)
	ok(w)
}

// setUserPassword sets a new password for another account and ends its sessions.
func (s *Server) setUserPassword(w http.ResponseWriter, r *http.Request) {
	var req struct{ Password string }
	if !decode(w, r, &req) {
		return
	}
	u, good := s.userFromPath(w, r)
	if !good {
		return
	}
	if msg := validPassword(req.Password); msg != "" {
		fieldErrs(w, map[string]string{"password": msg})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.C.St.SetPassword(u.ID, string(hash), ""); err != nil {
		fail(w, err)
		return
	}
	_ = s.C.St.EndSessions(u.ID)
	s.C.Event("warning", "Appliance", "Passwort von „"+u.Username+"“ neu gesetzt", "Alle Sitzungen des Kontos sind beendet (von "+currentUser(r).Username+").")
	ok(w)
}

// resetUserTOTP switches two-factor sign-in of another account off (lost phone).
func (s *Server) resetUserTOTP(w http.ResponseWriter, r *http.Request) {
	u, good := s.userFromPath(w, r)
	if !good {
		return
	}
	if err := s.C.St.DeleteSetting(TOTPKey(u.ID)); err != nil {
		fail(w, err)
		return
	}
	_ = s.C.St.EndSessions(u.ID)
	s.C.Event("warning", "Appliance", "Zwei-Faktor-Anmeldung von „"+u.Username+"“ zurückgesetzt", "von "+currentUser(r).Username)
	ok(w)
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	entries, err := s.C.St.Audit(r.URL.Query().Get("user"), 500)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}
