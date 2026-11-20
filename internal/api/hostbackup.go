package api

import (
	"context"
	"net/http"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/hostexec"
)

// getHostBackups shows the host backup of every Proxmox VE cluster.
func (s *Server) getHostBackups(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, map[string]any{"clusters": s.C.HostBackups(ctx), "ssh_key": hostexec.PublicKey(s.C.Signer)})
}

func (s *Server) putHostBackup(w http.ResponseWriter, r *http.Request) {
	var conf core.HostBackupConf
	if !decode(w, r, &conf) {
		return
	}
	errs, err := s.C.SetHostBackup(r.Context(), r.PathValue("cluster"), conf)
	if err != nil {
		fail(w, err)
		return
	}
	if fieldErrs(w, errs) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) runHostBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Node string `json:"node"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.C.StartHostBackup(r.PathValue("cluster"), req.Node); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) checkHostBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Node string `json:"node"`
	}
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	res, err := s.C.CheckHostAccess(ctx, r.PathValue("cluster"), req.Node)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
