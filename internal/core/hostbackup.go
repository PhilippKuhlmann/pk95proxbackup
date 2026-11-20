package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/hostexec"
	"pk95proxbackup/internal/prox"
)

// Host backup: Proxmox VE does not back up its own nodes. Once a day the appliance logs in to
// every node over SSH and runs proxmox-backup-client there, with the login Proxmox VE already
// has for its PBS storage (/etc/pve/priv/storage/<id>.pw) and its encryption key, if any. No
// secret leaves the node; the backup is group host/<node> next to the guests, so it goes
// along to the second server and the rotation disks. It holds /etc (with /etc/pve), /root and
// a folder "info" with what a rebuild needs (versions, network, disks, pools, packages, a
// consistent copy of the cluster database).

const (
	hostBackupType      = "host"
	hostBackupMarker    = "# pk95-hostbackup"
	hostCheckMarker     = "# pk95-hostcheck"
	hostBackupKeepStd   = 30
	hostBackupTimeStd   = "02:30"
	hostBackupTries     = 3
	hostBackupTimeout   = 30 * time.Minute
	hostBackupRetryWait = time.Hour
)

// HostBackupConf is the host backup of one cluster.
type HostBackupConf struct {
	Enabled bool              `json:"enabled"`
	Storage string            `json:"storage"` // PBS storage of Proxmox VE the nodes write to
	Time    string            `json:"time"`    // daily, HH:MM
	Keep    int               `json:"keep"`    // newest backups kept per node
	Port    int               `json:"port"`
	User    string            `json:"user"`
	Addrs   map[string]string `json:"addrs,omitempty"` // node → SSH address (empty: cluster address)
}

// HostNodeState is the result of the last backups of a node.
type HostNodeState struct {
	HostKey   string `json:"host_key,omitempty"`
	LastRun   int64  `json:"last_run"`
	LastOK    int64  `json:"last_ok"`
	LastError string `json:"last_error,omitempty"`
	Encrypted bool   `json:"encrypted"`
	Duration  int64  `json:"duration"`
	Tries     int    `json:"tries"`
}

type hostBackupRuntime struct {
	mu   sync.Mutex
	busy map[string]bool // cluster|node
}

// HostSnap is one backup of a node.
type HostSnap struct {
	Time      int64 `json:"time"`
	Size      int64 `json:"size"`
	Protected bool  `json:"protected"`
	Encrypted bool  `json:"encrypted"`
}

// HostNodeView is a node on the page.
type HostNodeView struct {
	Node      string        `json:"node"`
	Online    bool          `json:"online"`
	Addr      string        `json:"addr"`     // address used for SSH
	Detected  string        `json:"detected"` // address from the cluster
	HasKey    bool          `json:"has_key"`  // host key learned
	State     HostNodeState `json:"state"`
	Running   bool          `json:"running"`
	Snapshots []HostSnap    `json:"snapshots"`
}

// HostStorage is a PBS storage the nodes can write to.
type HostStorage struct {
	Storage    string `json:"storage"`
	Datastore  string `json:"datastore"`
	NS         string `json:"ns,omitempty"`
	Server     string `json:"server"`      // address in Proxmox VE
	User       string `json:"user"`        // login of Proxmox VE on the Backup Server
	ServerID   string `json:"server_id"`   // Backup Server of the appliance ("" = unknown)
	ServerName string `json:"server_name"` // its name
	Encrypted  bool   `json:"encrypted"`
	Disabled   bool   `json:"disabled"`
}

// HostBackupView is the host backup of one cluster as the page shows it.
type HostBackupView struct {
	Cluster     string         `json:"cluster"`
	ClusterName string         `json:"cluster_name"`
	Conf        HostBackupConf `json:"conf"`
	Configured  bool           `json:"configured"`
	Storages    []HostStorage  `json:"storages"`
	Target      *HostStorage   `json:"target,omitempty"`
	Nodes       []HostNodeView `json:"nodes"`
	Next        int64          `json:"next"`
	Error       string         `json:"error,omitempty"`
}

