// Package prox talks to the REST APIs of Proxmox Backup Server and Proxmox VE (api2/json).
// TLS certificates are pinned by their SHA-256 fingerprint (both products use self-signed
// certificates by default), so no CA is needed and a changed certificate is an error.
package prox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is a connection to one PBS or PVE API.
type Client struct {
	base   string // https://host:port/api2/json
	auth   string // value of the Authorization header
	http   *http.Client
	slow   *http.Client // no overall timeout: downloads and slow calls, bounded by the context
	Host   string
	Port   int
	Finger string
}

// APIError is a non-2xx answer of the API.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// NormalizeFingerprint makes "aa:BB:..." / "aabb..." comparable.
func NormalizeFingerprint(f string) string {
	f = strings.ToLower(strings.TrimSpace(f))
	f = strings.ReplaceAll(f, ":", "")
	f = strings.ReplaceAll(f, " ", "")
	return f
}

// FormatFingerprint renders a hex fingerprint as AA:BB:… (the form PBS and PVE show).
func FormatFingerprint(f string) string {
	f = NormalizeFingerprint(f)
	var parts []string
	for i := 0; i+2 <= len(f); i += 2 {
		parts = append(parts, f[i:i+2])
	}
	return strings.Join(parts, ":")
}

func certFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return fmt.Sprintf("%x", sum[:])
}

// FetchFingerprint connects to host:port and returns the SHA-256 fingerprint of the server
// certificate (trust on first use: the UI shows it for confirmation).
func FetchFingerprint(ctx context.Context, host string, port int) (string, error) {
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 8 * time.Second}, Config: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // only reads the certificate
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return "", fmt.Errorf("keine Verbindung zu %s:%d: %w", host, port, err)
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("der Server hat kein Zertifikat geschickt")
	}
	return FormatFingerprint(certFingerprint(certs[0].Raw)), nil
}

func pinnedTransport(fingerprint string) *http.Transport {
	want := NormalizeFingerprint(fingerprint)
	return &http.Transport{
		Proxy:               nil,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     60 * time.Second,
		MaxIdleConnsPerHost: 4,
		DialContext:         (&net.Dialer{Timeout: 8 * time.Second}).DialContext,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // verified below by pinning
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				if len(raw) == 0 {
					return errors.New("kein Zertifikat")
				}
				if got := certFingerprint(raw[0]); got != want {
					return fmt.Errorf("Zertifikat passt nicht zum hinterlegten Fingerabdruck (jetzt %s)", FormatFingerprint(got))
				}
				return nil
			},
		},
	}
}

func newClient(host string, port int, fingerprint, auth string) *Client {
	tr := pinnedTransport(fingerprint)
	return &Client{
		base:   "https://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/api2/json",
		auth:   auth,
		http:   &http.Client{Transport: tr, Timeout: 60 * time.Second},
		slow:   &http.Client{Transport: tr},
		Host:   host,
		Port:   port,
		Finger: fingerprint,
	}
}

// Params are API parameters. Values are formatted with fmt; bools as 0/1 for PVE and
// true/false for PBS is handled by the caller (both accept 0/1 and true/false for booleans).
type Params map[string]any

func (p Params) encode() url.Values {
	v := url.Values{}
	for k, val := range p {
		switch x := val.(type) {
		case nil:
		case bool:
			if x {
				v.Set(k, "1")
			} else {
				v.Set(k, "0")
			}
		case []string:
			for _, s := range x {
				v.Add(k, s)
			}
		default:
			v.Set(k, fmt.Sprint(x))
		}
	}
	return v
}

// Do calls the API and decodes the "data" member of the answer into out (may be nil).
func (c *Client) Do(ctx context.Context, method, path string, params Params, out any) error {
	_, err := c.DoTotal(ctx, method, path, params, out)
	return err
}

// DoTotal is Do and also returns the "total" member (task logs and lists report it).
func (c *Client) DoTotal(ctx context.Context, method, path string, params Params, out any) (int, error) {
	return c.do(ctx, c.http, method, path, params, out)
}

// DoSlow is Do without the 60 s limit (the context bounds it), for calls that may take minutes.
func (c *Client) DoSlow(ctx context.Context, method, path string, params Params, out any) error {
	_, err := c.do(ctx, c.slow, method, path, params, out)
	return err
}

