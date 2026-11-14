package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
)

// Protection against deleting backups (by mistake or by ransomware that took over Proxmox VE):
//   - Proxmox VE logs in to the Backup Server with a token that may back up and read, but not
//     delete or prune (role DatastoreBackup instead of DatastorePowerUser). Retention is then the
//     job of the Backup Server (prune job of the datastore).
//   - The last backup of each month and year is protected for a while (PBS "protected" flag:
//     neither prune nor delete removes it).
//   - When many backups of a datastore disappear at once, the appliance raises an alarm.

// ProtectSettings are the settings of the protection.
type ProtectSettings struct {
	Months      int  `json:"months"`       // protect the last backup of each of the last N complete months
	Years       int  `json:"years"`        // … of each of the last N complete years
	NewStorages bool `json:"new_storages"` // new PVE storages get a token without delete right
}

// ProtectSettings returns the stored settings.
func (c *Core) ProtectSettings() ProtectSettings {
	var s ProtectSettings
	_ = c.St.GetSetting("protect", &s)
	return s
}

// SetProtectSettings stores the settings and applies the automatic protection at once.
func (c *Core) SetProtectSettings(s ProtectSettings) error {
	if s.Months < 0 || s.Months > 120 || s.Years < 0 || s.Years > 30 {
		return errors.New("Monate 0–120, Jahre 0–30.")
	}
	if err := c.St.SetSetting("protect", s); err != nil {
		return err
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		_ = c.RunAutoProtect(ctx)
	}()
	return nil
}

// MatchServer finds the Backup Server a PVE storage points to: by address first, then by
// certificate fingerprint (servers may share one certificate, e.g. a wildcard certificate).
func (c *Core) MatchServer(st prox.Storage) (PBSServer, bool) {
	servers, _ := c.Servers()
	for _, srv := range servers {
		if st.Server != "" && (st.Server == srv.Host || st.Server == srv.PublicHost) && !srv.Local {
			return srv, true
		}
		if srv.Local && st.Server == srv.PublicHost && st.Server != "" {
			return srv, true
		}
	}
	for _, srv := range servers {
		if prox.NormalizeFingerprint(st.Fingerprint) != "" && prox.NormalizeFingerprint(st.Fingerprint) == prox.NormalizeFingerprint(srv.Fingerprint) {
			return srv, true
		}
	}
	return PBSServer{}, false
}

// ---------- delete right of Proxmox VE ----------

// deleteRoles may delete or prune backups.
var deleteRoles = []string{"Admin", "DatastoreAdmin", "DatastorePowerUser"}

// StorageGuard is the delete right of one PBS storage of a cluster.
type StorageGuard struct {
	Cluster     string   `json:"cluster"`
	ClusterName string   `json:"cluster_name"`
	Storage     string   `json:"storage"`
	Server      string   `json:"server,omitempty"`
	ServerName  string   `json:"server_name,omitempty"`
	Datastore   string   `json:"datastore"`
	AuthID      string   `json:"auth_id"`
	State       string   `json:"state"`             // protected, open, user, unknown
	Problem     string   `json:"problem,omitempty"` // why it cannot be switched here
	Retention   []string `json:"retention"`         // retention Proxmox VE applies itself
}

// canDelete reports whether authID may delete in store, and the ACL path that grants it.
func canDelete(acls []prox.ACL, authID, store string) (bool, string) {
	for _, a := range acls {
		if a.UGID != authID || !slices.Contains(deleteRoles, a.Role) {
			continue
		}
		if a.Path == "/datastore/"+store {
			return true, a.Path
		}
		if a.Propagate && (a.Path == "/" || a.Path == "/datastore") {
			return true, a.Path
		}
	}
	return false, ""
}

func keepsAll(prune string) bool {
	return prune == "" || strings.Contains(prune, "keep-all=1")
}

// guardOf judges one storage with the ACLs of its server.
func guardOf(g *StorageGuard, acls []prox.ACL) {
	user, _, isToken := strings.Cut(g.AuthID, "!")
	if !isToken {
		g.State, g.Problem = "user", "Proxmox VE meldet sich mit dem Benutzer "+g.AuthID+" an, nicht mit einem Token – dessen Rechte lassen sich hier nicht begrenzen. Speicher austragen und über die Appliance neu eintragen."
		return
	}
	tokDel, tokPath := canDelete(acls, g.AuthID, g.Datastore)
	userDel := user == "root@pam"
	if !userDel {
		userDel, _ = canDelete(acls, user, g.Datastore)
	}
	g.State, g.Problem = "protected", ""
	if tokDel && userDel {
		g.State = "open"
		if tokPath != "/datastore/"+g.Datastore {
			g.Problem = "Das Löschrecht kommt von einem Eintrag für „" + tokPath + "“ auf dem Backup-Server. Den bitte dort unter Konfiguration → Berechtigungen entfernen."
		}
	}
}