func (c *Core) hostConfs() map[string]HostBackupConf {
	m := map[string]HostBackupConf{}
	_ = c.St.GetSetting("hostbackup", &m)
	return m
}

func (c *Core) hostStates() map[string]map[string]HostNodeState {
	m := map[string]map[string]HostNodeState{}
	_ = c.St.GetSetting("hostbackup-state", &m)
	return m
}

// updateHostState changes the stored state of one node.
func (c *Core) updateHostState(cluster, node string, f func(*HostNodeState)) HostNodeState {
	c.hostBackup.mu.Lock()
	defer c.hostBackup.mu.Unlock()
	m := c.hostStates()
	if m[cluster] == nil {
		m[cluster] = map[string]HostNodeState{}
	}
	s := m[cluster][node]
	f(&s)
	m[cluster][node] = s
	_ = c.St.SetSetting("hostbackup-state", m)
	return s
}

func (c *Core) hostConf(cluster string) (HostBackupConf, bool) {
	conf, ok := c.hostConfs()[cluster]
	if conf.Time == "" {
		conf.Time = hostBackupTimeStd
	}
	if conf.Keep == 0 {
		conf.Keep = hostBackupKeepStd
	}
	if conf.Port == 0 {
		conf.Port = 22
	}
	if conf.User == "" {
		conf.User = "root"
	}
	return conf, ok
}

var hhmmRe = regexp.MustCompile(`^([01]?\d|2[0-3]):([0-5]\d)$`)

// SetHostBackup stores the host backup of a cluster.
func (c *Core) SetHostBackup(ctx context.Context, cluster string, conf HostBackupConf) (map[string]string, error) {
	if _, err := c.Cluster(cluster); err != nil {
		return nil, err
	}
	errs := map[string]string{}
	conf.Time = strings.TrimSpace(conf.Time)
	if m := hhmmRe.FindStringSubmatch(conf.Time); m == nil {
		errs["time"] = "Uhrzeit als HH:MM, z. B. 02:30."
	} else {
		h, _ := strconv.Atoi(m[1])
		conf.Time = fmt.Sprintf("%02d:%s", h, m[2])
	}
	if conf.Keep < 1 || conf.Keep > 365 {
		errs["keep"] = "1 bis 365 Stände."
	}
	if conf.Port == 0 {
		conf.Port = 22
	}
	if conf.Port < 1 || conf.Port > 65535 {
		errs["port"] = "Port 1 bis 65535."
	}
	conf.User = strings.TrimSpace(conf.User)
	if conf.User == "" {
		conf.User = "root"
	}
	if conf.User != "root" {
		errs["user"] = "Nur root kann /etc/pve/priv lesen."
	}
	for n, a := range conf.Addrs {
		a = strings.TrimSpace(a)
		if a == "" {
			delete(conf.Addrs, n)
			continue
		}
		if strings.ContainsAny(a, " /@") {
			errs["addr_"+n] = "Nur Adresse oder Name, ohne Benutzer und Port."
		}
		conf.Addrs[n] = a
	}
	if conf.Enabled || conf.Storage != "" {
		pve, _, err := c.PVE(cluster)
		if err != nil {
			return nil, err
		}
		storages, err := pve.Storages(ctx)
		if err != nil {
			return nil, err
		}
		found := false
		for _, st := range storages {
			if st.Storage == conf.Storage && st.Type == "pbs" {
				found = true
			}
		}
		if !found {
			errs["storage"] = "Einen Backup-Server-Speicher von Proxmox VE wählen."
		}
	}
	if len(errs) > 0 {
		return errs, nil
	}
	c.hostBackup.mu.Lock()
	m := c.hostConfs()
	m[cluster] = conf
	err := c.St.SetSetting("hostbackup", m)
	c.hostBackup.mu.Unlock()
	return nil, err
}

