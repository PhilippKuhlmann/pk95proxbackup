package api

import (
	"context"
	"net/http"
	"time"
)

// prunePreview shows what a cleanup with the entered retention would keep (nothing is deleted).
func (s *Server) prunePreview(w http.ResponseWriter, r *http.Request) {
	var rt retention
	if !decode(w, r, &rt) {
		return
	}
	errs := map[string]string{}
	rt.validateKeep(errs)
	if fieldErrs(w, errs) {
		return
	}
	k := rt.keep()
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	out, err := s.C.PrunePreview(ctx, r.PathValue("id"), r.PathValue("store"), k)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
