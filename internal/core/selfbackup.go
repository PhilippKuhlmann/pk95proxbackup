package core

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/scrypt"

	"pk95proxbackup/internal/prox"
	"pk95proxbackup/internal/store"
)

// Self backup: the configuration of the appliance (its database with connections, plans and
// accounts, its SSH key and certificate, and the configuration of the local Proxmox Backup
// Server) is encrypted with a recovery password and stored once a day as backup group
// host/pk95proxbackup in the datastores of this appliance. The rotation disks copy it along,
// so a new appliance can be set up from any of them, or from a downloaded file.

const (
	selfBackupType = "host"
	selfBackupID   = "pk95proxbackup"
	selfBackupFile = "pk95proxbackup.conf.blob"
	selfBackupKeep = 14
	selfBackupMax  = 256 << 20
	configMagic    = "PK95CFG1"
)

// SystemRoot is where the system files of the backup are read from and written to.
var SystemRoot = "/"

// systemFiles are the configuration files of this machine that belong in the backup.
var systemFiles = []string{"etc/proxmox-backup", "etc/pk95proxbackup", "etc/systemd/system/mnt-datastore-*.mount", "etc/systemd/system/mnt-pk95-*.mount"}

var dataFiles = []string{"pk95proxbackup.db", "ssh_ed25519", "cert.pem", "key.pem"}

// SelfBackupSettings are the stored settings (the key never leaves the appliance).
type SelfBackupSettings struct {
	Enabled   bool     `json:"enabled"`
	Salt      []byte   `json:"salt,omitempty"`
	Key       []byte   `json:"key,omitempty"`
	LastRun   int64    `json:"last_run"`
	LastOK    int64    `json:"last_ok"`
	LastError string   `json:"last_error,omitempty"`
	LastSize  int64    `json:"last_size"`
	Targets   []string `json:"targets"` // "server name: store"
}

// SelfBackupInfo is what the settings page shows.
type SelfBackupInfo struct {
	Enabled   bool     `json:"enabled"`
	HasKey    bool     `json:"has_key"`
	LastRun   int64    `json:"last_run"`
	LastOK    int64    `json:"last_ok"`
	LastError string   `json:"last_error,omitempty"`
	LastSize  int64    `json:"last_size"`
	Targets   []string `json:"targets"`
	Running   bool     `json:"running"`
}

type selfBackupRuntime struct {
	mu   sync.Mutex
	busy bool
}

// ConfigManifest describes a configuration backup.
type ConfigManifest struct {
	Format  int    `json:"format"`
	Version string `json:"version"`
	Host    string `json:"host"`
	Created int64  `json:"created"`
	PBS     bool   `json:"pbs"`
	Files   int    `json:"files"`
}

func (c *Core) selfBackupSettings() SelfBackupSettings {
	var s SelfBackupSettings
	_ = c.St.GetSetting("selfbackup", &s)
	return s
}

// SelfBackup returns the state of the configuration backup.
func (c *Core) SelfBackup() SelfBackupInfo {
	s := c.selfBackupSettings()
	if s.Targets == nil {
		s.Targets = []string{}
	}
	c.selfBackup.mu.Lock()
	busy := c.selfBackup.busy
	c.selfBackup.mu.Unlock()
	return SelfBackupInfo{Enabled: s.Enabled, HasKey: len(s.Key) == 32, LastRun: s.LastRun, LastOK: s.LastOK, LastError: s.LastError,
		LastSize: s.LastSize, Targets: s.Targets, Running: busy}
}

// ValidRecoveryPassword checks a new recovery password.
func ValidRecoveryPassword(p string) string {
	if len([]rune(p)) < 12 {
		return "Mindestens 12 Zeichen."
	}
	return ""
}