// hostAddrs reads the node addresses of a cluster (node → IP) and which nodes are online.
func hostAddrs(ctx context.Context, pve *prox.PVE, cl PVECluster) (map[string]string, map[string]bool, []string, error) {
	nodes, err := pve.Nodes(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	addrs, online := map[string]string{}, map[string]bool{}
	var names []string
	for _, n := range nodes {
		names = append(names, n.Node)
		online[n.Node] = n.Status == "online" || n.Status == ""
	}
	sort.Strings(names)
	if members, err := pve.ClusterStatus(ctx); err == nil {
		for _, m := range members {
			if m.Type == "node" && m.IP != "" {
				addrs[m.Name] = m.IP
			}
		}
	}
	// A single node is reached where its API is.
	if len(names) == 1 && addrs[names[0]] == "" {
		addrs[names[0]] = cl.Host
	}
	return addrs, online, names, nil
}

// hostStorages lists the PBS storages of a cluster with the Backup Server behind them.
func (c *Core) hostStorages(storages []prox.Storage) []HostStorage {
	out := []HostStorage{}
	for _, st := range storages {
		if st.Type != "pbs" {
			continue
		}
		h := HostStorage{Storage: st.Storage, Datastore: st.Datastore, NS: st.Namespace, Server: st.Server, User: st.Username, Encrypted: st.EncryptionKey != "", Disabled: st.Disable == 1}
		if srv, ok := c.MatchServer(st); ok {
			h.ServerID, h.ServerName = srv.ID, srv.Name
		}
		out = append(out, h)
	}
	// Storages of this appliance's servers first, then by name.
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].ServerID != "") != (out[j].ServerID != "") {
			return out[i].ServerID != ""
		}
		return out[i].Storage < out[j].Storage
	})
	return out
}

// HostBackups shows the host backup of every cluster.
func (c *Core) HostBackups(ctx context.Context) []HostBackupView {
	clusters, _ := c.Clusters()
	out := make([]HostBackupView, len(clusters))
	var wg sync.WaitGroup
	for i, cl := range clusters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = c.hostBackupView(ctx, cl)
		}()
	}
	wg.Wait()
	sort.SliceStable(out, func(i, j int) bool { return out[i].ClusterName < out[j].ClusterName })
	return out
}

func (c *Core) hostBackupView(ctx context.Context, cl PVECluster) HostBackupView {
	conf, configured := c.hostConf(cl.ID)
	v := HostBackupView{Cluster: cl.ID, ClusterName: cl.Name, Conf: conf, Configured: configured, Storages: []HostStorage{}, Nodes: []HostNodeView{}}
	if conf.Enabled {
		v.Next = nextDaily(conf.Time, time.Now())
	}
	pve := c.PVEClient(cl)
	addrs, online, names, err := hostAddrs(ctx, pve, cl)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	storages, err := pve.Storages(ctx)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	v.Storages = c.hostStorages(storages)
	if v.Conf.Storage == "" && len(v.Storages) > 0 {
		v.Conf.Storage = v.Storages[0].Storage // suggestion
	}
	for i := range v.Storages {
		if v.Storages[i].Storage == v.Conf.Storage {
			t := v.Storages[i]
			v.Target = &t
		}
	}
	snaps := map[string][]HostSnap{}
	if v.Target != nil && v.Target.ServerID != "" {
		if srv, err := c.Server(v.Target.ServerID); err == nil {
			if list, err := c.PBSClient(srv).Snapshots(ctx, v.Target.Datastore, v.Target.NS); err == nil {
				for _, s := range list {
					if s.Type == hostBackupType {
						snaps[s.ID] = append(snaps[s.ID], HostSnap{Time: s.Time, Size: s.Size, Protected: s.Protected, Encrypted: s.Fingerprint != ""})
					}
				}
			}
		}
	}
	states := c.hostStates()[cl.ID]
	c.hostBackup.mu.Lock()
	busy := map[string]bool{}
	for k, b := range c.hostBackup.busy {
		busy[k] = b
	}
	c.hostBackup.mu.Unlock()
	for _, n := range names {
		st := states[n]
		nv := HostNodeView{Node: n, Online: online[n], Detected: addrs[n], Addr: conf.Addrs[n], HasKey: st.HostKey != "", Running: busy[cl.ID+"|"+n],
			Snapshots: snaps[n]}
		if nv.Addr == "" {
			nv.Addr = addrs[n]
		}
		st.HostKey = ""
		nv.State = st
		if nv.Snapshots == nil {
			nv.Snapshots = []HostSnap{}
		}
		sort.Slice(nv.Snapshots, func(i, j int) bool { return nv.Snapshots[i].Time > nv.Snapshots[j].Time })
		v.Nodes = append(v.Nodes, nv)
	}
	return v
}

