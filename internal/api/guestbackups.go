package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// guestBackups lists where the backups of a guest lie (before deleting them all).
func (s *Server) guestBackups(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	out, err := s.C.FindGuestBackups(ctx, q.Get("type"), q.Get("bid"))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteGuestBackups removes all backups of a guest from the chosen datastores. The ID has to
// be typed again (confirm) – a slip of the mouse deletes nothing.
func (s *Server) deleteGuestBackups(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	typ, id := q.Get("type"), q.Get("bid")
	if q.Get("confirm") != id || id == "" {
		writeErr(w, http.StatusUnprocessableEntity, "Zur Bestätigung die ID eintippen.")
		return
	}
	var places []string
	for _, p := range strings.Split(q.Get("places"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			places = append(places, p)
		}
	}
	if len(places) == 0 {
		writeErr(w, http.StatusUnprocessableEntity, "Keinen Datastore gewählt.")
		return
	}
	if !s.takeConfirm(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	res, err := s.C.DeleteGuestBackups(ctx, typ, id, places)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	kind := map[string]string{"vm": "VM", "ct": "Container"}[typ]
	word := "Sicherungen"
	if res.Deleted == 1 {
		word = "Sicherung"
	}
	body := fmt.Sprintf("%d %s von %s %s gelöscht (von %s).", res.Deleted, word, kind, id, currentUser(r).Username)
	if len(res.Kept) > 0 {
		body += " Geschützt und geblieben: " + strings.Join(res.Kept, ", ") + "."
	}
	if len(res.Failed) > 0 {
		body += " Nicht gelöscht: " + strings.Join(res.Failed, "; ") + "."
	}
	sev := "warning"
	if len(res.Failed) > 0 {
		sev = "critical"
	}
	s.C.Event(sev, "Sicherungen", fmt.Sprintf("Sicherungen von %s %s gelöscht", kind, id), body+" Der Platz wird beim nächsten „Speicher freigeben“ frei.")
	writeJSON(w, http.StatusOK, res)
}