// SetSelfBackup turns the backup on or off and sets a new recovery password (if not empty).
func (c *Core) SetSelfBackup(enabled bool, password string) error {
	s := c.selfBackupSettings()
	if password != "" {
		if m := ValidRecoveryPassword(password); m != "" {
			return errors.New(m)
		}
		salt := make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return err
		}
		key, err := deriveConfigKey(password, salt)
		if err != nil {
			return err
		}
		s.Salt, s.Key = salt, key
	}
	if enabled && len(s.Key) != 32 {
		return errors.New("Zuerst ein Wiederherstellungs-Passwort festlegen.")
	}
	s.Enabled = enabled
	return c.St.SetSetting("selfbackup", s)
}

func deriveConfigKey(password string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(password), salt, 1<<15, 8, 1, 32)
}

func sealConfig(key, salt, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte(configMagic), salt...)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, plain, []byte(configMagic)), nil
}

// OpenConfig decrypts a configuration backup with the recovery password.
func OpenConfig(data []byte, password string) ([]byte, error) {
	if len(data) < len(configMagic)+16+12+16 || string(data[:len(configMagic)]) != configMagic {
		return nil, errors.New("Das ist keine Konfigurationssicherung von PK95 ProxBackup.")
	}
	salt := data[len(configMagic) : len(configMagic)+16]
	rest := data[len(configMagic)+16:]
	key, err := deriveConfigKey(password, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, rest[:gcm.NonceSize()], rest[gcm.NonceSize():], []byte(configMagic))
	if err != nil {
		return nil, errors.New("Das Wiederherstellungs-Passwort stimmt nicht (oder die Datei ist beschädigt).")
	}
	return plain, nil
}

// buildConfigArchive packs the configuration as tar.gz.
func (c *Core) buildConfigArchive() ([]byte, ConfigManifest, error) {
	host, _ := os.Hostname()
	man := ConfigManifest{Format: 1, Version: c.Version, Host: host, Created: time.Now().Unix()}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name string, data []byte, mode int64, mod time.Time, owner ...int) error {
		man.Files++
		h := &tar.Header{Name: name, Mode: mode, Size: int64(len(data)), ModTime: mod, Typeflag: tar.TypeReg, Uid: -1, Gid: -1}
		if len(owner) == 2 {
			h.Uid, h.Gid = owner[0], owner[1]
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	// A consistent copy of the database.
	tmp := filepath.Join(c.DataDir, ".selfbackup-"+NewID(4)+".db")
	if err := c.St.Backup(tmp); err != nil {
		return nil, man, fmt.Errorf("Datenbank kopieren: %w", err)
	}
	db, err := os.ReadFile(tmp)
	os.Remove(tmp)
	if err != nil {
		return nil, man, err
	}
	if err := add("data/pk95proxbackup.db", db, 0o600, time.Now()); err != nil {
		return nil, man, err
	}
	for _, f := range dataFiles[1:] {
		if b, err := os.ReadFile(filepath.Join(c.DataDir, f)); err == nil {
			if err := add("data/"+f, b, 0o600, time.Now()); err != nil {
				return nil, man, err
			}
		}
	}
	for _, pattern := range systemFiles {
		matches, _ := filepath.Glob(filepath.Join(SystemRoot, pattern))
		for _, m := range matches {
			_ = filepath.WalkDir(m, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() || !d.Type().IsRegular() {
					return nil
				}
				if strings.HasSuffix(p, ".lck") || strings.HasSuffix(p, ".lock") || strings.HasSuffix(p, ".tmp") {
					return nil
				}
				info, err := d.Info()
				if err != nil || info.Size() > 16<<20 {
					return nil
				}
				b, err := os.ReadFile(p)
				if err != nil {
					return nil
				}
				rel, err := filepath.Rel(SystemRoot, p)
				if err != nil {
					return nil
				}
				rel = filepath.ToSlash(rel)
				if strings.HasPrefix(rel, "etc/proxmox-backup/") {
					man.PBS = true
				}
				uid, gid := fileOwner(info)
				return add("root/"+rel, b, int64(info.Mode().Perm()), info.ModTime(), uid, gid)
			})
		}
	}
	mb, _ := json.MarshalIndent(man, "", "  ")
	if err := add("manifest.json", mb, 0o600, time.Now()); err != nil {
		return nil, man, err
	}
	if err := tw.Close(); err != nil {
		return nil, man, err
	}
	if err := gz.Close(); err != nil {
		return nil, man, err
	}
	return buf.Bytes(), man, nil
}

