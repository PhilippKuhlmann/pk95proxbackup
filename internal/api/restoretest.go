package api

import (
	"net/http"
	"os"
	"regexp"
	"time"

	"pk95proxbackup/internal/core"
)

// fileNameRe accepts one plain file name: no leading dot, so "." and ".."
// cannot pass.
var fileNameRe = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*$`)

func (s *Server) listRestoreTests(w http.ResponseWriter, r *http.Request) {
	plans, err := s.C.RestoreTestPlans()
	if err != nil {
		fail(w, err)
		return
	}
	type planView struct {
		core.RestoreTestPlan
		ScheduleText string  `json:"schedule_text"`
		Next         []int64 `json:"next"`
	}
	views := []planView{}
	for _, p := range plans {
		views = append(views, planView{RestoreTestPlan: p, ScheduleText: p.ScheduleText(), Next: p.NextRuns(time.Now(), 3)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": views, "runs": s.C.RestoreTestRuns(), "active": s.C.ActiveRestoreTest(), "kept": s.C.KeptGuests()})
}

// previewSchedule describes a schedule and its next dates (while editing a plan).
func (s *Server) previewSchedule(w http.ResponseWriter, r *http.Request) {
	var p core.RestoreTestPlan
	if !decode(w, r, &p) {
		return
	}
	errs := map[string]string{}
	p.Name, p.Cluster, p.Node, p.Storage, p.PBS, p.Store = "x", "x", "x", "x", "x", "x"
	for k, v := range p.Normalize() {
		if k == "weekdays" || k == "time" || k == "month_day" || k == "month_weekday" || k == "start_date" {
			errs[k] = v
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": p.ScheduleText(), "next": p.NextRuns(time.Now(), 6), "errors": errs})
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

// deleteRestoreTest removes a plan and the test guests it kept.
func (s *Server) deleteRestoreTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.C.DeleteKept(id, currentUser(r).Username); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.C.St.Delete(core.KindRTPlan, id); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

// deleteKept removes the test guests a plan kept (in the background).
func (s *Server) deleteKept(w http.ResponseWriter, r *http.Request) {
	if err := s.C.DeleteKept(r.PathValue("id"), currentUser(r).Username); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
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
	path, found := s.C.RecordedScreenshot(run, file)
	if !found {
		writeErr(w, http.StatusNotFound, "Kein Bild.")
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		writeErr(w, http.StatusNotFound, "Kein Bild.")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(b)
}
