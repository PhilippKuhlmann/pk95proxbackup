package core

import (
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Access describes how the appliance is reached from outside (reverse proxy).
type Access struct {
	// PublicURL is the address of the web UI used in mail links, e.g.
	// https://backup.example.org. Empty = the detected proxy address or the IP.
	PublicURL string `json:"public_url"`
	// TrustedProxies are reverse proxies whose X-Forwarded-For header is believed
	// (for the login lock-out and the event log).
	TrustedProxies []string `json:"trusted_proxies"`
}

// Seen is what the appliance noticed about a reverse proxy in front of it.
type Seen struct {
	ProxyIP string `json:"proxy_ip,omitempty"`
	URL     string `json:"url,omitempty"`
	At      int64  `json:"at,omitempty"`
}

var (
	seenMu sync.Mutex
	seen   Seen
)

// Access returns the access settings.
func (c *Core) Access() Access {
	var a Access
	_ = c.St.GetSetting("access", &a)
	return a
}

// proxyHostRe is a host name with an optional port, as a reverse proxy forwards it.
var proxyHostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,62}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,62}[A-Za-z0-9])?)*(:[0-9]{1,5})?$`)

// NoteProxy remembers a request that came through a reverse proxy (shown in the settings to
// be adopted; used for links only when the proxy is trusted, see PublicURL).
func (c *Core) NoteProxy(proxyIP, proto, host string) {
	if host == "" || len(host) > 253 || !proxyHostRe.MatchString(host) {
		return
	}
	if h, _, err := net.SplitHostPort(host); err == nil && net.ParseIP(h) != nil {
		return
	}
	if net.ParseIP(host) != nil {
		return
	}
	switch proto {
	case "":
		proto = "https"
	case "http", "https":
	default:
		return
	}
	seenMu.Lock()
	seen = Seen{ProxyIP: proxyIP, URL: proto + "://" + host, At: time.Now().Unix()}
	seenMu.Unlock()
}

// SeenProxy returns the last reverse proxy noticed (empty when none).
func (c *Core) SeenProxy() Seen {
	seenMu.Lock()
	defer seenMu.Unlock()
	return seen
}

// PublicURL is the address used in links: the setting, else a trusted proxy seen in front of
// the appliance, else https://<IP>. The headers of an untrusted client never become a link.
func (c *Core) PublicURL() string {
	if u := strings.TrimRight(c.Access().PublicURL, "/"); u != "" {
		return u
	}
	if s := c.SeenProxy(); s.URL != "" && s.ProxyIP != "" && c.TrustsProxy(s.ProxyIP) {
		return s.URL
	}
	if ip := PrimaryIP(); ip != "" {
		return "https://" + ip
	}
	return ""
}

// TrustsProxy reports whether ip is a configured reverse proxy.
func (c *Core) TrustsProxy(ip string) bool {
	for _, p := range c.Access().TrustedProxies {
		p = strings.TrimSpace(p)
		if p == ip {
			return true
		}
		if _, n, err := net.ParseCIDR(p); err == nil && n.Contains(net.ParseIP(ip)) {
			return true
		}
	}
	return false
}