// ExportConfig returns the encrypted configuration (for download).
func (c *Core) ExportConfig() ([]byte, ConfigManifest, error) {
	s := c.selfBackupSettings()
	if len(s.Key) != 32 {
		return nil, ConfigManifest{}, errors.New("Zuerst ein Wiederherstellungs-Passwort festlegen.")
	}
	plain, man, err := c.buildConfigArchive()
	if err != nil {
		return nil, man, err
	}
	sealed, err := sealConfig(s.Key, s.Salt, plain)
	return sealed, man, err
}

func (c *Core) scheduleSelfBackup(now time.Time) {
	s := c.selfBackupSettings()
	if !s.Enabled || len(s.Key) != 32 {
		return
	}
	// Daily; after a failure try again after an hour.
	if s.LastOK > 0 && now.Unix()-s.LastOK < 24*3600 || s.LastRun > s.LastOK && now.Unix()-s.LastRun < 3600 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		_ = c.RunSelfBackup(ctx)
	}()
}

// selfBackupTargets are the datastores that receive the backup: the fixed (not removable)
// datastores of this appliance's PBS, or of all Backup Servers when there is none.
func (c *Core) selfBackupTargets(ctx context.Context) ([]PBSServer, [][]string) {
	servers, _ := c.Servers()
	var local []PBSServer
	for _, s := range servers {
		if s.Local {
			local = append(local, s)
		}
	}
	if len(local) > 0 {
		servers = local
	}
	var outS []PBSServer
	var outT [][]string
	for _, s := range servers {
		cfgs, err := c.PBSClient(s).Datastores(ctx)
		if err != nil {
			continue
		}
		var stores []string
		for _, d := range cfgs {
			if d.BackingDevice == "" && d.Maintenance == "" {
				stores = append(stores, d.Name)
			}
		}
		sort.Strings(stores)
		if len(stores) > 0 {
			outS, outT = append(outS, s), append(outT, stores)
		}
	}
	return outS, outT
}

// RunSelfBackup writes the encrypted configuration into the datastores now.
func (c *Core) RunSelfBackup(ctx context.Context) error {
	c.selfBackup.mu.Lock()
	if c.selfBackup.busy {
		c.selfBackup.mu.Unlock()
		return errors.New("Die Sicherung läuft gerade.")
	}
	c.selfBackup.busy = true
	c.selfBackup.mu.Unlock()
	defer func() {
		c.selfBackup.mu.Lock()
		c.selfBackup.busy = false
		c.selfBackup.mu.Unlock()
	}()
	s := c.selfBackupSettings()
	wasOK := s.LastError == ""
	s.LastRun = time.Now().Unix()
	err := c.runSelfBackup(ctx, &s)
	cur := c.selfBackupSettings() // keeps a password changed meanwhile
	cur.LastRun, cur.LastSize, cur.Targets = s.LastRun, s.LastSize, s.Targets
	cur.LastError = errText(err)
	if err == nil {
		cur.LastOK = s.LastRun
	}
	_ = c.St.SetSetting("selfbackup", cur)
	if err != nil && wasOK {
		c.Event("warning", "Appliance", "Konfiguration der Appliance nicht gesichert", err.Error())
	} else if err == nil && !wasOK {
		c.Event("info", "Appliance", "Konfiguration der Appliance wieder gesichert", "Ziel: "+strings.Join(cur.Targets, ", "))
	}
	return err
}