// nextDaily is the next time hh:mm (today or tomorrow).
func nextDaily(hhmm string, now time.Time) int64 {
	t := dailyAt(hhmm, now)
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t.Unix()
}

// lastDaily is the latest time hh:mm that is not after now.
func lastDaily(hhmm string, now time.Time) time.Time {
	t := dailyAt(hhmm, now)
	if t.After(now) {
		t = t.AddDate(0, 0, -1)
	}
	return t
}

func dailyAt(hhmm string, now time.Time) time.Time {
	h, m := 2, 30
	if p := hhmmRe.FindStringSubmatch(hhmm); p != nil {
		h, _ = strconv.Atoi(p[1])
		m, _ = strconv.Atoi(p[2])
	}
	return time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
}

// scheduleHostBackups starts the daily backups (also the one missed while the appliance was
// off) and retries a failed node every hour, three times at most.
func (c *Core) scheduleHostBackups(now time.Time) {
	confs := c.hostConfs()
	states := c.hostStates()
	var started map[string]int64
	_ = c.St.GetSetting("hostbackup-sched", &started)
	if started == nil {
		started = map[string]int64{}
	}
	changed := false
	for id, conf := range confs {
		if !conf.Enabled {
			continue
		}
		due := lastDaily(conf.Time, now).Unix()
		if started[id] < due {
			started[id] = now.Unix()
			changed = true
			go c.runHostBackupsBG(id, "", true)
			continue
		}
		for node, st := range states[id] {
			if st.LastError != "" && st.LastRun >= due && st.Tries < hostBackupTries && now.Sub(time.Unix(st.LastRun, 0)) >= hostBackupRetryWait {
				go c.runHostBackupsBG(id, node, false)
			}
		}
	}
	if changed {
		_ = c.St.SetSetting("hostbackup-sched", started)
	}
}

func (c *Core) runHostBackupsBG(cluster, node string, fresh bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
	defer cancel()
	if err := c.runHostBackups(ctx, cluster, node, fresh); err != nil {
		log.Printf("hostbackup %s: %v", cluster, err)
	}
}

// StartHostBackup backs up one node (or all nodes) of a cluster now, in the background.
func (c *Core) StartHostBackup(cluster, node string) error {
	conf, _ := c.hostConf(cluster)
	if conf.Storage == "" {
		return errors.New("Zuerst den Speicher für die Host-Sicherung wählen.")
	}
	c.hostBackup.mu.Lock()
	busy := c.hostBackup.busy[cluster+"|"+node]
	c.hostBackup.mu.Unlock()
	if busy {
		return errors.New("Die Sicherung läuft gerade.")
	}
	go c.runHostBackupsBG(cluster, node, true)
	return nil
}

// RunHostBackup backs up one node (or all) and waits for the result (tests).
func (c *Core) RunHostBackup(ctx context.Context, cluster, node string) error {
	return c.runHostBackups(ctx, cluster, node, true)
}

