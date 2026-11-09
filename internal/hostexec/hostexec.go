// Package hostexec runs commands and writes files on the machine of a Backup Server:
// locally (PBS inside this appliance) or over SSH (an existing PBS elsewhere).
package hostexec

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Runner executes commands on a host.
type Runner interface {
	// Run executes name with args and returns combined output.
	Run(ctx context.Context, name string, args ...string) (string, error)
	// WriteFile writes a file with the given mode (creating parent directories).
	WriteFile(ctx context.Context, path string, data []byte, mode os.FileMode) error
	// RemoveFile deletes a file (missing is fine).
	RemoveFile(ctx context.Context, path string) error
	// Describe names the host for messages.
	Describe() string
}

// Local runs on this machine.
type Local struct{}

// Run implements Runner.
func (Local) Run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %s", name, firstLine(out, err))
	}
	return string(out), nil
}

// WriteFile implements Runner.
func (Local) WriteFile(_ context.Context, path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RemoveFile implements Runner.
func (Local) RemoveFile(_ context.Context, path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Describe implements Runner.
func (Local) Describe() string { return "diese Appliance" }

func firstLine(out []byte, err error) string {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return err.Error()
	}
	if len(s) > 600 {
		s = s[len(s)-600:]
	}
	return s
}

// ---- SSH ----

// SSH runs commands on a remote host as the given user (normally root).
type SSH struct {
	Host    string
	Port    int
	User    string
	Signer  ssh.Signer
	HostKey string // known host key (authorized_keys format); empty = learn on first use
	OnLearn func(key string)
	mu      sync.Mutex
	learned string
}

// Describe implements Runner.
func (s *SSH) Describe() string { return s.User + "@" + s.Host }

func (s *SSH) dial(ctx context.Context) (*ssh.Client, error) {
	port := s.Port
	if port == 0 {
		port = 22
	}
	cfg := &ssh.ClientConfig{
		User:    s.User,
		Auth:    []ssh.AuthMethod{ssh.PublicKeys(s.Signer)},
		Timeout: 10 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
			s.mu.Lock()
			defer s.mu.Unlock()
			want := s.HostKey
			if want == "" {
				want = s.learned
			}
			if want == "" {
				s.learned = got
				if s.OnLearn != nil {
					s.OnLearn(got)
				}
				return nil
			}
			if got != want {
				return errors.New("SSH-Hostschlüssel hat sich geändert, Verbindung abgelehnt")
			}
			return nil
		},
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(s.Host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("SSH zu %s nicht erreichbar: %w", s.Host, err)
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, conn.RemoteAddr().String(), cfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SSH-Anmeldung an %s: %w", s.Host, err)
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// ShellQuote quotes one argument for a POSIX shell.
func ShellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@,+", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (s *SSH) exec(ctx context.Context, command string, stdin []byte) (string, error) {
	c, err := s.dial(ctx)
	if err != nil {
		return "", err
	}
	defer c.Close()
	sess, err := c.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var out bytes.Buffer
	sess.Stdout = &out
	sess.Stderr = &out
	if stdin != nil {
		sess.Stdin = bytes.NewReader(stdin)
	}
	done := make(chan error, 1)
	go func() { done <- sess.Run("LC_ALL=C " + command) }()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		return out.String(), ctx.Err()
	case <-time.After(2 * time.Minute):
		return out.String(), errors.New("Zeitüberschreitung")
	}
	if err != nil {
		return out.String(), fmt.Errorf("%s", firstLine(out.Bytes(), err))
	}
	return out.String(), nil
}

// Run implements Runner.
func (s *SSH) Run(ctx context.Context, name string, args ...string) (string, error) {
	parts := []string{ShellQuote(name)}
	for _, a := range args {
		parts = append(parts, ShellQuote(a))
	}
	return s.exec(ctx, strings.Join(parts, " "), nil)
}

// WriteFile implements Runner.
func (s *SSH) WriteFile(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	q := ShellQuote(path)
	tmp := ShellQuote(path + ".tmp")
	cmd := fmt.Sprintf("mkdir -p %s && umask 077 && cat > %s && chmod %o %s && mv %s %s",
		ShellQuote(filepath.Dir(path)), tmp, mode, tmp, tmp, q)
	_, err := s.exec(ctx, cmd, data)
	return err
}

// RemoveFile implements Runner.
func (s *SSH) RemoveFile(ctx context.Context, path string) error {
	_, err := s.exec(ctx, "rm -f "+ShellQuote(path), nil)
	return err
}

// LoadOrCreateKey returns the appliance's SSH key (ed25519) stored at path.
func LoadOrCreateKey(path string) (ssh.Signer, error) {
	if b, err := os.ReadFile(path); err == nil {
		return ssh.ParsePrivateKey(b)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, "pk95proxbackup")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(priv)
}

// PublicKey renders the public key in authorized_keys form.
func PublicKey(s ssh.Signer) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.PublicKey()))) + " pk95proxbackup"
}
