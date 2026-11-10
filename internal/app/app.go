// Package app wires store, core, API and web UI together and runs the servers.
package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"pk95proxbackup/internal/api"
	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/fake"
	"pk95proxbackup/internal/hostexec"
	"pk95proxbackup/internal/store"
	"pk95proxbackup/web"
)

// Config is the runtime configuration.
type Config struct {
	Version  string
	Listen   string
	HTTPPort string // plain HTTP listener that redirects to HTTPS ("" = off)
	DataDir  string
	Demo     bool
	NoTLS    bool
}

// Run starts everything and blocks until ctx is done.
func Run(ctx context.Context, cfg Config) error {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "pk95proxbackup.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	signer, err := hostexec.LoadOrCreateKey(filepath.Join(cfg.DataDir, "ssh_ed25519"))
	if err != nil {
		return fmt.Errorf("SSH-Schlüssel: %w", err)
	}
	c := core.New(st, cfg.DataDir, cfg.Version, signer)
	c.SelfUUID = core.ReadSelfUUID
	if cfg.Demo {
		stop, err := fake.Setup(c)
		if err != nil {
			return err
		}
		defer stop()
	}
	if err := ensureSetupCode(st, cfg.DataDir); err != nil {
		return err
	}
	go c.Run(ctx)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.New(c, web.FS, !cfg.NoTLS),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 2)
	if cfg.NoTLS {
		go func() { errc <- srv.ListenAndServe() }()
	} else {
		cert, err := loadOrCreateCert(cfg.DataDir)
		if err != nil {
			return fmt.Errorf("Zertifikat: %w", err)
		}
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		go func() { errc <- srv.ListenAndServeTLS("", "") }()
		if cfg.HTTPPort != "" {
			handler := srv.Handler
			redir := &http.Server{Addr: cfg.HTTPPort, ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// A reverse proxy that already speaks HTTPS to the browser may talk plain HTTP to
				// us: serve it instead of redirecting (which would loop).
				proto := strings.ToLower(r.Header.Get("X-Forwarded-Proto"))
				if proto == "https" || (proto == "" && r.Header.Get("X-Forwarded-For") != "") {
					handler.ServeHTTP(w, r)
					return
				}
				host, _, err := net.SplitHostPort(r.Host)
				if err != nil {
					host = r.Host
				}
				_, port, _ := net.SplitHostPort(cfg.Listen)
				target := "https://" + host
				if port != "" && port != "443" {
					target += ":" + port
				}
				http.Redirect(w, r, target+r.URL.RequestURI(), http.StatusMovedPermanently)
			})}
			go func() {
				if err := redir.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Printf("http redirect: %v", err)
				}
			}()
			defer redir.Close()
		}
	}
	log.Printf("PK95 ProxBackup %s läuft auf %s (Daten: %s)", cfg.Version, cfg.Listen, cfg.DataDir)
	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}

// ensureSetupCode writes a one-time code while no account exists and shows it on the console.
func ensureSetupCode(st *store.Store, dataDir string) error {
	n, err := st.UserCount()
	if err != nil || n > 0 {
		_ = os.Remove("/etc/issue.d/pk95proxbackup.issue")
		return err
	}
	path := api.SetupCodePath(dataDir)
	code := ""
	if b, err := os.ReadFile(path); err == nil {
		code = strings.TrimSpace(string(b))
	}
	if code == "" {
		const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
		buf := make([]byte, 8)
		for i := range buf {
			n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
			buf[i] = alphabet[n.Int64()]
		}
		code = string(buf[:4]) + "-" + string(buf[4:])
		if err := os.WriteFile(path, []byte(code+"\n"), 0o600); err != nil {
			return err
		}
	}
	log.Printf("Einrichtungscode: %s", code)
	if _, err := os.Stat("/etc/issue.d"); err == nil {
		ip := core.PrimaryIP()
		msg := fmt.Sprintf("\n  PK95 ProxBackup: https://%s\n  Einrichtungscode: %s\n\n", ip, code)
		_ = os.WriteFile("/etc/issue.d/pk95proxbackup.issue", []byte(msg), 0o644)
	}
	return nil
}

// loadOrCreateCert returns the HTTPS certificate: a custom one (cert.pem/key.pem in the data
// directory) or a self-signed one created on first start.
func loadOrCreateCert(dataDir string) (tls.Certificate, error) {
	certPath, keyPath := filepath.Join(dataDir, "cert.pem"), filepath.Join(dataDir, "key.pem")
	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	host, _ := os.Hostname()
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host, Organization: []string{"PK95 ProxBackup"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{host, "localhost"},
	}
	for _, ip := range core.LocalIPs() {
		if p := net.ParseIP(ip); p != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, p)
		}
	}
	tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP("127.0.0.1"))
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(certPath, keyPath)
}