func (c *Core) runHostBackups(ctx context.Context, cluster, only string, fresh bool) error {
	cl, err := c.Cluster(cluster)
	if err != nil {
		return err
	}
	conf, _ := c.hostConf(cluster)
	pve := c.PVEClient(cl)
	addrs, online, names, err := hostAddrs(ctx, pve, cl)
	if err != nil {
		return err
	}
	storages, err := pve.Storages(ctx)
	if err != nil {
		return err
	}
	var st prox.Storage
	for _, s := range storages {
		if s.Storage == conf.Storage && s.Type == "pbs" {
			st = s
		}
	}
	var errs []string
	for _, n := range names {
		if only != "" && n != only {
			continue
		}
		if !online[n] {
			if only != "" {
				return fmt.Errorf("%s ist nicht online", n)
			}
			continue // a node that is off is no failure of the backup
		}
		addr := conf.Addrs[n]
		if addr == "" {
			addr = addrs[n]
		}
		if err := c.backupNode(ctx, cl, conf, st, n, addr, fresh); err != nil {
			errs = append(errs, n+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// nodeRunner is the SSH connection to a node (the demo replaces it).
func (c *Core) nodeRunner(cl PVECluster, conf HostBackupConf, node, addr string) hostexec.Runner {
	if c.NodeRunner != nil {
		return c.NodeRunner(cl, node)
	}
	st := c.hostStates()[cl.ID][node]
	return &hostexec.SSH{Host: addr, Port: conf.Port, User: conf.User, Signer: c.Signer, HostKey: st.HostKey, Timeout: hostBackupTimeout,
		OnLearn: func(key string) {
			// Trust on first use: remember the host key of the node.
			c.updateHostState(cl.ID, node, func(s *HostNodeState) {
				if s.HostKey == "" {
					s.HostKey = key
				}
			})
		}}
}

func (c *Core) backupNode(ctx context.Context, cl PVECluster, conf HostBackupConf, st prox.Storage, node, addr string, fresh bool) error {
	key := cl.ID + "|" + node
	c.hostBackup.mu.Lock()
	if c.hostBackup.busy == nil {
		c.hostBackup.busy = map[string]bool{}
	}
	if c.hostBackup.busy[key] {
		c.hostBackup.mu.Unlock()
		return errors.New("läuft bereits")
	}
	c.hostBackup.busy[key] = true
	c.hostBackup.mu.Unlock()
	defer func() {
		c.hostBackup.mu.Lock()
		delete(c.hostBackup.busy, key)
		c.hostBackup.mu.Unlock()
	}()
	start := time.Now()
	var out string
	var err error
	switch {
	case st.Storage == "":
		err = fmt.Errorf("Speicher „%s“ gibt es in Proxmox VE nicht (mehr)", conf.Storage)
	case addr == "":
		err = errors.New("keine Adresse für SSH bekannt – bei den Einstellungen eintragen")
	default:
		run := c.nodeRunner(cl, conf, node, addr)
		out, err = run.Run(ctx, "bash", "-c", hostBackupScript(st, node))
		if err == nil && !strings.Contains(out, "PK95-OK") {
			err = errors.New("unvollständig")
		}
		if err != nil {
			err = hostScriptError(out, err)
		}
	}
	prev := c.hostStates()[cl.ID][node]
	now := time.Now()
	c.updateHostState(cl.ID, node, func(s *HostNodeState) {
		s.LastRun = now.Unix()
		s.Duration = int64(now.Sub(start).Seconds())
		if fresh {
			s.Tries = 1
		} else {
			s.Tries++
		}
		if err != nil {
			s.LastError = err.Error()
			return
		}
		s.LastError, s.LastOK, s.Tries = "", now.Unix(), 0
		s.Encrypted = strings.Contains(out, "PK95-ENC")
	})
	if err != nil {
		if prev.LastError == "" {
			c.Event("warning", cl.Name, "Host-Sicherung von "+node+" fehlgeschlagen", err.Error()+"\n\nZiel: "+conf.Storage+". Die Appliance versucht es in einer Stunde erneut.")
		}
		return err
	}
	if prev.LastError != "" {
		c.Event("info", cl.Name, "Host-Sicherung von "+node+" wieder in Ordnung", "Ziel: "+conf.Storage)
	}
	c.pruneHostBackups(ctx, st, node, conf.Keep)
	return nil
}

// hostScriptError turns the output of the script into a short message.
func hostScriptError(out string, err error) error {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, l := range lines {
		if m, ok := strings.CutPrefix(strings.TrimSpace(l), "PK95-ERR: "); ok {
			return errors.New(m)
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if m, ok := strings.CutPrefix(l, "Error: "); ok {
			return errors.New("proxmox-backup-client: " + m)
		}
	}
	msg := err.Error()
	if strings.Contains(msg, "unable to authenticate") || strings.Contains(msg, "SSH-Anmeldung") {
		return errors.New("SSH-Anmeldung abgelehnt – den Schlüssel der Appliance auf dem Knoten eintragen")
	}
	return err
}

// pruneHostBackups keeps the newest backups of a node (protected ones stay).
func (c *Core) pruneHostBackups(ctx context.Context, st prox.Storage, node string, keep int) {
	srv, ok := c.MatchServer(st)
	if !ok || keep < 1 {
		return
	}
	pbs := c.PBSClient(srv)
	snaps, err := pbs.Snapshots(ctx, st.Datastore, st.Namespace)
	if err != nil {
		return
	}
	var mine []prox.Snapshot
	for _, s := range snaps {
		if s.Type == hostBackupType && s.ID == node {
			mine = append(mine, s)
		}
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].Time > mine[j].Time })
	for i := keep; i < len(mine); i++ {
		if !mine[i].Protected {
			_ = pbs.DeleteSnapshot(ctx, st.Datastore, st.Namespace, hostBackupType, node, mine[i].Time)
		}
	}
}

// HostCheck is the result of a connection test to a node.
type HostCheck struct {
	Version  string   `json:"version"`
	Problems []string `json:"problems"`
}

// CheckHostAccess logs in to a node and checks what the backup needs.
func (c *Core) CheckHostAccess(ctx context.Context, cluster, node string) (HostCheck, error) {
	res := HostCheck{Problems: []string{}}
	cl, err := c.Cluster(cluster)
	if err != nil {
		return res, err
	}
	conf, _ := c.hostConf(cluster)
	addrs, _, names, err := hostAddrs(ctx, c.PVEClient(cl), cl)
	if err != nil {
		return res, err
	}
	known := false
	for _, n := range names {
		known = known || n == node
	}
	if !known {
		return res, fmt.Errorf("Knoten %s gibt es in %s nicht", node, cl.Name)
	}
	addr := conf.Addrs[node]
	if addr == "" {
		addr = addrs[node]
	}
	if addr == "" {
		return res, errors.New("keine Adresse für SSH bekannt")
	}
	out, err := c.nodeRunner(cl, conf, node, addr).Run(ctx, "bash", "-c", hostCheckScript(conf.Storage))
	if err != nil {
		return res, hostScriptError(out, err)
	}
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "pve-manager/"):
			res.Version = l
		case strings.HasPrefix(l, "PK95-MISS: "):
			res.Problems = append(res.Problems, strings.TrimPrefix(l, "PK95-MISS: "))
		}
	}
	if res.Version == "" {
		res.Problems = append(res.Problems, "kein Proxmox VE (pveversion fehlt)")
	}
	return res, nil
}

