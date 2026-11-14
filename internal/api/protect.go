package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"pk95proxbackup/internal/core"
)

// getProtect returns the settings of the delete protection and the delete right of every PBS
// storage in Proxmox VE.
func (s *Server) getProtect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	guards, errs := s.C.StorageGuards(ctx)
	if errs == nil {
		errs = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": s.C.ProtectSettings(), "auto": s.C.AutoProtectInfo(), "storages": guards, "errors": errs})
}

func (s *Server) putProtect(w http.ResponseWriter, r *http.Request) {
	var req core.ProtectSettings
	if !decode(w, r, &req) {
		return
	}
	old := s.C.ProtectSettings()
	if err := s.C.SetProtectSettings(req); err != nil {
		fieldErrs(w, map[string]string{"months": err.Error()})
		return
	}
	if old != req {
		sev := "info"
		if req.Months < old.Months || req.Years < old.Years || (old.NewStorages && !req.NewStorages) {
			sev = "warning"
		}
		s.C.Event(sev, "Appliance", "Löschschutz geändert", describeProtect(req)+" (von "+currentUser(r).Username+").")
	}
	ok(w)
}

func describeProtect(p core.ProtectSettings) string {
	var out string
	switch {
	case p.Months > 0 && p.Years > 0:
		out = "Geschützt werden die letzte Sicherung jedes Monats für " + strconv.Itoa(p.Months) + " Monate und jedes Jahres für " + strconv.Itoa(p.Years) + " Jahre"
	case p.Months > 0:
		out = "Geschützt wird die letzte Sicherung jedes Monats für " + strconv.Itoa(p.Months) + " Monate"
	case p.Years > 0:
		out = "Geschützt wird die letzte Sicherung jedes Jahres für " + strconv.Itoa(p.Years) + " Jahre"
	default:
		out = "Keine automatisch geschützten Monats- und Jahresstände"
	}
	if p.NewStorages {
		out += "; neue Speicher in Proxmox VE ohne Löschrecht"
	}
	return out
}

// putStorageGuard switches the delete right of one storage.
func (s *Server) putStorageGuard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Protected bool `json:"protected"`
	}
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	g, err := s.C.SetStorageGuard(ctx, r.PathValue("cluster"), r.PathValue("storage"), req.Protected, currentUser(r).Username)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, g)
}

// runProtect applies the automatic protection now.
func (s *Server) runProtect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	if err := s.C.RunAutoProtect(ctx); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.C.AutoProtectInfo())
}
