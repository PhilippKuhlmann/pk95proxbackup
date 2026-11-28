package api

import "net/http"

// Target times for the recovery of guests (see core/rto.go).

func (s *Server) getRTO(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.C.RTOSettingsOf())
}

// putRTO sets the target time for all guests without their own (0: none).
func (s *Server) putRTO(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Default int `json:"default"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.C.SetRTODefault(req.Default); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.C.RTOSettingsOf())
}

// putRTOGuest sets the target time of one guest (minutes null: as all others, -1: none).
func (s *Server) putRTOGuest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Minutes *int `json:"minutes"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.C.SetRTOGuest(r.PathValue("cluster"), r.PathValue("vmid"), req.Minutes); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.C.RTOSettingsOf())
}