func hostRepository(st prox.Storage) string {
	host := st.Server
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		host = "[" + host + "]"
	}
	if st.Port != 0 && st.Port != 8007 {
		host += ":" + strconv.Itoa(st.Port)
	}
	return st.Username + "@" + host + ":" + st.Datastore
}

func hostCheckScript(storage string) string {
	return strings.NewReplacer("@STORE@", hostexec.ShellQuote(storage)).Replace(hostCheckMarker + `
STORE=@STORE@
pveversion 2>/dev/null | head -n1
command -v proxmox-backup-client >/dev/null 2>&1 || echo "PK95-MISS: proxmox-backup-client ist nicht installiert"
[ -d /etc/pve/nodes ] || echo "PK95-MISS: /etc/pve ist nicht eingehängt (pve-cluster läuft nicht)"
if [ -n "$STORE" ] && [ ! -r "/etc/pve/priv/storage/$STORE.pw" ]; then echo "PK95-MISS: Kennwort des Speichers $STORE fehlt (/etc/pve/priv/storage/$STORE.pw)"; fi
df -Pk /tmp | awk 'NR==2 && $4 < 262144 {print "PK95-MISS: weniger als 256 MiB frei in /tmp"}'
exit 0
`)
}

// hostReadme lies in the folder "info" of every backup.
const hostReadme = `Host-Sicherung von PK95 ProxBackup
==================================

Archive dieser Sicherung:
  etc   – /etc des Knotens samt /etc/pve (Gäste, Speicher, Benutzer, Firewall, Cluster)
  root  – /root (Skripte, SSH-Schlüssel; ohne ISO- und Disk-Images)
  info  – dieser Ordner: Versionen, Netzwerk, Platten, ZFS/LVM, Pakete, Bootloader,
          config.db (Datenbank von /etc/pve, konsistente Kopie)

Knoten neu aufbauen:
  1. Proxmox VE in derselben Version (pveversion.txt) mit gleichem Hostnamen und
     gleicher IP installieren.
  2. /etc/network/interfaces und /etc/hosts aus "etc" übernehmen, Netzwerk neu laden.
  3. ZFS-Pools importieren (zpool import -f <pool>), LVM wird von selbst erkannt.
  4. Einzelner Knoten: Gäste aus etc/pve/qemu-server und etc/pve/lxc nach /etc/pve/…
     kopieren, storage.cfg vergleichen und übernehmen.
     Cluster: den neuen Knoten mit "pvecm add <anderer Knoten>" aufnehmen – /etc/pve
     kommt dann von den anderen Knoten.
  5. Ganzer Cluster verloren: systemctl stop pve-cluster; config.db nach
     /var/lib/pve-cluster/ kopieren; systemctl start pve-cluster.

Wiederherstellen auf der Kommandozeile:
  proxmox-backup-client restore host/<knoten>/<zeit> etc.pxar /root/wiederherstellung/etc \
    --repository <benutzer>@<server>:<datastore> [--keyfile <schlüssel>]
`