// StorageGuards lists the PBS storages of all clusters with their delete right.
func (c *Core) StorageGuards(ctx context.Context) ([]StorageGuard, []string) {
	clusters, _ := c.Clusters()
	out := []StorageGuard{}
	var errs []string
	acls := map[string][]prox.ACL{}
	aclErr := map[string]error{}
	for _, cl := range clusters {
		pve := c.PVEClient(cl)
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		storages, err := pve.Storages(cctx)
		jobs, _ := pve.BackupJobs(cctx)
		cancel()
		if err != nil {
			errs = append(errs, cl.Name+": "+err.Error())
			continue
		}
		for _, st := range storages {
			if st.Type != "pbs" {
				continue
			}
			g := StorageGuard{Cluster: cl.ID, ClusterName: cl.Name, Storage: st.Storage, Datastore: st.Datastore, AuthID: st.Username, Retention: []string{}}
			if !keepsAll(st.PruneBackups) {
				g.Retention = append(g.Retention, "Speicher "+st.Storage+": "+st.PruneBackups)
			}
			for _, j := range jobs {
				if j.Storage == st.Storage && j.IsEnabled() && !keepsAll(j.PruneBackups) {
					name := j.Comment
					if name == "" {
						name = j.ID
					}
					g.Retention = append(g.Retention, "Job „"+name+"“: "+j.PruneBackups)
				}
			}
			srv, ok := c.MatchServer(st)
			if !ok {
				g.State, g.Problem = "unknown", "Der Backup-Server dieses Speichers ist nicht unter Verbindungen eingetragen."
				out = append(out, g)
				continue
			}
			g.Server, g.ServerName = srv.ID, srv.Name
			if _, done := acls[srv.ID]; !done && aclErr[srv.ID] == nil {
				sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
				list, err := c.PBSClient(srv).ACLs(sctx)
				cancel()
				if err != nil {
					aclErr[srv.ID] = err
					errs = append(errs, srv.Name+": "+err.Error())
				} else {
					acls[srv.ID] = list
				}
			}
			if aclErr[srv.ID] != nil {
				g.State, g.Problem = "unknown", "Rechte auf "+srv.Name+" nicht lesbar."
			} else {
				guardOf(&g, acls[srv.ID])
			}
			out = append(out, g)
		}
	}
	return out, errs
}

// SetStorageGuard takes the delete right of a storage away (on) or gives it back.
func (c *Core) SetStorageGuard(ctx context.Context, clusterID, storage string, on bool, by string) (StorageGuard, error) {
	guards, _ := c.StorageGuards(ctx)
	var g StorageGuard
	for _, x := range guards {
		if x.Cluster == clusterID && x.Storage == storage {
			g = x
		}
	}
	if g.Storage == "" {
		return g, errors.New("Speicher nicht gefunden.")
	}
	if g.State == "user" || g.State == "unknown" {
		return g, errors.New(g.Problem)
	}
	pbs, srv, err := c.PBS(g.Server)
	if err != nil {
		return g, err
	}
	path := "/datastore/" + g.Datastore
	if on {
		acls, err := pbs.ACLs(ctx)
		if err != nil {
			return g, err
		}
		// first the new rights, then the old ones away: backups keep running throughout
		for _, role := range []string{"DatastoreBackup", "DatastoreReader"} {
			if err := pbs.SetACL(ctx, path, role, g.AuthID); err != nil {
				return g, fmt.Errorf("Rechte setzen: %w", err)
			}
		}
		for _, a := range acls {
			if a.UGID == g.AuthID && a.Path == path && slices.Contains(deleteRoles, a.Role) {
				if err := pbs.RemoveACL(ctx, path, a.Role, g.AuthID); err != nil {
					return g, fmt.Errorf("Recht %s entfernen: %w", a.Role, err)
				}
			}
		}
	} else {
		ids := []string{g.AuthID}
		if user, _, _ := strings.Cut(g.AuthID, "!"); user != "root@pam" {
			ids = append(ids, user)
		}
		for _, id := range ids {
			if err := pbs.SetACL(ctx, path, "DatastorePowerUser", id); err != nil {
				return g, fmt.Errorf("Rechte setzen: %w", err)
			}
		}
	}
	acls, err := pbs.ACLs(ctx)
	if err != nil {
		return g, err
	}
	guardOf(&g, acls)
	if on && g.State != "protected" {
		return g, errors.New(g.Problem)
	}
	if on {
		c.Event("info", g.ClusterName, "Löschschutz für "+g.Storage+" eingeschaltet",
			"Proxmox VE darf auf "+srv.Name+" / "+g.Datastore+" nur noch sichern und lesen, aber nichts löschen (von "+by+"). Aufräumen übernimmt der Backup-Server.")
	} else {
		c.Event("warning", g.ClusterName, "Löschschutz für "+g.Storage+" ausgeschaltet",
			"Proxmox VE darf auf "+srv.Name+" / "+g.Datastore+" wieder Sicherungen löschen (von "+by+").")
	}
	return g, nil
}

