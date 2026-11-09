// Package shares mounts NFS and SMB/CIFS shares on a Backup Server through systemd mount units,
// so the mounts come back after a reboot and Proxmox Backup Server can keep a datastore on them.
package shares

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"pk95proxbackup/internal/hostexec"
)

// Root is the directory below which all shares are mounted.
const Root = "/mnt/pk95"

// Share describes one network share.
type Share struct {
	ID       string `json:"id"`        // [a-z0-9_]: used in paths and the unit name
	Name     string `json:"name"`      // shown in the UI
	ServerID string `json:"server_id"` // Backup Server the share is mounted on
	Type     string `json:"type"`      // nfs, cifs
	Source   string `json:"source"`    // nfs: host:/export, cifs: //host/share
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Domain   string `json:"domain,omitempty"`
	Options  string `json:"options,omitempty"` // extra mount options
	Store    string `json:"store,omitempty"`   // datastore created on the share
}

var (
	idRe     = regexp.MustCompile(`^[a-z0-9_]{2,32}$`)
	nfsRe    = regexp.MustCompile(`^[A-Za-z0-9.\-\[\]:]+:/[^\s]*$`)
	cifsRe   = regexp.MustCompile(`^//[A-Za-z0-9.\-\[\]:]+/[^\s]+$`)
	optionRe = regexp.MustCompile(`^[A-Za-z0-9_=.,:\-/]*$`)
)

