package api

import (
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"pk95proxbackup/internal/store"
	"pk95proxbackup/internal/totp"
)

// Two-factor sign-in with an authenticator app (TOTP). After the password, the login answers with a
// short-lived ticket; the code (or a recovery code) is then sent with the ticket to /api/login/totp.

const (
	totpIssuer    = "PK95 ProxBackup"
	ticketTTL     = 5 * time.Minute
	ticketTries   = 5
	recoveryCount = 10
)

// TOTPState is stored per user under the setting "totp:<user id>".
type TOTPState struct {
	Secret   string   `json:"secret,omitempty"`
	Enabled  bool     `json:"enabled"`
	Since    int64    `json:"since,omitempty"`
	LastStep int64    `json:"last_step,omitempty"` // last accepted time step (no code twice)
	Recovery []string `json:"recovery,omitempty"`  // SHA-256 of unused recovery codes
	Pending  string   `json:"pending,omitempty"`   // secret being set up, not confirmed yet
}

type loginTicket struct {
	user  int64
	until time.Time
	tries int
}

// totpMu serializes read-modify-write of TOTP states (used steps, recovery codes).
var totpMu sync.Mutex

// TOTPKey is the setting key of a user's TOTP state.
func TOTPKey(userID int64) string { return "totp:" + strconv.FormatInt(userID, 10) }

func loadTOTP(st *store.Store, userID int64) TOTPState {
	var t TOTPState
	_ = st.GetSetting(TOTPKey(userID), &t)
	return t
}

// DisableTOTP switches two-factor sign-in off for a user (console rescue: pk95proxbackup -totp-off).
func DisableTOTP(st *store.Store, username string) error {
	u, err := st.UserByName(username)
	if err != nil {
		return err
	}
	return st.DeleteSetting(TOTPKey(u.ID))
}

func (s *Server) newTicket(userID int64) string {
	tok := randomToken(24)
	s.limMu.Lock()
	defer s.limMu.Unlock()
	now := time.Now()
	for k, t := range s.tickets {
		if now.After(t.until) {
			delete(s.tickets, k)
		}
	}
	s.tickets[tok] = &loginTicket{user: userID, until: now.Add(ticketTTL)}
	return tok
}

// loginTOTP completes the login with the code of the authenticator app or a recovery code.
func (s *Server) loginTOTP(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if s.limited(ip) {
		writeErr(w, http.StatusTooManyRequests, "Zu viele Fehlversuche. Bitte 15 Minuten warten.")
		return
	}
	var req struct{ Ticket, Code string }
	if !decode(w, r, &req) {
		return
	}
	expired := "Anmeldung abgelaufen. Bitte Benutzername und Passwort erneut eingeben."
	s.limMu.Lock()
	t := s.tickets[req.Ticket]
	if t != nil && time.Now().After(t.until) {
		delete(s.tickets, req.Ticket)
		t = nil
	}
	s.limMu.Unlock()
	if t == nil {
		writeErr(w, http.StatusUnauthorized, expired)
		return
	}
	u, err := s.C.St.UserByID(t.user)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, expired)
		return
	}
	totpMu.Lock()
	st := loadTOTP(s.C.St, u.ID)
	usedRecovery, okCode := false, false
	if st.Enabled {
		if step, ok := totp.Verify(st.Secret, req.Code, time.Now(), st.LastStep); ok {
			st.LastStep, okCode = step, true
		} else if rest, ok := totp.UseRecovery(st.Recovery, req.Code); ok {
			st.Recovery, okCode, usedRecovery = rest, true, true
		}
		if okCode {
			_ = s.C.St.SetSetting(TOTPKey(u.ID), st)
		}
	} else {
		okCode = true // switched off in the meantime (console rescue): the password was enough
	}
	totpMu.Unlock()
	if !okCode {
		s.failed(ip)
		s.limMu.Lock()
		t.tries++
		gone := t.tries >= ticketTries
		if gone {
			delete(s.tickets, req.Ticket)
		}
		s.limMu.Unlock()
		time.Sleep(400 * time.Millisecond)
		if gone {
			writeErr(w, http.StatusUnauthorized, "Zu viele falsche Codes. Bitte erneut anmelden.")
			return
		}
		fieldErrs(w, map[string]string{"code": "Code stimmt nicht. Bitte den aktuellen Code aus der App eingeben."})
		return
	}
	s.limMu.Lock()
	delete(s.tickets, req.Ticket)
	s.limMu.Unlock()
	if err := s.startSession(w, u.ID); err != nil {
		fail(w, err)
		return
	}
	resp := map[string]any{"user": u.Username}
	if usedRecovery {
		resp["recovery_left"] = len(st.Recovery)
		s.C.Event("warning", "Appliance", "Anmeldung mit Wiederherstellungscode",
			"Konto „"+u.Username+"“ hat sich mit einem Wiederherstellungscode angemeldet. Übrig: "+strconv.Itoa(len(st.Recovery))+".")
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) getTOTP(w http.ResponseWriter, r *http.Request) {
	st := loadTOTP(s.C.St, currentUser(r).ID)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": st.Enabled, "since": st.Since, "recovery_left": len(st.Recovery)})
}