// ---------- monthly and yearly backups ----------

type autoProtectState struct {
	Protected map[string]int64 `json:"protected"` // snapshots the appliance protected
	Exempt    map[string]bool  `json:"exempt"`    // unprotected by hand: leave alone
	Last      int64            `json:"last"`
	Error     string           `json:"error,omitempty"`
}

// AutoProtectInfo is shown in the settings.
type AutoProtectInfo struct {
	Protected int    `json:"protected"`
	Last      int64  `json:"last"`
	Error     string `json:"error,omitempty"`
}

var autoProtectMu sync.Mutex

const autoProtectEvery = 6 * time.Hour

func snapKey(srv, store string, s prox.Snapshot) string {
	return srv + "|" + store + "|" + s.Type + "|" + s.ID + "|" + strconv.FormatInt(s.Time, 10)
}

func (c *Core) autoProtect() autoProtectState {
	var st autoProtectState
	_ = c.St.GetSetting("autoprotect", &st)
	if st.Protected == nil {
		st.Protected = map[string]int64{}
	}
	if st.Exempt == nil {
		st.Exempt = map[string]bool{}
	}
	return st
}

// AutoProtectInfo returns the state of the automatic protection.
func (c *Core) AutoProtectInfo() AutoProtectInfo {
	st := c.autoProtect()
	return AutoProtectInfo{Protected: len(st.Protected), Last: st.Last, Error: st.Error}
}

// AutoProtected returns the keys of the snapshots the appliance protected itself.
func (c *Core) AutoProtected() map[string]int64 { return c.autoProtect().Protected }

// SnapKey is the key of a snapshot in AutoProtected.
func SnapKey(srv, store string, s prox.Snapshot) string { return snapKey(srv, store, s) }

func (c *Core) scheduleAutoProtect(now time.Time) {
	st := c.autoProtect()
	if now.Unix()-st.Last < int64(autoProtectEvery.Seconds()) {
		return
	}
	set := c.ProtectSettings()
	if set.Months == 0 && set.Years == 0 && len(st.Protected) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		_ = c.RunAutoProtect(ctx)
	}()
}

// wantedProtected returns the snapshots to protect: the newest of each of the last `months`
// complete months and of each of the last `years` complete years, per backup group.
func wantedProtected(snaps []prox.Snapshot, months, years int, now time.Time) map[int]bool {
	curMonth := now.Year()*12 + int(now.Month()) - 1
	byMonth := map[string]int{}
	byYear := map[string]int{}
	for i, s := range snaps {
		if s.Type != "vm" && s.Type != "ct" {
			continue
		}
		t := time.Unix(s.Time, 0).In(now.Location())
		g := s.Type + "/" + s.ID
		m := t.Year()*12 + int(t.Month()) - 1
		if months > 0 && m < curMonth && m >= curMonth-months {
			k := g + "|" + strconv.Itoa(m)
			if j, ok := byMonth[k]; !ok || snaps[j].Time < s.Time {
				byMonth[k] = i
			}
		}
		if years > 0 && t.Year() < now.Year() && t.Year() >= now.Year()-years {
			k := g + "|" + strconv.Itoa(t.Year())
			if j, ok := byYear[k]; !ok || snaps[j].Time < s.Time {
				byYear[k] = i
			}
		}
	}
	out := map[int]bool{}
	for _, i := range byMonth {
		out[i] = true
	}
	for _, i := range byYear {
		out[i] = true
	}
	return out
}