// Stream sends a GET and returns the response for streaming (2xx only; the caller closes it).
func (c *Client) Stream(ctx context.Context, path string, params Params) (*http.Response, error) {
	u := c.base + path
	if vals := params.encode(); len(vals) > 0 {
		u += "?" + vals.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.auth)
	resp, err := c.slow.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s nicht erreichbar: %w", c.Host, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, &APIError{Status: resp.StatusCode, Message: apiMessage(resp, raw)}
	}
	return resp, nil
}

func (c *Client) do(ctx context.Context, client *http.Client, method, path string, params Params, out any) (int, error) {
	u := c.base + path
	var body io.Reader
	vals := params.encode()
	if method == http.MethodGet || method == http.MethodDelete {
		if len(vals) > 0 {
			u += "?" + vals.Encode()
		}
	} else {
		body = strings.NewReader(vals.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", c.auth)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, fmt.Errorf("%s nicht erreichbar: %w", c.Host, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return 0, &APIError{Status: resp.StatusCode, Message: apiMessage(resp, raw)}
	}
	if out == nil {
		return 0, nil
	}
	var env struct {
		Data  json.RawMessage `json:"data"`
		Total int             `json:"total"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return 0, fmt.Errorf("unerwartete Antwort von %s: %w", c.Host, err)
	}
	if len(env.Data) == 0 || bytes.Equal(env.Data, []byte("null")) {
		return env.Total, nil
	}
	return env.Total, json.Unmarshal(env.Data, out)
}

func apiMessage(resp *http.Response, raw []byte) string {
	var env struct {
		Message string            `json:"message"`
		Errors  map[string]string `json:"errors"`
	}
	msg := ""
	if json.Unmarshal(raw, &env) == nil {
		msg = strings.TrimSpace(env.Message)
		for k, v := range env.Errors {
			msg = strings.TrimSpace(msg + " " + k + ": " + strings.TrimSpace(v))
		}
	}
	if msg == "" {
		// PVE puts the reason into the status line.
		if i := strings.Index(resp.Status, " "); i > 0 {
			msg = strings.TrimSpace(resp.Status[i+1:])
		}
	}
	if msg == "" {
		msg = strings.TrimSpace(string(raw))
	}
	if resp.StatusCode == 401 {
		return "Anmeldung abgelehnt (Token oder Secret falsch): " + msg
	}
	if resp.StatusCode == 403 {
		return "Keine Berechtigung: " + msg
	}
	return msg
}

// IsStatus reports whether err is an API error with that HTTP status.
func IsStatus(err error, status int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == status
}

// UPIDNode returns the node name of a task id (UPID:node:...).
func UPIDNode(upid string) string {
	parts := strings.Split(upid, ":")
	if len(parts) > 2 && parts[0] == "UPID" {
		return parts[1]
	}
	return ""
}

// TaskLine is one line of a task log.
type TaskLine struct {
	N int    `json:"n"`
	T string `json:"t"`
}

// TaskStatus is the state of a task.
type TaskStatus struct {
	Status     string `json:"status"` // running, stopped
	ExitStatus string `json:"exitstatus"`
	Type       string `json:"type"`
	ID         string `json:"id"`
	StartTime  int64  `json:"starttime"`
}

// WaitTask polls a task until it is stopped. statusPath is ".../tasks/<upid>/status".
func (c *Client) waitTask(ctx context.Context, statusPath string, timeout time.Duration) (TaskStatus, error) {
	deadline := time.Now().Add(timeout)
	for {
		var st TaskStatus
		if err := c.Do(ctx, http.MethodGet, statusPath, nil, &st); err != nil {
			return st, err
		}
		if st.Status == "stopped" {
			if st.ExitStatus != "OK" && !strings.HasPrefix(st.ExitStatus, "WARNINGS") {
				return st, fmt.Errorf("Aufgabe fehlgeschlagen: %s", st.ExitStatus)
			}
			return st, nil
		}
		if time.Now().After(deadline) {
			return st, errors.New("Aufgabe läuft noch (Zeitlimit erreicht), Stand unter Aufgaben")
		}
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