// setupTOTP creates a new secret to scan; it becomes active only after enableTOTP confirms a code.
func (s *Server) setupTOTP(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	totpMu.Lock()
	defer totpMu.Unlock()
	st := loadTOTP(s.C.St, u.ID)
	if st.Enabled {
		writeErr(w, http.StatusConflict, "Die Zwei-Faktor-Anmeldung ist bereits eingeschaltet.")
		return
	}
	st.Pending = totp.NewSecret()
	if err := s.C.St.SetSetting(TOTPKey(u.ID), st); err != nil {
		fail(w, err)
		return
	}
	account := u.Username
	if h, err := os.Hostname(); err == nil && h != "" {
		account += "@" + h
	}
	uri := totp.URI(totpIssuer, account, st.Pending)
	svg, err := totp.QRSVG(uri)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"secret": totp.GroupSecret(st.Pending), "uri": uri, "qr": svg, "account": account, "issuer": totpIssuer})
}

func (s *Server) enableTOTP(w http.ResponseWriter, r *http.Request) {
	var req struct{ Code string }
	if !decode(w, r, &req) {
		return
	}
	u := currentUser(r)
	totpMu.Lock()
	st := loadTOTP(s.C.St, u.ID)
	if st.Enabled {
		totpMu.Unlock()
		writeErr(w, http.StatusConflict, "Die Zwei-Faktor-Anmeldung ist bereits eingeschaltet.")
		return
	}
	if st.Pending == "" {
		totpMu.Unlock()
		writeErr(w, http.StatusBadRequest, "Bitte die Einrichtung neu starten.")
		return
	}
	step, ok := totp.Verify(st.Pending, req.Code, time.Now(), 0)
	if !ok {
		totpMu.Unlock()
		fieldErrs(w, map[string]string{"code": "Code stimmt nicht. Uhrzeit des Telefons prüfen und den aktuellen Code eingeben."})
		return
	}
	codes, hashes := totp.NewRecoveryCodes(recoveryCount)
	st = TOTPState{Secret: st.Pending, Enabled: true, Since: time.Now().Unix(), LastStep: step, Recovery: hashes}
	err := s.C.St.SetSetting(TOTPKey(u.ID), st)
	totpMu.Unlock()
	if err != nil {
		fail(w, err)
		return
	}
	if c, err := r.Cookie(cookieName); err == nil {
		_ = s.C.St.EndOtherSessions(u.ID, c.Value)
	}
	s.C.Event("info", "Appliance", "Zwei-Faktor-Anmeldung eingeschaltet", "Konto „"+u.Username+"“ meldet sich jetzt mit Passwort und Code aus der Authenticator-App an.")
	writeJSON(w, http.StatusOK, map[string]any{"recovery": codes})
}

// checkPassword verifies the current password for sensitive account changes.
func checkPassword(w http.ResponseWriter, u store.User, pw string) bool {
	if bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(pw)) != nil {
		time.Sleep(400 * time.Millisecond)
		fieldErrs(w, map[string]string{"password": "Passwort stimmt nicht."})
		return false
	}
	return true
}

func (s *Server) disableTOTP(w http.ResponseWriter, r *http.Request) {
	var req struct{ Password string }
	if !decode(w, r, &req) {
		return
	}
	u := currentUser(r)
	if !checkPassword(w, u, req.Password) {
		return
	}
	totpMu.Lock()
	err := s.C.St.DeleteSetting(TOTPKey(u.ID))
	totpMu.Unlock()
	if err != nil {
		fail(w, err)
		return
	}
	s.C.Event("warning", "Appliance", "Zwei-Faktor-Anmeldung ausgeschaltet", "Konto „"+u.Username+"“ meldet sich wieder nur mit dem Passwort an.")
	ok(w)
}

func (s *Server) newRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	var req struct{ Password string }
	if !decode(w, r, &req) {
		return
	}
	u := currentUser(r)
	if !checkPassword(w, u, req.Password) {
		return
	}
	totpMu.Lock()
	st := loadTOTP(s.C.St, u.ID)
	if !st.Enabled {
		totpMu.Unlock()
		writeErr(w, http.StatusBadRequest, "Die Zwei-Faktor-Anmeldung ist nicht eingeschaltet.")
		return
	}
	codes, hashes := totp.NewRecoveryCodes(recoveryCount)
	st.Recovery = hashes
	err := s.C.St.SetSetting(TOTPKey(u.ID), st)
	totpMu.Unlock()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery": codes})
}
