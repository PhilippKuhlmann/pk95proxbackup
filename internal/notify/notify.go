// Package notify sends notifications by e-mail (SMTP) and to a webhook (ntfy, Gotify, generic JSON).
package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Settings configure the channels.
type Settings struct {
	MinSeverity string `json:"min_severity"` // info, warning, critical

	MailEnabled bool   `json:"mail_enabled"`
	SMTPHost    string `json:"smtp_host"`
	SMTPPort    int    `json:"smtp_port"`
	SMTPSec     string `json:"smtp_security"` // starttls, tls, none
	SMTPUser    string `json:"smtp_user"`
	SMTPPass    string `json:"smtp_password,omitempty"`
	MailFrom    string `json:"mail_from"`
	MailTo      string `json:"mail_to"`

	HookEnabled bool   `json:"hook_enabled"`
	HookKind    string `json:"hook_kind"` // ntfy, gotify, json
	HookURL     string `json:"hook_url"`
	HookToken   string `json:"hook_token,omitempty"`
}

// Defaults are the settings on first start.
func Defaults() Settings {
	return Settings{MinSeverity: "warning", SMTPPort: 587, SMTPSec: "starttls", HookKind: "ntfy"}
}

// Message is one notification.
type Message struct {
	Severity string
	Source   string
	Title    string
	Body     string
	Details  *Details
	Host     string // name of the appliance
	URL      string // address of the web UI
	Version  string
	Time     time.Time
}

var rank = map[string]int{"info": 0, "warning": 1, "critical": 2}

// Wants reports whether a message of that severity should be sent.
func (s Settings) Wants(sev string) bool { return rank[sev] >= rank[s.MinSeverity] }

// Send delivers the message to all enabled channels and joins their errors.
func Send(ctx context.Context, s Settings, m Message) error {
	var errs []error
	if s.MailEnabled {
		if err := sendMail(s, m); err != nil {
			errs = append(errs, fmt.Errorf("E-Mail: %w", err))
		}
	}
	if s.HookEnabled {
		if err := sendHook(ctx, s, m); err != nil {
			errs = append(errs, fmt.Errorf("Webhook: %w", err))
		}
	}
	return errors.Join(errs...)
}

func sendMail(s Settings, m Message) error {
	if s.SMTPHost == "" || s.MailTo == "" || s.MailFrom == "" {
		return errors.New("Server, Absender und Empfänger angeben")
	}
	addr := net.JoinHostPort(s.SMTPHost, strconv.Itoa(s.SMTPPort))
	var to []string
	for _, t := range strings.Split(s.MailTo, ",") {
		if t = strings.TrimSpace(t); t != "" {
			to = append(to, t)
		}
	}
	var msg bytes.Buffer
	boundary := "pk95-" + fmt.Sprint(time.Now().UnixNano())
	fmt.Fprintf(&msg, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n",
		s.MailFrom, strings.Join(to, ", "), mimeHeader(Subject(m)), time.Now().Format(time.RFC1123Z), boundary)
	for _, part := range []struct{ ctype, body string }{{"text/plain", Text(m)}, {"text/html", HTML(m)}} {
		fmt.Fprintf(&msg, "--%s\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n", boundary, part.ctype)
		enc := b64(part.body)
		for len(enc) > 76 {
			msg.WriteString(enc[:76] + "\r\n")
			enc = enc[76:]
		}
		msg.WriteString(enc + "\r\n")
	}
	fmt.Fprintf(&msg, "--%s--\r\n", boundary)

	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	tlsCfg := &tls.Config{ServerName: s.SMTPHost, MinVersion: tls.VersionTLS12}
	if s.SMTPSec == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	c, err := smtp.NewClient(conn, s.SMTPHost)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if s.SMTPSec == "starttls" {
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if s.SMTPUser != "" {
		if err := c.Auth(smtp.PlainAuth("", s.SMTPUser, s.SMTPPass, s.SMTPHost)); err != nil {
			return fmt.Errorf("Anmeldung: %w", err)
		}
	}
	if err := c.Mail(s.MailFrom); err != nil {
		return err
	}
	for _, t := range to {
		if err := c.Rcpt(t); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg.Bytes()); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func mimeHeader(s string) string {
	for _, r := range s {
		if r > 127 {
			return "=?utf-8?B?" + b64(s) + "?="
		}
	}
	return s
}

func sendHook(ctx context.Context, s Settings, m Message) error {
	if s.HookURL == "" {
		return errors.New("keine URL")
	}
	var req *http.Request
	var err error
	prio := map[string]string{"info": "3", "warning": "4", "critical": "5"}[m.Severity]
	switch s.HookKind {
	case "ntfy":
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, s.HookURL, strings.NewReader(hookText(m)))
		if err == nil {
			req.Header.Set("Title", mimeHeader("PK95 ProxBackup: "+m.Title))
			req.Header.Set("Priority", prio)
			req.Header.Set("Tags", map[string]string{"ok": "white_check_mark", "warning": "warning", "error": "x"}[m.Details.StatusOf(m.Severity)])
			if m.URL != "" {
				req.Header.Set("Click", m.URL+linkPath(m))
			}
			if s.HookToken != "" {
				req.Header.Set("Authorization", "Bearer "+s.HookToken)
			}
		}
	case "gotify":
		body, _ := json.Marshal(map[string]any{"title": "PK95 ProxBackup: " + m.Title, "message": hookText(m), "priority": map[string]int{"info": 2, "warning": 5, "critical": 8}[m.Severity]})
		u := s.HookURL
		if s.HookToken != "" {
			sep := "?"
			if strings.Contains(u, "?") {
				sep = "&"
			}
			u += sep + "token=" + s.HookToken
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	default:
		body, _ := json.Marshal(map[string]any{"source": "pk95proxbackup", "severity": m.Severity, "status": m.Details.StatusOf(m.Severity),
			"title": m.Title, "body": m.Body, "details": m.Details, "host": m.Host, "ts": time.Now().Unix()})
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, s.HookURL, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			if s.HookToken != "" {
				req.Header.Set("Authorization", "Bearer "+s.HookToken)
			}
		}
	}
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// hookText is the push message: the text version without the title (it is the push title).
func hookText(m Message) string {
	t := Text(m)
	if i := strings.Index(t, "\n"); i >= 0 {
		t = t[i+1:]
	}
	if len(t) > 3500 {
		t = t[:3500] + "\n…"
	}
	return strings.TrimSpace(t)
}
