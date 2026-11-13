package api

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"pk95proxbackup/internal/core"
)

// Disk health, updates and the backup of the appliance's own configuration.

// ---- disk health ----

func (s *Server) listHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"disks": s.C.DiskHealths()})
}

func (s *Server) checkHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	s.C.CheckDisksNow(ctx)
	writeJSON(w, http.StatusOK, map[string]any{"disks": s.C.DiskHealths()})
}

func (s *Server) forgetHealth(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	for _, h := range s.C.DiskHealths() {
		if h.ID != id {
			continue
		}
		if h.Present {
			writeErr(w, http.StatusConflict, "Die Platte ist angeschlossen – sie würde sofort wieder erscheinen.")
			return
		}
		if err := s.C.ForgetDiskHealth(id); err != nil {
			fail(w, err)
			return
		}
		ok(w)
		return
	}
	writeErr(w, http.StatusNotFound, "Nicht gefunden.")
}

// ---- updates ----

func (s *Server) getUpdate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.C.Update())
}

func (s *Server) putUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AutoCheck bool `json:"auto_check"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.C.SetUpdateAutoCheck(req.AutoCheck); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.C.Update())
}

func (s *Server) checkUpdate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	info, err := s.C.CheckUpdate(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Nachsehen fehlgeschlagen: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) installUpdate(w http.ResponseWriter, r *http.Request) {
	if err := s.C.InstallUpdate(); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.C.Update())
}

// ---- configuration backup ----

func (s *Server) getSelfBackup(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.C.SelfBackup())
}

func (s *Server) putSelfBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled  bool   `json:"enabled"`
		Password string `json:"password"`
		Repeat   string `json:"repeat"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Password != "" {
		if m := core.ValidRecoveryPassword(req.Password); m != "" {
			fieldErrs(w, map[string]string{"password": m})
			return
		}
		if req.Password != req.Repeat {
			fieldErrs(w, map[string]string{"repeat": "Die beiden Eingaben stimmen nicht überein."})
			return
		}
	}
	before := s.C.SelfBackup()
	if err := s.C.SetSelfBackup(req.Enabled, req.Password); err != nil {
		fieldErrs(w, map[string]string{"password": err.Error()})
		return
	}
	if req.Password != "" && before.HasKey {
		s.C.Event("info", "Appliance", "Wiederherstellungs-Passwort geändert", "Ältere Sicherungen der Konfiguration lassen sich weiterhin nur mit dem bisherigen Passwort öffnen.")
	}
	// Switched on (or a new password): write a first backup right away.
	if req.Enabled && (!before.Enabled || req.Password != "") {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			_ = s.C.RunSelfBackup(ctx)
		}()
	}
	writeJSON(w, http.StatusOK, s.C.SelfBackup())
}

func (s *Server) runSelfBackup(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	if err := s.C.RunSelfBackup(ctx); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.C.SelfBackup())
}

func (s *Server) downloadSelfBackup(w http.ResponseWriter, r *http.Request) {
	data, man, err := s.C.ExportConfig()
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	name := fmt.Sprintf("pk95proxbackup-%s-%s.pk95cfg", safeName(man.Host), time.Unix(man.Created, 0).Format("2006-01-02-1504"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "appliance"
	}
	return b.String()
}

func (s *Server) foundSelfBackups(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	writeJSON(w, http.StatusOK, map[string]any{"backups": s.C.FindConfigBackups(ctx)})
}

const maxConfigUpload = 300 << 20

// readRestore reads the backup and the recovery password: a file upload (multipart) or a
// backup in a datastore ({server_id, store, time}).
func (s *Server) readRestore(w http.ResponseWriter, r *http.Request, allowStore bool) (data []byte, password, code string, ok bool) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, maxConfigUpload)
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeErr(w, http.StatusBadRequest, "Datei zu groß oder unvollständig übertragen.")
			return nil, "", "", false
		}
		password, code = r.FormValue("password"), r.FormValue("code")
		f, _, err := r.FormFile("file")
		if err != nil {
			fieldErrs(w, map[string]string{"file": "Bitte die Sicherungsdatei (.pk95cfg) wählen."})
			return nil, "", "", false
		}
		defer f.Close()
		data, err = io.ReadAll(f)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "Datei nicht lesbar.")
			return nil, "", "", false
		}
		return data, password, code, true
	}
	if !allowStore {
		writeErr(w, http.StatusBadRequest, "Bitte eine Datei hochladen.")
		return nil, "", "", false
	}
	var req struct {
		ServerID string `json:"server_id"`
		Store    string `json:"store"`
		Time     int64  `json:"time"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return nil, "", "", false
	}
	if req.Password == "" {
		fieldErrs(w, map[string]string{"password": "Wiederherstellungs-Passwort angeben."})
		return nil, "", "", false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	data, err := s.C.LoadConfigBackup(ctx, req.ServerID, req.Store, req.Time)
	if err != nil {
		fail(w, fmt.Errorf("Sicherung laden: %w", err))
		return nil, "", "", false
	}
	return data, req.Password, "", true
}

func (s *Server) restoreSelfBackup(w http.ResponseWriter, r *http.Request) {
	data, password, _, ok := s.readRestore(w, r, true)
	if !ok {
		return
	}
	s.doRestore(w, r, data, password)
}

// setupRestore sets up a new appliance from a backup instead of creating an account.
func (s *Server) setupRestore(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if s.limited(ip) {
		writeErr(w, http.StatusTooManyRequests, "Zu viele Fehlversuche. Bitte 15 Minuten warten.")
		return
	}
	if n, _ := s.C.St.UserCount(); n > 0 {
		writeErr(w, http.StatusConflict, "Die Einrichtung ist bereits abgeschlossen. Wiederherstellen unter Einstellungen.")
		return
	}
	data, password, code, ok := s.readRestore(w, r, false)
	if !ok {
		return
	}
	want := s.setupCode()
	if want == "" || subtle.ConstantTimeCompare([]byte(strings.ToUpper(strings.TrimSpace(code))), []byte(want)) != 1 {
		s.failed(ip)
		fieldErrs(w, map[string]string{"code": "Einrichtungscode stimmt nicht. Er steht auf der Konsole der VM."})
		return
	}
	s.doRestore(w, r, data, password)
}

func (s *Server) doRestore(w http.ResponseWriter, r *http.Request, data []byte, password string) {
	if password == "" {
		fieldErrs(w, map[string]string{"password": "Wiederherstellungs-Passwort angeben."})
		return
	}
	if s.C.Restart == nil {
		writeErr(w, http.StatusConflict, "Diese Installation kann sich nicht selbst neu starten.")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
	defer cancel()
	man, err := s.C.RestoreConfig(ctx, data, password)
	if err != nil {
		if strings.Contains(err.Error(), "Passwort") {
			s.failed(s.clientIP(r))
			fieldErrs(w, map[string]string{"password": err.Error()})
			return
		}
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"host": man.Host, "created": man.Created, "version": man.Version, "pbs": man.PBS})
}

// ---- state of the Backup Servers ----

func (s *Server) listServerHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"servers": s.C.ServerHealths()})
}

func (s *Server) checkServerHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	h, err := s.C.CheckServerHealth(ctx, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) reloadServer(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	h, err := s.C.ReloadServerServices(ctx, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}
