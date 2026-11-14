// Package api serves the JSON API and the embedded web UI.
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/store"
)

const (
	cookieName = "pk95pb_session"
	sessionTTL = 7 * 24 * time.Hour
)

// Server is the HTTP API.
type Server struct {
	C      *core.Core
	Web    fs.FS
	Secure bool // cookies only over HTTPS
	mux    *http.ServeMux

	limMu   sync.Mutex
	fails   map[string][]time.Time
	granted map[string]bool         // read access given to PVE storages (see readAccess)
	tickets map[string]*loginTicket // password checked, TOTP code still missing
}

// New builds the handler.
func New(c *core.Core, web fs.FS, secure bool) *Server {
	s := &Server{C: c, Web: web, Secure: secure, mux: http.NewServeMux(), fails: map[string][]time.Time{}, granted: map[string]bool{}, tickets: map[string]*loginTicket{}}
	s.routes()
	c.EnsureStorage = s.ensurePVEStorage
	c.MonthlyHTML = func(rep core.MonthlyReport) []byte { return s.renderMonthly(rep, false) }
	c.ReportHTML = func(run core.RestoreTestRun, images func(string) (string, []byte)) []byte {
		b, _ := s.renderReport(run, images, false)
		return b
	}
	return s
}

type ctxKey struct{}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	if r.Header.Get("X-Forwarded-For") != "" {
		host := r.Header.Get("X-Forwarded-Host")
		if host == "" {
			host = r.Host
		}
		s.C.NoteProxy(remoteIP(r), strings.ToLower(r.Header.Get("X-Forwarded-Proto")), host)
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	m := s.mux
	// public
	m.HandleFunc("GET /api/session", s.getSession)
	m.HandleFunc("POST /api/login", s.login)
	m.HandleFunc("POST /api/login/totp", s.loginTOTP)
	m.HandleFunc("POST /api/logout", s.logout)
	m.HandleFunc("POST /api/setup", s.setup)
	m.HandleFunc("POST /api/setup/restore", s.setupRestore)

	a := func(pattern string, h http.HandlerFunc) { m.Handle(pattern, s.auth(h)) }

	a("GET /api/overview", s.overview)
	a("GET /api/events", s.events)
	a("GET /api/running", s.running)
	a("GET /api/system", s.system)
	a("GET /api/settings", s.getSettings)
	a("PUT /api/settings", s.putSettings)
	a("POST /api/settings/test", s.testNotify)
	a("POST /api/settings/telegram/chats", s.telegramChats)
	a("GET /api/settings/preview", s.previewMail)
	a("PUT /api/settings/access", s.putAccess)
	a("POST /api/account/password", s.changePassword)
	a("GET /api/account/totp", s.getTOTP)
	a("POST /api/account/totp/setup", s.setupTOTP)
	a("POST /api/account/totp/enable", s.enableTOTP)
	a("POST /api/account/totp/disable", s.disableTOTP)
	a("POST /api/account/totp/recovery", s.newRecoveryCodes)
	a("GET /api/health", s.listHealth)
	a("POST /api/health/check", s.checkHealth)
	a("DELETE /api/health", s.forgetHealth)
	a("GET /api/update", s.getUpdate)
	a("PUT /api/update", s.putUpdate)
	a("POST /api/update/check", s.checkUpdate)
	a("POST /api/update/install", s.installUpdate)
	a("GET /api/selfbackup", s.getSelfBackup)
	a("PUT /api/selfbackup", s.putSelfBackup)
	a("POST /api/selfbackup/run", s.runSelfBackup)
	a("GET /api/selfbackup/download", s.downloadSelfBackup)
	a("GET /api/selfbackup/found", s.foundSelfBackups)
	a("POST /api/selfbackup/restore", s.restoreSelfBackup)

	// Backup Servers
	a("POST /api/probe", s.probe)
	a("GET /api/pbs", s.listPBS)
	a("POST /api/pbs", s.savePBS)
	a("PUT /api/pbs/{id}", s.savePBS)
	a("DELETE /api/pbs/{id}", s.deletePBS)
	a("POST /api/pbs/{id}/test-ssh", s.testSSH)
	a("GET /api/serverhealth", s.listServerHealth)
	a("GET /api/report/monthly", s.monthlyReport)
	a("POST /api/report/monthly/send", s.sendMonthly)
	a("GET /api/report/monthly/settings", s.getMonthly)
	a("PUT /api/report/monthly/settings", s.putMonthly)
	a("POST /api/pbs/{id}/health", s.checkServerHealth)
	a("POST /api/pbs/{id}/reload", s.reloadServer)
	a("GET /api/pbs/{id}/datastores", s.listDatastores)
	a("POST /api/pbs/{id}/datastores", s.createDatastore)
	a("PUT /api/pbs/{id}/datastores/{store}", s.updateDatastore)
	a("DELETE /api/pbs/{id}/datastores/{store}", s.deleteDatastore)
	a("POST /api/pbs/{id}/datastores/{store}/{action}", s.datastoreAction)
	a("GET /api/pbs/{id}/datastores/{store}/snapshots", s.snapshots)
	a("DELETE /api/pbs/{id}/datastores/{store}/snapshots", s.deleteSnapshot)
	a("GET /api/pbs/{id}/disks", s.disks)
	a("POST /api/pbs/{id}/disks/prepare", s.prepareDisk)
	a("GET /api/pbs/{id}/sync", s.listSync)
	a("POST /api/pbs/{id}/sync", s.saveSync)
	a("POST /api/pbs/{id}/sync/check", s.checkSyncSource)
	a("PUT /api/pbs/{id}/sync/{job}", s.saveSync)
	a("DELETE /api/pbs/{id}/sync/{job}", s.deleteSync)
	a("POST /api/pbs/{id}/sync/{job}/run", s.runSync)
	a("GET /api/tasks/log", s.taskLog)

	// Proxmox VE
	a("GET /api/pve", s.listPVE)
	a("POST /api/pve", s.savePVE)
	a("PUT /api/pve/{id}", s.savePVE)
	a("DELETE /api/pve/{id}", s.deletePVE)
	a("GET /api/pve/{id}/storages", s.pveStorages)
	a("POST /api/pve/{id}/storages", s.addPVEStorage)
	a("DELETE /api/pve/{id}/storages/{storage}", s.deletePVEStorage)
	a("GET /api/pve/{id}/nodes/{node}/storages", s.nodeStorages)
	a("GET /api/pve/{id}/jobs", s.listJobs)
	a("GET /api/pve/{id}/mail", s.getPVEMail)
	a("PUT /api/pve/{id}/mail", s.putPVEMail)
	a("POST /api/pve/{id}/jobs", s.saveJob)
	a("PUT /api/pve/{id}/jobs/{job}", s.saveJob)
	a("DELETE /api/pve/{id}/jobs/{job}", s.deleteJob)
	a("POST /api/pve/{id}/jobs/{job}/run", s.runJob)
	a("GET /api/guests", s.guests)
	a("POST /api/guests/backup", s.backupGuest)
	a("GET /api/guests/snapshots", s.guestSnapshots)
	a("POST /api/restore", s.restore)
	a("GET /api/restore/files", s.restoreFiles)
	a("GET /api/restoretests", s.listRestoreTests)
	a("POST /api/restoretests", s.saveRestoreTest)
	a("POST /api/restoretests/cancel", s.cancelRestoreTest)
	a("POST /api/restoretests/schedule", s.previewSchedule)
	a("GET /api/restoretests/screenshot", s.restoreTestScreenshot)
	a("GET /api/restoretests/report", s.restoreTestReport)
	a("PUT /api/restoretests/{id}", s.saveRestoreTest)
	a("DELETE /api/restoretests/{id}", s.deleteRestoreTest)
	a("POST /api/restoretests/{id}/run", s.runRestoreTest)
	a("GET /api/restore/sources", s.restoreSources)
	a("GET /api/restore/source", s.restoreSourceGuests)
	a("GET /api/restore/download", s.restoreDownload)

	// Shares
	a("GET /api/shares", s.listShares)
	a("POST /api/shares", s.saveShare)
	a("PUT /api/shares/{id}", s.saveShare)
	a("DELETE /api/shares/{id}", s.deleteShare)
	a("POST /api/shares/test", s.testShare)
	a("POST /api/shares/{id}/{action}", s.shareAction)

	// Rotation
	a("GET /api/rotations", s.listRotations)
	a("POST /api/rotations", s.saveRotation)
	a("PUT /api/rotations/{id}", s.saveRotation)
	a("DELETE /api/rotations/{id}", s.deleteRotation)
	a("PUT /api/rotations/{id}/plan", s.saveRotationPlan)
	a("POST /api/rotations/{id}/disks", s.addRotationDisk)
	a("DELETE /api/rotations/{id}/disks/{key}", s.removeRotationDisk)
	a("POST /api/rotations/{id}/disks/{key}/{action}", s.rotationDiskAction)
	a("GET /api/archives", s.listArchives)
	a("POST /api/archives", s.createArchive)
	a("PUT /api/archives/{id}", s.updateArchive)
	a("DELETE /api/archives/{id}", s.deleteArchive)
	a("POST /api/archives/{id}/check", s.checkArchive)
	a("POST /api/archives/{id}/eject", s.ejectArchive)
	a("POST /api/archives/import", s.importArchive)
	a("GET /api/pbs/{id}/foreign", s.foreignPartitions)
	a("GET /api/pbs/{id}/foreign/scan", s.scanForeign)
	a("GET /api/passthrough", s.passthroughInfo)
	a("POST /api/passthrough", s.passthrough)

	m.Handle("/", s.static())
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func fieldErrs(w http.ResponseWriter, errs map[string]string) bool {
	if len(errs) == 0 {
		return false
	}
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "Bitte die markierten Felder prüfen.", "fields": errs})
	return true
}

