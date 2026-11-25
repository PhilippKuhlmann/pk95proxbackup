package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"pk95proxbackup/internal/hostexec"
	"pk95proxbackup/internal/prox"
)

// Service check of a restore test: after the start the appliance looks whether the services
// the plan names listen on their ports – inside the guest, since the test guest has no network:
// VMs through the guest agent (/proc/net/tcp on Linux, netstat on Windows), containers with
// pct exec over the SSH access to the node (as for the host backup).

// ServiceResult is one checked port.
type ServiceResult struct {
	Port int  `json:"port"`
	OK   bool `json:"ok"`
}

var servicesLineRe = regexp.MustCompile(`^\s*(\d+)\s*:\s*([\d,;\s]*)$`)

// ParseServices reads the ports of a plan: one line per guest, "100: 443, 3306".
func ParseServices(text string) (map[string][]int, error) {
	out := map[string][]int{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := servicesLineRe.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("Zeile %d: bitte als „ID: Port, Port“ schreiben, z. B. „100: 443, 3306“", i+1)
		}
		seen := map[int]bool{}
		for _, p := range strings.FieldsFunc(m[2], func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' }) {
			n, err := strconv.Atoi(p)
			if err != nil || n < 1 || n > 65535 {
				return nil, fmt.Errorf("Zeile %d: „%s“ ist kein Port", i+1, p)
			}
			if !seen[n] {
				seen[n] = true
				out[m[1]] = append(out[m[1]], n)
			}
		}
		if len(out[m[1]]) == 0 {
			return nil, fmt.Errorf("Zeile %d: kein Port angegeben", i+1)
		}
		sort.Ints(out[m[1]])
	}
	return out, nil
}

// listeningProc reads the listening TCP ports from /proc/net/tcp and tcp6 (state 0A).
func listeningProc(text string) map[int]bool {
	out := map[int]bool{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[3] != "0A" {
			continue
		}
		_, hexPort, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		if p, err := strconv.ParseUint(hexPort, 16, 16); err == nil {
			out[int(p)] = true
		}
	}
	return out
}

// listeningNetstat reads the listening TCP ports from "netstat -an" of Windows.
func listeningNetstat(text string) map[int]bool {
	out := map[int]bool{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || !strings.HasPrefix(f[0], "TCP") || !strings.EqualFold(f[3], "LISTENING") {
			continue
		}
		i := strings.LastIndex(f[1], ":")
		if p, err := strconv.Atoi(f[1][i+1:]); i >= 0 && err == nil {
			out[p] = true
		}
	}
	return out
}

// errNoAgent: a VM without a guest agent cannot be looked into.
var errNoAgent = errors.New("ohne Gast-Agent lässt sich in die VM nicht hineinsehen")

// listeningPorts are the TCP ports a test guest listens on.
func (c *Core) listeningPorts(ctx context.Context, pve *prox.PVE, cl PVECluster, node, typ string, vmid int, agent bool) (map[int]bool, error) {
	if typ == "qemu" {
		if !agent {
			return nil, errNoAgent
		}
		if text, err := pve.AgentFileRead(ctx, node, vmid, "/proc/net/tcp"); err == nil {
			v6, _ := pve.AgentFileRead(ctx, node, vmid, "/proc/net/tcp6")
			return listeningProc(text + "\n" + v6), nil
		}
		// no /proc: Windows
		out, _, err := pve.AgentExec(ctx, node, vmid, []string{"netstat", "-an"})
		if err != nil {
			return nil, fmt.Errorf("Gast-Agent: %w", err)
		}
		return listeningNetstat(out), nil
	}
	run, err := c.nodeShell(ctx, pve, cl, node)
	if err != nil {
		return nil, err
	}
	out, err := run.Run(ctx, "pct", "exec", strconv.Itoa(vmid), "--", "sh", "-c", "cat /proc/net/tcp /proc/net/tcp6 2>/dev/null; true")
	if err != nil {
		return nil, fmt.Errorf("pct exec auf %s: %w", node, err)
	}
	return listeningProc(out), nil
}

// nodeShell is the SSH access to a node of a cluster – the one the host backup uses.
func (c *Core) nodeShell(ctx context.Context, pve *prox.PVE, cl PVECluster, node string) (hostexec.Runner, error) {
	conf := c.hostConfs()[cl.ID]
	if c.NodeRunner != nil {
		return c.NodeRunner(cl, node), nil
	}
	if conf.User == "" {
		return nil, errors.New("für Container braucht die Prüfung den SSH-Zugang zu den Knoten – unter Proxmox-VE-Hosts einrichten")
	}
	addr := conf.Addrs[node]
	if addr == "" {
		addrs, _, _, err := hostAddrs(ctx, pve, cl)
		if err != nil {
			return nil, err
		}
		addr = addrs[node]
	}
	if addr == "" {
		return nil, fmt.Errorf("keine Adresse für %s bekannt", node)
	}
	return c.nodeRunner(cl, conf, node, addr), nil
}