func (c *Core) runSelfBackup(ctx context.Context, s *SelfBackupSettings) error {
	if len(s.Key) != 32 {
		return errors.New("Kein Wiederherstellungs-Passwort festgelegt.")
	}
	plain, _, err := c.buildConfigArchive()
	if err != nil {
		return err
	}
	sealed, err := sealConfig(s.Key, s.Salt, plain)
	if err != nil {
		return err
	}
	s.LastSize = int64(len(sealed))
	servers, stores := c.selfBackupTargets(ctx)
	if len(servers) == 0 {
		return errors.New("Kein Datastore gefunden, in den die Konfiguration geschrieben werden kann.")
	}
	upload := c.UploadSelfBackup
	if upload == nil {
		upload = uploadWithClient
	}
	s.Targets = []string{}
	var errs []string
	for i, srv := range servers {
		pbs := c.PBSClient(srv)
		for _, st := range stores[i] {
			if err := upload(ctx, srv, st, sealed); err != nil {
				errs = append(errs, st+": "+err.Error())
				continue
			}
			s.Targets = append(s.Targets, srv.Name+": "+st)
			c.pruneSelfBackups(ctx, pbs, st)
		}
	}
	if len(s.Targets) == 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	if len(errs) > 0 {
		log.Printf("selfbackup: %s", strings.Join(errs, "; "))
	}
	return nil
}

// pruneSelfBackups keeps the newest backups of the configuration.
func (c *Core) pruneSelfBackups(ctx context.Context, pbs *prox.PBS, st string) {
	snaps, err := pbs.Snapshots(ctx, st, "")
	if err != nil {
		return
	}
	var mine []prox.Snapshot
	for _, sn := range snaps {
		if sn.Type == selfBackupType && sn.ID == selfBackupID {
			mine = append(mine, sn)
		}
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].Time > mine[j].Time })
	for i := selfBackupKeep; i < len(mine); i++ {
		if !mine[i].Protected {
			_ = pbs.DeleteSnapshot(ctx, st, "", selfBackupType, selfBackupID, mine[i].Time)
		}
	}
}

// uploadWithClient stores the backup with proxmox-backup-client as host/pk95proxbackup.
func uploadWithClient(ctx context.Context, srv PBSServer, st string, data []byte) error {
	bin, err := exec.LookPath("proxmox-backup-client")
	if err != nil {
		return errors.New("proxmox-backup-client ist nicht installiert (apt install proxmox-backup-client)")
	}
	dir, err := os.MkdirTemp("", "pk95cfg")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "pk95proxbackup.conf")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		return err
	}
	host := srv.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := srv.Port
	if port == 0 {
		port = 8007
	}
	repo := srv.AuthID + "@" + host + ":" + strconv.Itoa(port) + ":" + st
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "backup", "pk95proxbackup.conf:"+file, "--repository", repo,
		"--backup-type", selfBackupType, "--backup-id", selfBackupID)
	cmd.Env = append(os.Environ(), "PBS_PASSWORD="+srv.Secret, "PBS_FINGERPRINT="+srv.Fingerprint, "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(strings.ReplaceAll(msg, srv.Secret, "…"))
	}
	return nil
}

// FoundConfig is a configuration backup found in a datastore.
type FoundConfig struct {
	ServerID   string `json:"server_id"`
	ServerName string `json:"server_name"`
	Store      string `json:"store"`
	Time       int64  `json:"time"`
	Size       int64  `json:"size"`
}