func fail(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "Nicht gefunden.")
		return
	}
	writeErr(w, http.StatusBadGateway, err.Error())
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "Ungültige Anfrage: "+err.Error())
		return false
	}
	return true
}

func ok(w http.ResponseWriter) { writeJSON(w, http.StatusOK, map[string]any{"ok": true}) }

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func remoteIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// clientIP is the address of the caller: behind a trusted reverse proxy the last address in
// X-Forwarded-For that is not a trusted proxy itself.
func (s *Server) clientIP(r *http.Request) string {
	ip := remoteIP(r)
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" || !s.C.TrustsProxy(ip) {
		return ip
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.TrimSpace(parts[i])
		if net.ParseIP(p) == nil {
			break
		}
		ip = p
		if !s.C.TrustsProxy(p) {
			break
		}
	}
	return ip
}

// ---- auth ----

func (s *Server) auth(next http.HandlerFunc) http.Handler {
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
		ctx := context.WithValue(r.Context(), ctxKey{}, u)
		next(w, r.WithContext(ctx))
	})
}

func currentUser(r *http.Request) store.User {
	u, _ := r.Context().Value(ctxKey{}).(store.User)
	return u
}

// SetupCodePath is the file holding the one-time setup code.
func SetupCodePath(dataDir string) string { return filepath.Join(dataDir, "setup-code") }

