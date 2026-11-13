package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"pk95proxbackup/internal/prox"
)

// Copies between two Backup Servers: the target pulls from the source. On the target the
// appliance keeps a "remote" (address, fingerprint, login) for the source. The login is a
// token of its own on the source that may only read the datastores that are copied — not the
// appliance's admin token. The target has to reach the source over the network (TCP 8007).

const syncUser = "pk95-sync@pbs"

// SourceAddress is how other servers reach a Backup Server by default.
func SourceAddress(s PBSServer) string {
	host := s.Address()
	if s.Local {
		host = s.PublicHost
		if host == "" {
			host = PrimaryIP()
		}
	}
	if s.Port != 0 && s.Port != 8007 && !s.Local {
		host = net.JoinHostPort(host, strconv.Itoa(s.Port))
	}
	return host
}

// splitHostPort reads "host", "host:port" or "[v6]:port".
func splitHostPort(s string) (string, int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, errors.New("Adresse fehlt")
	}
	if h, p, err := net.SplitHostPort(s); err == nil {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", 0, errors.New("ungültiger Port")
		}
		return h, n, nil
	}
	if strings.ContainsAny(s, " /") {
		return "", 0, errors.New("nur Name oder IP, optional mit :Port")
	}
	return strings.Trim(s, "[]"), 8007, nil
}

type syncToken struct {
	TokenID string `json:"token_id"`
	Secret  string `json:"secret"`
}

var tokenNameRe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// RemoteFor makes sure the target server has a remote pointing at the source server that may
// read the given datastore, and returns its name ("" when both are the same server). host is
// the address under which the target reaches the source ("" = the current one or the default).
func (c *Core) RemoteFor(ctx context.Context, target *prox.PBS, targetSrv PBSServer, sourceID, sourceStore, host string) (string, error) {
	if sourceID == targetSrv.ID {
		return "", nil
	}
	src, err := c.Server(sourceID)
	if err != nil {
		return "", fmt.Errorf("Quell-Server: %w", err)
	}
	name := JobID("pk95", src.ID)
	// Keep an address set before, unless a new one is given.
	if host == "" {
		if rs, err := target.Remotes(ctx); err == nil {
			for _, r := range rs {
				if r.Name == name && r.Host != "" {
					host = r.Host
					if r.Port != 0 && r.Port != 8007 {
						host = net.JoinHostPort(r.Host, strconv.Itoa(r.Port))
					}
				}
			}
		}
	}
	if host == "" {
		host = SourceAddress(src)
	}
	h, port, err := splitHostPort(host)
	if err != nil {
		return "", fmt.Errorf("Adresse der Quelle: %w", err)
	}
	// Datastores the remote must be able to read: this one and those of existing jobs.
	stores := map[string]bool{}
	if sourceStore != "" {
		stores[sourceStore] = true
	}
	if jobs, err := target.Jobs(ctx, "sync"); err == nil {
		for _, j := range jobs {
			if j.Remote == name && j.RemoteStore != "" {
				stores[j.RemoteStore] = true
			}
		}
	}
	authID, secret := src.AuthID, src.Secret
	if tok, err := c.syncTokenFor(ctx, src, targetSrv, stores); err == nil {
		authID, secret = tok.TokenID, tok.Secret
	} // otherwise: the appliance's token, as before (e.g. the source does not allow new users)
	err = target.PutRemote(ctx, prox.Remote{Name: name, Host: h, Port: port, AuthID: authID, Fingerprint: src.Fingerprint,
		Comment: "PK95 ProxBackup: " + src.Name}, secret)
	if err != nil {
		return "", fmt.Errorf("Remote auf %s anlegen: %w", targetSrv.Name, err)
	}
	return name, nil
}

// syncTokenFor returns the read-only token on the source for copies to the target (created
// once) and grants it read access to the datastores.
func (c *Core) syncTokenFor(ctx context.Context, src, target PBSServer, stores map[string]bool) (syncToken, error) {
	pbs := c.PBSClient(src)
	key := "synctoken:" + src.ID + ">" + target.ID
	var tok syncToken
	_ = c.St.GetSetting(key, &tok)
	if tok.TokenID == "" {
		if err := pbs.EnsureUser(ctx, syncUser, "PK95 ProxBackup: Kopien zu anderen Backup-Servern"); err != nil {
			return tok, err
		}
		id, secret, err := pbs.NewToken(ctx, syncUser, "to-"+tokenNameRe.ReplaceAllString(target.ID, "-"), "liest für Kopierjobs auf "+target.Name)
		if err != nil {
			return tok, err
		}
		tok = syncToken{TokenID: id, Secret: secret}
		if err := c.St.SetSetting(key, tok); err != nil {
			return tok, err
		}
	}
	for s := range stores {
		if err := pbs.SetACL(ctx, "/datastore/"+s, "DatastoreReader", tok.TokenID); err != nil {
			return tok, err
		}
	}
	return tok, nil
}

// RemoteCheck is the result of letting the target connect to the source.
type RemoteCheck struct {
	OK     bool     `json:"ok"`
	Host   string   `json:"host"`
	Stores []string `json:"stores"`
	Error  string   `json:"error,omitempty"`
	Hint   string   `json:"hint,omitempty"`
}

// CheckRemote sets up the remote and lets the target list the datastores of the source: the
// connection is tested from where it is used, not from the appliance.
func (c *Core) CheckRemote(ctx context.Context, targetID, sourceID, sourceStore, host string) RemoteCheck {
	res := RemoteCheck{Stores: []string{}}
	target, tsrv, err := c.PBS(targetID)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	src, err := c.Server(sourceID)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if host == "" {
		host = SourceAddress(src)
	}
	res.Host = host
	name, err := c.RemoteFor(ctx, target, tsrv, sourceID, sourceStore, host)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if name == "" {
		res.OK = true
		return res
	}
	stores, err := target.ScanRemote(ctx, name)
	if err != nil {
		res.Error = err.Error()
		res.Hint = remoteHint(err.Error(), tsrv.Name, src.Name, host)
		return res
	}
	res.OK, res.Stores = true, stores
	return res
}

// remoteHint explains a failed connection from the target to the source.
func remoteHint(msg, target, source, host string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "timed out") || strings.Contains(m, "timeout") || strings.Contains(m, "no route") || strings.Contains(m, "unreachable"):
		return target + " erreicht " + source + " unter " + host + " nicht (keine Antwort). Netzwerk, VPN und Firewall prüfen: TCP-Port 8007 muss vom Ziel zur Quelle offen sein."
	case strings.Contains(m, "refused"):
		return "Unter " + host + " antwortet kein Backup-Server (Verbindung abgelehnt). Adresse und Port prüfen."
	case strings.Contains(m, "fingerprint") || strings.Contains(m, "certificate") || strings.Contains(m, "tls") || strings.Contains(m, "ssl"):
		return "Unter " + host + " antwortet ein anderer Server als " + source + " (Zertifikat passt nicht). Adresse prüfen – oder bei " + source + " unter Verbindungen den Fingerabdruck aktualisieren."
	case strings.Contains(m, "401") || strings.Contains(m, "authentication") || strings.Contains(m, "permission"):
		return source + " lehnt die Anmeldung ab. Verbindung zu " + source + " unter Verbindungen neu speichern."
	case strings.Contains(m, "resolve") || strings.Contains(m, "lookup") || strings.Contains(m, "name or service"):
		return "Der Name " + host + " lässt sich auf " + target + " nicht auflösen. IP-Adresse eintragen oder DNS prüfen."
	}
	return target + " konnte " + source + " unter " + host + " nicht abfragen."
}
