package api

import (
	"context"
	"net/http"
	"time"

	"pk95proxbackup/internal/prox"
)

// prunePreview shows what a cleanup with the entered retention would keep (nothing is deleted).
func (s *Server) prunePreview(w http.ResponseWriter, r *http.Request) {
	var rt retention
	if !decode(w, r, &rt) {
		return
	}
	errs := map[string]string{}
	for _, v := range []int{rt.KeepLast, rt.KeepDaily, rt.KeepWeekly, rt.KeepMonthly, rt.KeepYearly} {
		if v < 0 || v > 10000 {
			errs["keep_last"] = "Werte von 0 bis 10000."
		}
	}
	if fieldErrs(w, errs) {
		return
	}
	k := prox.Keep{Last: rt.KeepLast, Daily: rt.KeepDaily, Weekly: rt.KeepWeekly, Monthly: rt.KeepMonthly, Yearly: rt.KeepYearly}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	out, err := s.C.PrunePreview(ctx, r.PathValue("id"), r.PathValue("store"), k)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