func (s *Server) setupCode() string {
	b, err := os.ReadFile(SetupCodePath(s.C.DataDir))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	n, _ := s.C.St.UserCount()
	resp := map[string]any{"setup": n == 0, "version": s.C.Version}
	if c, err := r.Cookie(cookieName); err == nil {
		if u, err := s.C.St.SessionUser(c.Value); err == nil {
			resp["user"] = u.Username
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) limited(ip string) bool {
	s.limMu.Lock()
	defer s.limMu.Unlock()
	var keep []time.Time
	for _, t := range s.fails[ip] {
		if time.Since(t) < 15*time.Minute {
			keep = append(keep, t)
		}
	}
	s.fails[ip] = keep
	return len(keep) >= 10
}

func (s *Server) failed(ip string) {
	s.limMu.Lock()
	s.fails[ip] = append(s.fails[ip], time.Now())
	s.limMu.Unlock()
}

func (s *Server) startSession(w http.ResponseWriter, userID int64) error {
	tok := randomToken(32)
	if err := s.C.St.CreateSession(tok, userID, sessionTTL); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true, Secure: s.Secure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds())})
	return nil
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if s.limited(ip) {
		writeErr(w, http.StatusTooManyRequests, "Zu viele Fehlversuche. Bitte 15 Minuten warten.")
		return
	}
	var req struct{ Username, Password string }
	if !decode(w, r, &req) {
		return
	}
	u, err := s.C.St.UserByName(strings.TrimSpace(req.Username))
	if err != nil || bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(req.Password)) != nil {
		s.failed(ip)
		time.Sleep(400 * time.Millisecond)
		writeErr(w, http.StatusUnauthorized, "Benutzername oder Passwort falsch.")
		return
	}
	if loadTOTP(s.C.St, u.ID).Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"totp": true, "ticket": s.newTicket(u.ID)})
		return
	}
	if err := s.startSession(w, u.ID); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u.Username})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		_ = s.C.St.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteStrictMode})
	ok(w)
}

