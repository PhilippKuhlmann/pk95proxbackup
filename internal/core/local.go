package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	"pk95proxbackup/internal/prox"
)

const localTokenName = "pk95proxbackup"

// HasLocalPBS reports whether Proxmox Backup Server is installed on this machine.
func HasLocalPBS() bool {
	_, err := os.Stat("/usr/sbin/proxmox-backup-manager")
	return err == nil
}

// EnsureLocalServer registers the PBS of this appliance with its own API token once.
func (c *Core) EnsureLocalServer(ctx context.Context) error {
	servers, err := c.Servers()
	if err != nil {
		return err
	}
	for _, s := range servers {
		if s.Local {
			return nil
		}
	}
	if !HasLocalPBS() {
		return nil
	}
	fp, err := prox.FetchFingerprint(ctx, "127.0.0.1", 8007)
	if err != nil {
		return fmt.Errorf("lokaler PBS noch nicht bereit: %w", err)
	}
	r := c.LocalRunner
	_, _ = r.Run(ctx, "proxmox-backup-manager", "user", "delete-token", "root@pam", localTokenName)
	out, err := r.Run(ctx, "proxmox-backup-manager", "user", "generate-token", "root@pam", localTokenName, "--output-format", "json")
	if err != nil {
		return err
	}
	var tok struct {
		TokenID string `json:"tokenid"`
		Value   string `json:"value"`
	}
	if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &tok); err != nil {
		return fmt.Errorf("Token-Antwort nicht lesbar: %w", err)
	}
	if _, err := r.Run(ctx, "proxmox-backup-manager", "acl", "update", "/", "Admin", "--auth-id", tok.TokenID); err != nil {
		return err
	}
	host, _ := os.Hostname()
	s := PBSServer{
		ID: "local", Name: "Diese Appliance (" + host + ")", Local: true, Host: "127.0.0.1", Port: 8007,
		PublicHost: PrimaryIP(), AuthID: tok.TokenID, Secret: tok.Value, Fingerprint: fp,
	}
	if err := c.St.Put(KindPBS, s.ID, s); err != nil {
		return err
	}
	c.Event("info", "Appliance", "Lokaler Backup-Server eingebunden", "Der Proxmox Backup Server dieser Appliance ist als „"+s.Name+"“ eingetragen.")
	return nil
}

// PrimaryIP returns the address of the interface with the default route.
func PrimaryIP() string {
	conn, err := net.Dial("udp", "192.0.2.1:9") // no packet is sent
	if err == nil {
		defer conn.Close()
		if a, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			return a.IP.String()
		}
	}
	return ""
}

// LocalIPs lists the non-loopback addresses of this machine.
func LocalIPs() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			out = append(out, n.IP.String())
		}
	}
	return out
}

// ReadSelfUUID reads the SMBIOS UUID of this VM.
func ReadSelfUUID() string {
	b, err := os.ReadFile("/sys/class/dmi/id/product_uuid")
	if err != nil {
		out, err2 := exec.Command("dmidecode", "-s", "system-uuid").Output()
		if err2 != nil {
			return ""
		}
		b = out
	}
	return strings.ToLower(strings.TrimSpace(string(b)))
}
