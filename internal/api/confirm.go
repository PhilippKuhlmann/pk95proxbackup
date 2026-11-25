package api

import (
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"pk95proxbackup/internal/store"
)

// Deleting backups needs the password once more. POST /api/account/confirm checks it (with the
// limit on failed attempts of the login) and hands out a token that is good once, for two
// minutes, for this account and this session; the delete routes want it in X-PK95-Confirm.

const confirmTTL = 2 * time.Minute

type confirmTicket struct {
	user    int64
	session string
	until   time.Time
}

func sessionCookie(r *http.Request) string {
	if c, err := r.Cookie(cookieName); err == nil {
		return c.Value
	}
	return ""
}

func (s *Server) confirmPassword(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	done, allowed := s.attempt(ip)
	if !allowed {
		writeErr(w, http.StatusTooManyRequests, "Zu viele Fehlversuche. Bitte 15 Minuten warten.")
		return
	}
	bad := false
	defer func() { done(!bad) }()
	var req struct{ Password string }
	if !decode(w, r, &req) {
		return
	}
	cur := currentUser(r)
	u, err := s.C.St.UserByName(cur.Username)
	if err != nil || u.ID != cur.ID || bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(req.Password)) != nil {
		bad = true
		_ = s.C.St.AddAudit(store.AuditEntry{TS: time.Now().Unix(), User: cur.Username, IP: ip, Action: "Passwortbestätigung fehlgeschlagen", Status: http.StatusForbidden})
		time.Sleep(400 * time.Millisecond)
		// not 401: that would sign the browser out
		writeErr(w, http.StatusForbidden, "Das Passwort stimmt nicht.")
		return
	}
	tok := randomToken(24)
	now := time.Now()
	s.limMu.Lock()
	for k, t := range s.confirms {
		if now.After(t.until) {
			delete(s.confirms, k)
		}
	}
	s.confirms[tok] = &confirmTicket{user: u.ID, session: sessionCookie(r), until: now.Add(confirmTTL)}
	s.limMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"token": tok})
}

// takeConfirm uses up the confirmation token of the request; false (and an answer written) when
// there is none that fits.
func (s *Server) takeConfirm(w http.ResponseWriter, r *http.Request) bool {
	tok := r.Header.Get("X-PK95-Confirm")
	s.limMu.Lock()
	t := s.confirms[tok]
	delete(s.confirms, tok)
	s.limMu.Unlock()
	if tok == "" || t == nil || t.user != currentUser(r).ID || t.session != sessionCookie(r) || time.Now().After(t.until) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Zum Löschen bitte das Passwort noch einmal eingeben.", "confirm_required": true})
		return false
	}
	return true
}