func validPassword(pw string) string {
	if len([]rune(pw)) < 10 {
		return "Mindestens 10 Zeichen."
	}
	return ""
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if s.limited(ip) {
		writeErr(w, http.StatusTooManyRequests, "Zu viele Fehlversuche. Bitte 15 Minuten warten.")
		return
	}
	n, _ := s.C.St.UserCount()
	if n > 0 {
		writeErr(w, http.StatusConflict, "Die Einrichtung ist bereits abgeschlossen.")
		return
	}
	var req struct{ Code, Username, Password string }
	if !decode(w, r, &req) {
		return
	}
	code := s.setupCode()
	if code == "" || subtle.ConstantTimeCompare([]byte(strings.ToUpper(strings.TrimSpace(req.Code))), []byte(code)) != 1 {
		s.failed(ip)
		fieldErrs(w, map[string]string{"code": "Einrichtungscode stimmt nicht. Er steht auf der Konsole der VM."})
		return
	}
	errs := map[string]string{}
	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 || strings.ContainsAny(req.Username, " \t") {
		errs["username"] = "Mindestens 3 Zeichen, ohne Leerzeichen."
	}
	if m := validPassword(req.Password); m != "" {
		errs["password"] = m
	}
	if fieldErrs(w, errs) {
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
	id, err := s.C.St.CreateUser(req.Username, string(hash))
	if err != nil {
		fail(w, err)
		return
	}
	_ = os.Remove(SetupCodePath(s.C.DataDir))
	_ = s.startSession(w, id)
	s.C.Event("info", "Appliance", "Einrichtung abgeschlossen", "Konto „"+req.Username+"“ angelegt.")
	writeJSON(w, http.StatusOK, map[string]any{"user": req.Username})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decode(w, r, &req) {
		return
	}
	u := currentUser(r)
	if bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(req.Current)) != nil {
		fieldErrs(w, map[string]string{"current": "Aktuelles Passwort stimmt nicht."})
		return
	}
	if m := validPassword(req.New); m != "" {
		fieldErrs(w, map[string]string{"new": m})
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.New), 12)
	c, _ := r.Cookie(cookieName)
	if err := s.C.St.SetPassword(u.ID, string(hash), c.Value); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

// ---- static ----

func (s *Server) static() http.Handler {
	files := http.FileServerFS(s.Web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeErr(w, http.StatusNotFound, "Unbekannter Pfad.")
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" || !strings.Contains(p, ".") {
			// SPA: every page path serves index.html
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			w.Header().Set("Cache-Control", "no-cache")
			files.ServeHTTP(w, r2)
			return
		}
		if strings.HasPrefix(p, "fonts/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

var _ = log.Printf