// Validate checks a share definition and returns a field → message map.
func (s *Share) Validate() map[string]string {
	errs := map[string]string{}
	if !idRe.MatchString(s.ID) {
		errs["id"] = "2–32 Zeichen: Kleinbuchstaben, Ziffern und _."
	}
	if strings.TrimSpace(s.Name) == "" {
		errs["name"] = "Bitte einen Namen angeben."
	}
	s.Source = strings.TrimSpace(s.Source)
	switch s.Type {
	case "nfs":
		if !nfsRe.MatchString(s.Source) {
			errs["source"] = "Form: server:/export/pfad, z. B. 192.168.1.10:/volume1/backup"
		}
	case "cifs":
		s.Source = strings.ReplaceAll(s.Source, `\`, "/")
		if !cifsRe.MatchString(s.Source) {
			errs["source"] = "Form: //server/freigabe, z. B. //nas.local/backup"
		}
		if strings.ContainsAny(s.Username+s.Domain, "\n\r") || strings.ContainsAny(s.Password, "\n\r") {
			errs["username"] = "Zeilenumbrüche sind nicht erlaubt."
		}
	default:
		errs["type"] = "NFS oder SMB wählen."
	}
	if !optionRe.MatchString(s.Options) {
		errs["options"] = "Nur Optionen wie vers=3.0,soft (ohne Leerzeichen)."
	}
	return errs
}

// MountPoint is where the share is mounted.
func (s *Share) MountPoint() string { return Root + "/" + s.ID }

// UnitName is the systemd mount unit (mount point with / → -; IDs never need escaping).
func (s *Share) UnitName() string { return "mnt-pk95-" + s.ID + ".mount" }

func (s *Share) unitPath() string { return "/etc/systemd/system/" + s.UnitName() }
func (s *Share) credPath() string { return "/etc/pk95proxbackup/cifs-" + s.ID + ".cred" }

// Unit renders the systemd unit. backupUID is the uid of the "backup" user that runs PBS (34).
func (s *Share) Unit() string {
	opts := s.mountOptions()
	return fmt.Sprintf(`# Angelegt von PK95 ProxBackup. Änderungen bitte über die Oberfläche.
[Unit]
Description=PK95 ProxBackup Freigabe %s
After=network-online.target
Wants=network-online.target
Before=proxmox-backup-proxy.service

[Mount]
What=%s
Where=%s
Type=%s
Options=%s
TimeoutSec=60

[Install]
WantedBy=multi-user.target
`, oneLine(s.Name), s.Source, s.MountPoint(), s.Type, opts)
}

func (s *Share) mountOptions() string {
	var opts []string
	switch s.Type {
	case "nfs":
		opts = []string{"_netdev", "nofail"}
	case "cifs":
		// PBS writes as user "backup" (34); the files on the share belong to it.
		opts = []string{"credentials=" + s.credPath(), "uid=34", "gid=34", "file_mode=0660", "dir_mode=0770", "_netdev", "nofail"}
		if !strings.Contains(s.Options, "vers=") {
			opts = append(opts, "vers=3.0")
		}
	}
	if s.Options != "" {
		opts = append(opts, s.Options)
	}
	return strings.Join(opts, ",")
}

func (s *Share) credentials() string {
	var b strings.Builder
	b.WriteString("username=" + s.Username + "\n")
	b.WriteString("password=" + s.Password + "\n")
	if s.Domain != "" {
		b.WriteString("domain=" + s.Domain + "\n")
	}
	return b.String()
}

func oneLine(s string) string { return strings.NewReplacer("\n", " ", "\r", " ").Replace(s) }

// Install writes the unit (and credentials), protects the empty mount point and mounts the share.
func Install(ctx context.Context, r hostexec.Runner, s *Share) error {
	if s.Type == "cifs" {
		if err := r.WriteFile(ctx, s.credPath(), []byte(s.credentials()), 0o600); err != nil {
			return fmt.Errorf("Zugangsdaten schreiben: %w", err)
		}
	}
	if err := protectMountPoint(ctx, r, s.MountPoint()); err != nil {
		return err
	}
	if err := r.WriteFile(ctx, s.unitPath(), []byte(s.Unit()), 0o644); err != nil {
		return fmt.Errorf("Mount-Unit schreiben: %w", err)
	}
	if _, err := r.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	// A changed definition must be remounted.
	_, _ = r.Run(ctx, "systemctl", "stop", s.UnitName())
	if _, err := r.Run(ctx, "systemctl", "enable", "--now", s.UnitName()); err != nil {
		return fmt.Errorf("Einhängen fehlgeschlagen: %w", mountHint(err))
	}
	return nil
}

// protectMountPoint makes the empty directory immutable: if the share is missing, PBS cannot
// write into the root file system by mistake. Mounting on top of it still works.
func protectMountPoint(ctx context.Context, r hostexec.Runner, dir string) error {
	if out, err := r.Run(ctx, "findmnt", "-n", "-o", "TARGET", "--mountpoint", dir); err == nil && strings.TrimSpace(out) != "" {
		return nil // currently mounted, leave it alone
	}
	if _, err := r.Run(ctx, "mkdir", "-p", dir); err != nil {
		return err
	}
	_, _ = r.Run(ctx, "chattr", "+i", dir)
	return nil
}

// Remove unmounts the share and deletes unit and credentials. The data on the share stays.
func Remove(ctx context.Context, r hostexec.Runner, s *Share) error {
	_, _ = r.Run(ctx, "systemctl", "disable", "--now", s.UnitName())
	if err := r.RemoveFile(ctx, s.unitPath()); err != nil {
		return err
	}
	_ = r.RemoveFile(ctx, s.credPath())
	_, _ = r.Run(ctx, "systemctl", "daemon-reload")
	_, _ = r.Run(ctx, "chattr", "-i", s.MountPoint())
	_, _ = r.Run(ctx, "rmdir", s.MountPoint())
	return nil
}

// Mount mounts (start) or unmounts (stop) the share.
func Mount(ctx context.Context, r hostexec.Runner, s *Share, on bool) error {
	verb := "stop"
	if on {
		verb = "start"
	}
	_, err := r.Run(ctx, "systemctl", verb, s.UnitName())
	if err != nil && on {
		return mountHint(err)
	}
	return err
}

// State is the mount state of a share.
type State struct {
	Mounted bool   `json:"mounted"`
	Total   int64  `json:"total"`
	Used    int64  `json:"used"`
	Avail   int64  `json:"avail"`
	Error   string `json:"error,omitempty"`
}

// Status reads mount state and space.
func Status(ctx context.Context, r hostexec.Runner, s *Share) State {
	out, err := r.Run(ctx, "findmnt", "-n", "-b", "-o", "SIZE,USED,AVAIL", "--mountpoint", s.MountPoint())
	if err != nil || strings.TrimSpace(out) == "" {
		st := State{}
		if o, e := r.Run(ctx, "systemctl", "is-failed", s.UnitName()); e == nil && strings.TrimSpace(o) == "failed" {
			st.Error = "Einhängen fehlgeschlagen (systemctl status " + s.UnitName() + ")"
		}
		return st
	}
	st := State{Mounted: true}
	f := strings.Fields(out)
	if len(f) >= 3 {
		fmt.Sscan(f[0], &st.Total)
		fmt.Sscan(f[1], &st.Used)
		fmt.Sscan(f[2], &st.Avail)
	}
	return st
}

// Test mounts the share temporarily, writes and deletes a file and unmounts again.
func Test(ctx context.Context, r hostexec.Runner, s *Share) error {
	tmpID := "test_" + s.ID
	if len(tmpID) > 32 {
		tmpID = tmpID[:32]
	}
	t := *s
	t.ID = tmpID
	dir := "/run/pk95proxbackup/" + tmpID
	if t.Type == "cifs" {
		if err := r.WriteFile(ctx, t.credPath(), []byte(t.credentials()), 0o600); err != nil {
			return err
		}
		defer r.RemoveFile(context.WithoutCancel(ctx), t.credPath())
	}
	if _, err := r.Run(ctx, "mkdir", "-p", dir); err != nil {
		return err
	}
	opts := strings.ReplaceAll(t.mountOptions(), "_netdev,nofail", "")
	opts = strings.Trim(strings.ReplaceAll(opts, ",,", ","), ",")
	if _, err := r.Run(ctx, "mount", "-t", t.Type, "-o", opts, t.Source, dir); err != nil {
		_, _ = r.Run(ctx, "rmdir", dir)
		return mountHint(err)
	}
	defer func() {
		c := context.WithoutCancel(ctx)
		_, _ = r.Run(c, "umount", dir)
		_, _ = r.Run(c, "rmdir", dir)
	}()
	probe := dir + "/.pk95-schreibtest"
	if _, err := r.Run(ctx, "runuser", "-u", "backup", "--", "touch", probe); err != nil {
		if _, err2 := r.Run(ctx, "touch", probe); err2 != nil {
			return fmt.Errorf("eingehängt, aber nicht beschreibbar: %w", err2)
		}
		_, _ = r.Run(ctx, "rm", "-f", probe)
		return errors.New("eingehängt, aber der Benutzer „backup“ (UID 34) darf nicht schreiben. Bei NFS auf dem NAS „no_root_squash“ bzw. Zuordnung auf UID 34 einstellen")
	}
	_, _ = r.Run(ctx, "rm", "-f", probe)
	return nil
}

func mountHint(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "Permission denied") || strings.Contains(msg, "access denied"):
		return fmt.Errorf("%w – Zugriff verweigert: Benutzername/Passwort bzw. die Freigabe-Rechte für diese IP prüfen", err)
	case strings.Contains(msg, "No such file") || strings.Contains(msg, "does not exist"):
		return fmt.Errorf("%w – Freigabe oder Pfad gibt es nicht", err)
	case strings.Contains(msg, "wrong fs type") || strings.Contains(msg, "helper program"):
		return fmt.Errorf("%w – nfs-common bzw. cifs-utils fehlen auf dem Server (apt install nfs-common cifs-utils)", err)
	case strings.Contains(msg, "timed out") || strings.Contains(msg, "Host is down") || strings.Contains(msg, "No route"):
		return fmt.Errorf("%w – Server nicht erreichbar", err)
	}
	return err
}