// RunAutoProtect protects the monthly and yearly backups on all fixed datastores and lifts the
// protection of those that fell out of the window.
func (c *Core) RunAutoProtect(ctx context.Context) error {
	autoProtectMu.Lock()
	defer autoProtectMu.Unlock()
	set := c.ProtectSettings()
	st := c.autoProtect()
	now := time.Now()
	var problems []string
	servers, _ := c.Servers()
	added, lifted := 0, 0
	for _, srv := range servers {
		pbs := c.PBSClient(srv)
		stores, err := pbs.Datastores(ctx)
		if err != nil {
			problems = append(problems, srv.Name+": "+err.Error())
			continue
		}
		for _, ds := range stores {
			if ds.BackingDevice != "" { // removable disks: their content is managed by the rotation
				continue
			}
			snaps, err := pbs.Snapshots(ctx, ds.Name, "")
			if err != nil {
				problems = append(problems, srv.Name+" / "+ds.Name+": "+err.Error())
				continue
			}
			want := wantedProtected(snaps, set.Months, set.Years, now)
			wanted := map[string]bool{}
			for i, s := range snaps {
				k := snapKey(srv.ID, ds.Name, s)
				if !want[i] {
					continue
				}
				wanted[k] = true
				if st.Exempt[k] || s.Protected {
					continue // lifted by hand, or protected already (by hand or by us)
				}
				if err := pbs.SetProtected(ctx, ds.Name, "", s.Type, s.ID, s.Time, true); err != nil {
					problems = append(problems, fmt.Sprintf("%s / %s %s/%s: %v", srv.Name, ds.Name, s.Type, s.ID, err))
					continue
				}
				st.Protected[k] = now.Unix()
				added++
			}
			// lift what fell out of the window (only what the appliance protected itself)
			exists := map[string]prox.Snapshot{}
			for _, s := range snaps {
				exists[snapKey(srv.ID, ds.Name, s)] = s
			}
			prefix := srv.ID + "|" + ds.Name + "|"
			for k := range st.Protected {
				if !strings.HasPrefix(k, prefix) || wanted[k] {
					continue
				}
				if s, ok := exists[k]; ok && s.Protected {
					if err := pbs.SetProtected(ctx, ds.Name, "", s.Type, s.ID, s.Time, false); err != nil {
						problems = append(problems, fmt.Sprintf("%s / %s %s/%s: %v", srv.Name, ds.Name, s.Type, s.ID, err))
						continue
					}
					lifted++
				}
				delete(st.Protected, k)
			}
			for k := range st.Exempt {
				if _, ok := exists[k]; strings.HasPrefix(k, prefix) && !ok {
					delete(st.Exempt, k)
				}
			}
		}
	}
	st.Last = now.Unix()
	st.Error = strings.Join(problems, "; ")
	if err := c.St.SetSetting("autoprotect", st); err != nil {
		return err
	}
	if added > 0 || lifted > 0 {
		log.Printf("autoprotect: %d protected, %d released", added, lifted)
	}
	if len(problems) > 0 {
		return errors.New(st.Error)
	}
	return nil
}

// ProtectSnapshot protects one snapshot by hand or lifts the protection. Lifting an automatic
// protection by hand keeps the appliance from setting it again.
func (c *Core) ProtectSnapshot(ctx context.Context, srvID, store, typ, id string, t int64, on bool) error {
	pbs, _, err := c.PBS(srvID)
	if err != nil {
		return err
	}
	if err := pbs.SetProtected(ctx, store, "", typ, id, t, on); err != nil {
		return err
	}
	autoProtectMu.Lock()
	defer autoProtectMu.Unlock()
	st := c.autoProtect()
	k := snapKey(srvID, store, prox.Snapshot{Type: typ, ID: id, Time: t})
	if on {
		delete(st.Exempt, k)
	} else if _, mine := st.Protected[k]; mine {
		delete(st.Protected, k)
		st.Exempt[k] = true
	}
	return c.St.SetSetting("autoprotect", st)
}

// ---------- alarm on unusual deletions ----------

type inventory struct {
	At     int64          `json:"at"`
	Groups map[string]int `json:"groups"`
}

const deletionWatchEvery = 10 * time.Minute