// hostBackupScript runs on the node: info folder, then proxmox-backup-client with the login
// of the PVE storage.
func hostBackupScript(st prox.Storage, node string) string {
	return strings.NewReplacer(
		"@STORE@", hostexec.ShellQuote(st.Storage),
		"@ID@", hostexec.ShellQuote(node),
		"@NS@", hostexec.ShellQuote(st.Namespace),
		"@REPO@", hostexec.ShellQuote(hostRepository(st)),
		"@FP@", hostexec.ShellQuote(st.Fingerprint),
		"@README@", hostReadme,
	).Replace(hostBackupMarker + `
set -u
STORE=@STORE@
ID=@ID@
NS=@NS@
FP=@FP@
fail() { echo "PK95-ERR: $*"; exit 3; }
command -v proxmox-backup-client >/dev/null 2>&1 || fail "proxmox-backup-client ist auf dem Knoten nicht installiert"
[ -d /etc/pve/nodes ] || fail "/etc/pve ist nicht eingehängt (pve-cluster läuft nicht)"
PW="/etc/pve/priv/storage/$STORE.pw"
[ -r "$PW" ] || fail "Kennwort des Speichers $STORE fehlt ($PW)"
export PBS_REPOSITORY=@REPO@
PBS_PASSWORD="$(cat "$PW")"; export PBS_PASSWORD
[ -n "$FP" ] && export PBS_FINGERPRINT="$FP"
set --
if [ -f "/etc/pve/priv/storage/$STORE.enc" ]; then set -- --keyfile "/etc/pve/priv/storage/$STORE.enc"; echo PK95-ENC; fi
[ -n "$NS" ] && set -- "$@" --ns "$NS"
INFO="$(mktemp -d /tmp/pk95-host.XXXXXX)" || fail "kein Platz in /tmp"
trap 'rm -rf "$INFO"' EXIT
cd "$INFO" || fail "kein Platz in /tmp"
grab() { f="$1"; shift; command -v "$1" >/dev/null 2>&1 || return 0; "$@" >"$f" 2>&1 || true; }
grab pveversion.txt pveversion -v
grab hostname.txt hostname -f
grab ip-addr.txt ip -d address
grab ip-route.txt ip route show table all
grab lsblk.txt lsblk -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS,MODEL,SERIAL,UUID
grab blkid.txt blkid
grab df.txt df -hT
grab zpool-status.txt zpool status
grab zpool-list.txt zpool list -v
grab zfs-list.txt zfs list -o name,used,avail,refer,mountpoint
grab lvm.txt sh -c 'pvs; echo; vgs; echo; lvs -a'
grab cluster.txt sh -c 'pvecm status; echo; pvecm nodes'
grab guests.txt sh -c 'qm list; echo; pct list'
grab packages.txt dpkg-query -W -f='${Package}\t${Version}\n'
grab boot.txt sh -c 'proxmox-boot-tool status; echo; efibootmgr -v; echo; cat /proc/cmdline'
grab crontabs.txt sh -c 'for f in /var/spool/cron/crontabs/*; do [ -f "$f" ] && { echo "== $f"; cat "$f"; }; done'
if command -v sqlite3 >/dev/null 2>&1; then
  sqlite3 /var/lib/pve-cluster/config.db ".backup '$INFO/config.db'" 2>/dev/null || cp /var/lib/pve-cluster/config.db "$INFO/" 2>/dev/null
else
  cp /var/lib/pve-cluster/config.db "$INFO/" 2>/dev/null
fi
cat >LIESMICH.txt <<'PK95TXT'
@README@PK95TXT
cd /
proxmox-backup-client backup etc.pxar:/etc root.pxar:/root info.pxar:"$INFO" --include-dev /etc/pve \
  --backup-type host --backup-id "$ID" "$@" \
  --exclude '*.iso' --exclude '*.img' --exclude '*.qcow2' --exclude '*.raw' --exclude '*.vmdk' \
  --exclude '*.vma' --exclude '*.vma.*' --exclude '.cache' 2>&1
rc=$?
[ "$rc" -eq 0 ] && echo PK95-OK
exit "$rc"
`)
}