// FindConfigBackups lists the configuration backups in all reachable datastores.
func (c *Core) FindConfigBackups(ctx context.Context) []FoundConfig {
	out := []FoundConfig{}
	servers, _ := c.Servers()
	for _, s := range servers {
		pbs := c.PBSClient(s)
		us, err := pbs.Usage(ctx)
		if err != nil {
			continue
		}
		for _, u := range us {
			if u.MountStatus == "notmounted" {
				continue
			}
			snaps, err := pbs.Snapshots(ctx, u.Store, "")
			if err != nil {
				continue
			}
			for _, sn := range snaps {
				if sn.Type == selfBackupType && sn.ID == selfBackupID {
					out = append(out, FoundConfig{ServerID: s.ID, ServerName: s.Name, Store: u.Store, Time: sn.Time, Size: sn.Size})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time > out[j].Time })
	return out
}

// LoadConfigBackup downloads a configuration backup from a datastore.
func (c *Core) LoadConfigBackup(ctx context.Context, serverID, st string, t int64) ([]byte, error) {
	pbs, _, err := c.PBS(serverID)
	if err != nil {
		return nil, err
	}
	return pbs.SnapshotFileMax(ctx, st, "", selfBackupType, selfBackupID, t, selfBackupFile, selfBackupMax)
}

func stagingDir(dataDir string) string      { return filepath.Join(dataDir, "restore-staging") }
func restoreDonePath(dataDir string) string { return filepath.Join(dataDir, "restore-done.json") }

// RestoreConfig checks and unpacks a configuration backup, writes the system files back
// and restarts the program, which then swaps in the restored database (ApplyStagedRestore).
func (c *Core) RestoreConfig(ctx context.Context, data []byte, password string) (ConfigManifest, error) {
	plain, err := OpenConfig(data, password)
	if err != nil {
		return ConfigManifest{}, err
	}
	man, files, err := readConfigArchive(plain)
	if err != nil {
		return man, err
	}
	stage := stagingDir(c.DataDir)
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(filepath.Join(stage, "data"), 0o700); err != nil {
		return man, err
	}
	for name, f := range files {
		if strings.HasPrefix(name, "data/") {
			if err := os.WriteFile(filepath.Join(stage, name), f.data, 0o600); err != nil {
				return man, err
			}
		}
	}
	// The database must open (this also migrates it to the running version).
	st, err := store.Open(filepath.Join(stage, "data", "pk95proxbackup.db"))
	if err != nil {
		_ = os.RemoveAll(stage)
		return man, fmt.Errorf("Die Datenbank der Sicherung lässt sich nicht öffnen: %w", err)
	}
	st.Close()
	for _, suf := range []string{"-wal", "-shm"} {
		os.Remove(filepath.Join(stage, "data", "pk95proxbackup.db"+suf))
	}
	if err := c.applySystemFiles(ctx, files); err != nil {
		_ = os.RemoveAll(stage)
		return man, err
	}
	mb, _ := json.Marshal(man)
	if err := os.WriteFile(filepath.Join(stage, "READY"), mb, 0o600); err != nil {
		return man, err
	}
	log.Printf("Konfiguration von %s (Stand %s) wird eingespielt", man.Host, fmtTime(man.Created))
	if c.Restart != nil {
		time.AfterFunc(time.Second, c.Restart)
	}
	return man, nil
}

type archFile struct {
	data     []byte
	mode     int64
	uid, gid int
}

// fileOwner returns the numeric owner of a file (-1 when unknown). PBS reads its
// configuration as user backup, so the owner must survive a restore.
func fileOwner(info fs.FileInfo) (int, int) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), int(st.Gid)
	}
	return -1, -1
}

// readConfigArchive unpacks the tar.gz and accepts only the files a backup contains.
func readConfigArchive(plain []byte) (ConfigManifest, map[string]archFile, error) {
	var man ConfigManifest
	gz, err := gzip.NewReader(bytes.NewReader(plain))
	if err != nil {
		return man, nil, errors.New("Sicherung beschädigt (gzip)")
	}
	tr := tar.NewReader(gz)
	files := map[string]archFile{}
	allowedData := map[string]bool{}
	for _, f := range dataFiles {
		allowedData["data/"+f] = true
	}
	total := int64(0)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return man, nil, errors.New("Sicherung beschädigt (tar)")
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := path.Clean(h.Name)
		ok := name == "manifest.json" || allowedData[name]
		if strings.HasPrefix(name, "root/") && !strings.Contains(name, "..") {
			rel := strings.TrimPrefix(name, "root/")
			for _, pattern := range systemFiles {
				if m, _ := path.Match(pattern, rel); m || strings.HasPrefix(rel, pattern+"/") {
					ok = true
				}
			}
		}
		if !ok {
			return man, nil, fmt.Errorf("unerwartete Datei %q in der Sicherung", h.Name)
		}
		total += h.Size
		if total > 1<<30 {
			return man, nil, errors.New("Sicherung zu groß")
		}
		b, err := io.ReadAll(io.LimitReader(tr, h.Size))
		if err != nil {
			return man, nil, err
		}
		files[name] = archFile{data: b, mode: h.Mode, uid: h.Uid, gid: h.Gid}
	}
	m, ok := files["manifest.json"]
	if !ok || json.Unmarshal(m.data, &man) != nil || man.Format != 1 {
		return man, nil, errors.New("Sicherung ohne gültiges Inhaltsverzeichnis")
	}
	if _, ok := files["data/pk95proxbackup.db"]; !ok {
		return man, nil, errors.New("Die Sicherung enthält keine Datenbank.")
	}
	return man, files, nil
}