var deletionMu sync.Mutex

func (c *Core) scheduleDeletionWatch(now time.Time) {
	var last int64
	_ = c.St.GetSetting("deletionwatch", &last)
	if now.Unix()-last < int64(deletionWatchEvery.Seconds()) {
		return
	}
	_ = c.St.SetSetting("deletionwatch", now.Unix())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		c.WatchDeletions(ctx)
	}()
}

// WatchDeletions compares the backups of every fixed datastore with the last look and raises an
// alarm when a whole backup group or many backups disappeared.
func (c *Core) WatchDeletions(ctx context.Context) {
	deletionMu.Lock()
	defer deletionMu.Unlock()
	servers, _ := c.Servers()
	for _, srv := range servers {
		pbs := c.PBSClient(srv)
		stores, err := pbs.Datastores(ctx)
		if err != nil {
			continue
		}
		for _, ds := range stores {
			if ds.BackingDevice != "" {
				continue
			}
			snaps, err := pbs.Snapshots(ctx, ds.Name, "")
			if err != nil {
				continue
			}
			cur := inventory{At: time.Now().Unix(), Groups: map[string]int{}}
			for _, s := range snaps {
				cur.Groups[s.Type+"/"+s.ID]++
			}
			key := "inventory:" + srv.ID + "|" + ds.Name
			var prev inventory
			_ = c.St.GetSetting(key, &prev)
			_ = c.St.SetSetting(key, cur)
			if prev.At == 0 {
				continue
			}
			c.judgeDeletions(ctx, srv, ds.Name, prev, cur)
		}
	}
}

func (c *Core) judgeDeletions(ctx context.Context, srv PBSServer, store string, prev, cur inventory) {
	total, removed := 0, 0
	var vanished []string
	for g, n := range prev.Groups {
		total += n
		if m := cur.Groups[g]; m < n {
			removed += n - m
			if m == 0 {
				vanished = append(vanished, g)
			}
		}
	}
	mass := removed >= 5 && removed*5 >= total
	if len(vanished) == 0 && !mass {
		return
	}
	sort.Strings(vanished)
	names := c.guestNames(ctx)
	var lines []string
	for _, g := range vanished {
		name := ""
		if _, id, ok := strings.Cut(g, "/"); ok && names[id] != "" {
			name = " " + names[id]
		}
		lines = append(lines, fmt.Sprintf("%s%s: alle %d Sicherungen weg", g, name, prev.Groups[g]))
	}
	if len(lines) > 30 {
		lines = append(lines[:30], fmt.Sprintf("… und %d weitere", len(lines)-30))
	}
	mins := int(time.Since(time.Unix(prev.At, 0)).Round(time.Minute).Minutes())
	title := fmt.Sprintf("%d Sicherungen auf %s gelöscht", removed, store)
	body := fmt.Sprintf("Auf %s / %s sind in den letzten %d Minuten %d von %d Sicherungen verschwunden", srv.Name, store, max(mins, 1), removed, total)
	if len(vanished) > 0 {
		body += fmt.Sprintf(", von %d Servern alle", len(vanished))
	}
	body += ". Falls das niemand absichtlich getan hat: Zugänge zu Proxmox VE und zum Backup-Server prüfen, Passwörter ändern und den Löschschutz einschalten (Einstellungen → Löschschutz). Wechsel- und Archiv-Platten sind nicht betroffen, solange sie nicht angesteckt werden."
	c.EventDetails("critical", srv.Name, title, body, &notify.Details{Status: "error", Link: "/einstellungen/loeschschutz",
		Facts: []notify.Fact{{Label: "Datastore", Value: srv.Name + " / " + store}, {Label: "Gelöscht", Value: fmt.Sprintf("%d von %d", removed, total)}},
		Log:   lines})
}

// ForgetSnapshot notes a deletion the appliance made itself, so it raises no alarm.
func (c *Core) ForgetSnapshot(srvID, store, typ, id string) {
	deletionMu.Lock()
	defer deletionMu.Unlock()
	key := "inventory:" + srvID + "|" + store
	var inv inventory
	if c.St.GetSetting(key, &inv) != nil || inv.At == 0 {
		return
	}
	g := typ + "/" + id
	if inv.Groups[g] > 0 {
		inv.Groups[g]--
		if inv.Groups[g] == 0 {
			delete(inv.Groups, g)
		}
		_ = c.St.SetSetting(key, inv)
	}
}
