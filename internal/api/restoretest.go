package api

import (
	"net/http"
	"os"
	"regexp"

	"pk95proxbackup/internal/core"
)

var fileNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func (s *Server) listRestoreTests(w http.ResponseWriter, r *http.Request) {
	plans, err := s.C.RestoreTestPlans()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": plans, "runs": s.C.RestoreTestRuns(), "active": s.C.ActiveRestoreTest()})
}

func (s *Server) saveRestoreTest(w http.ResponseWriter, r *http.Request) {
	var p core.RestoreTestPlan
	if !decode(w, r, &p) {
		return
	}
	id := r.PathValue("id")
	if id != "" {
		var cur core.RestoreTestPlan
		if err := s.C.St.Get(core.KindRTPlan, id, &cur); err != nil {
			fail(w, err)
			return
		}
		p.ID = id
	} else {
		p.ID = core.NewID(3)
	}
	errs := p.Normalize()
	if _, err := s.C.Cluster(p.Cluster); err != nil && errs["node"] == "" {
		errs["node"] = "Proxmox VE wählen."
	}
	if _, err := s.C.Server(p.PBS); err != nil && errs["store"] == "" {
		errs["store"] = "Backup-Server wählen."
	}
	if fieldErrs(w, errs) {
		return
	}
	if err := s.C.St.Put(core.KindRTPlan, p.ID, p); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": p.ID})
}

func (s *Server) deleteRestoreTest(w http.ResponseWriter, r *http.Request) {
	if err := s.C.St.Delete(core.KindRTPlan, r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) runRestoreTest(w http.ResponseWriter, r *http.Request) {
	id, err := s.C.StartRestoreTest(r.PathValue("id"), currentUser(r).Username)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.C.Event("info", "Wiederherstellungstest", "Wiederherstellungstest gestartet", "von "+currentUser(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"run": id})
}

func (s *Server) cancelRestoreTest(w http.ResponseWriter, r *http.Request) {
	if !s.C.CancelRestoreTest() {
		writeErr(w, http.StatusConflict, "Es läuft kein Test.")
		return
	}
	ok(w)
}

func (s *Server) restoreTestScreenshot(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	run, file := q.Get("run"), q.Get("file")
	if !fileNameRe.MatchString(run) || !fileNameRe.MatchString(file) {
		writeErr(w, http.StatusBadRequest, "Ungültig.")
		return
	}
	b, err := os.ReadFile(s.C.ScreenshotPath(run, file))
	if err != nil {
		writeErr(w, http.StatusNotFound, "Kein Bild.")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(b)
}