// HostIssue is a node whose host backup failed or is older than two days.
type HostIssue struct {
	Cluster     string `json:"cluster"`
	ClusterName string `json:"cluster_name"`
	Node        string `json:"node"`
	Error       string `json:"error,omitempty"`
	LastOK      int64  `json:"last_ok"`
}

// HostBackupIssues lists the problems of the host backups from the stored state (no requests).
func (c *Core) HostBackupIssues() []HostIssue {
	out := []HostIssue{}
	clusters, _ := c.Clusters()
	confs, states := c.hostConfs(), c.hostStates()
	for _, cl := range clusters {
		if !confs[cl.ID].Enabled {
			continue
		}
		nodes := make([]string, 0, len(states[cl.ID]))
		for n := range states[cl.ID] {
			nodes = append(nodes, n)
		}
		sort.Strings(nodes)
		for _, n := range nodes {
			s := states[cl.ID][n]
			// Retries run for three hours: report a failure only when they are used up.
			failed := s.LastError != "" && (s.Tries >= hostBackupTries || time.Since(time.Unix(s.LastOK, 0)) > 26*time.Hour)
			stale := s.LastOK > 0 && time.Since(time.Unix(s.LastOK, 0)) > 50*time.Hour
			if failed || stale {
				out = append(out, HostIssue{Cluster: cl.ID, ClusterName: cl.Name, Node: n, Error: s.LastError, LastOK: s.LastOK})
			}
		}
	}
	return out
}
