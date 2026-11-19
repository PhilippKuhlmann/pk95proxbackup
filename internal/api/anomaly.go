package api

import (
	"net/http"

	"pk95proxbackup/internal/core"
)

type anomalyView struct {
	core.BackupAnomaly
	Text string `json:"text"`
}

func anomalyViews(list []core.BackupAnomaly) []anomalyView {
	out := make([]anomalyView, 0, len(list))
	for _, a := range list {
		out = append(out, anomalyView{BackupAnomaly: a, Text: core.AnomalyText(a)})
	}
	return out
}

// listAnomalies returns the unusual backups (open and answered).
func (s *Server) listAnomalies(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"anomalies": anomalyViews(s.C.Anomalies())})
}

// ackAnomaly marks an unusual backup as checked.
func (s *Server) ackAnomaly(w http.ResponseWriter, r *http.Request) {
	if err := s.C.AckAnomaly(r.Context(), r.PathValue("id"), currentUser(r).Username); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}