// applySystemFiles writes the configuration of the local PBS and the mount units back and
// restarts the services that read them.
func (c *Core) applySystemFiles(ctx context.Context, files map[string]archFile) error {
	r := c.LocalRunner
	pbs, units := false, []string{}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !strings.HasPrefix(n, "root/") {
			continue
		}
		rel := strings.TrimPrefix(n, "root/")
		if strings.HasPrefix(rel, "etc/proxmox-backup/") {
			if SystemRoot == "/" && !HasLocalPBS() {
				continue // no PBS here: its configuration has no use
			}
			pbs = true
		}
		mode := os.FileMode(files[n].mode) & 0o777
		if mode == 0 {
			mode = 0o600
		}
		dst := filepath.Join(SystemRoot, rel)
		if err := r.WriteFile(ctx, dst, files[n].data, mode); err != nil {
			return fmt.Errorf("%s zurückschreiben: %w", rel, err)
		}
		if f := files[n]; f.uid >= 0 && f.gid >= 0 {
			if _, err := r.Run(ctx, "chown", strconv.Itoa(f.uid)+":"+strconv.Itoa(f.gid), dst); err != nil {
				log.Printf("restore: chown %s: %v", rel, err)
			}
		}
		if strings.HasSuffix(rel, ".mount") {
			units = append(units, path.Base(rel))
		}
	}
	if len(units) > 0 {
		_, _ = r.Run(ctx, "systemctl", "daemon-reload")
		for _, u := range units {
			// A datastore disk that is not attached yet only fails to mount; that is fine.
			_, _ = r.Run(ctx, "systemctl", "enable", u)
			_, _ = r.Run(ctx, "systemctl", "start", u)
		}
	}
	if pbs {
		if _, err := r.Run(ctx, "systemctl", "restart", "proxmox-backup", "proxmox-backup-proxy"); err != nil {
			log.Printf("restore: restart pbs: %v", err)
		}
	}
	return nil
}

// ApplyStagedRestore runs before the database is opened: a restore prepared by RestoreConfig
// replaces the database, the SSH key and the certificate. The previous database stays as
// before-restore.db.
func ApplyStagedRestore(dataDir string) error {
	stage := stagingDir(dataDir)
	man, err := os.ReadFile(filepath.Join(stage, "READY"))
	if err != nil {
		return nil
	}
	db := filepath.Join(dataDir, "pk95proxbackup.db")
	for _, suf := range []string{"", "-wal", "-shm"} {
		old := filepath.Join(dataDir, "before-restore.db"+suf)
		os.Remove(old)
		if _, err := os.Stat(db + suf); err == nil {
			if err := os.Rename(db+suf, old); err != nil {
				return err
			}
		}
	}
	for _, f := range dataFiles {
		src := filepath.Join(stage, "data", f)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.Rename(src, filepath.Join(dataDir, f)); err != nil {
			return err
		}
	}
	_ = os.WriteFile(restoreDonePath(dataDir), man, 0o600)
	// Accounts exist again: the one-time setup code is no longer needed.
	os.Remove(filepath.Join(dataDir, "setup-code"))
	return os.RemoveAll(stage)
}
