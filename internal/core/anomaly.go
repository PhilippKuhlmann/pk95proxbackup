package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
)

// Unusual backups: the log of every PBS backup task says how much new data the client sent
// ("Upload size"). Ransomware that encrypts files, or a service that rewrites its data,
// shows up as a sudden jump; a backup much smaller than usual means data went missing.
// Either way the appliance raises a warning and protects the last normal backup of the guest,
// so the retention cannot remove it while someone looks into it.

const (
	anomalySource      = "Sicherungen"
	anomalyScanEvery   = 10 * time.Minute
	anomalyBackfill    = 14 * 24 * time.Hour // first scan: learn from the last two weeks
	anomalyFresh       = 48 * time.Hour      // older backups only feed the history
	anomalyMinHistory  = 5
	anomalyBase        = 14 // backups the normal value is taken from
	anomalyKeep        = 30
	anomalyBurstFactor = 5
	anomalyBurstMin    = 1 << 30 // at least 1 GiB of new data
	anomalyBurstFloor  = 64 << 20
	anomalyShrinkMin   = 1 << 30 // only guests that are normally at least 1 GiB
	anomalyPerScan     = 200
)

// BackupAnomaly is one unusual backup.
type BackupAnomaly struct {
	ID         string `json:"id"`
	ServerID   string `json:"server_id"`
	ServerName string `json:"server_name"`
	Store      string `json:"store"`
	NS         string `json:"ns,omitempty"`
	Type       string `json:"type"` // vm, ct
	GuestID    string `json:"guest_id"`
	Name       string `json:"name,omitempty"`
	Time       int64  `json:"time"`
	Kind       string `json:"kind"` // burst, shrink
	Upload     int64  `json:"upload"`
	NormalUp   int64  `json:"normal_upload"`
	Size       int64  `json:"size"`
	NormalSize int64  `json:"normal_size"`
	// Kept is the backup time of the last normal backup, protected against the retention.
	Kept    int64  `json:"kept,omitempty"`
	KeptOwn bool   `json:"kept_own,omitempty"` // protected by the appliance (released on ack)
	Found   int64  `json:"found"`
	AckAt   int64  `json:"ack_at,omitempty"`
	AckBy   string `json:"ack_by,omitempty"`
}

type backupSample struct {
	T    int64 `json:"t"`
	Up   int64 `json:"u"`
	Size int64 `json:"s,omitempty"`
}

type anomalyState struct {
	Seen     map[string]int64          `json:"seen"` // task → when it was read
	Hist     map[string][]backupSample `json:"hist"` // server|store|ns|type/id → backups
	List     []BackupAnomaly           `json:"list"`
	LastScan map[string]int64          `json:"last_scan"`
	Behind   map[string]bool           `json:"behind,omitempty"` // more to read than one scan took
	Version  int                       `json:"version"`
}

// anomalyVersion: 1.25.0 marked tasks as read without understanding their logs (time stamps
// in front of the lines); version 2 reads them again.
const anomalyVersion = 2

type anomalyRuntime struct{ busy atomic.Bool }

func (c *Core) anomalyState() anomalyState {
	var st anomalyState
	_ = c.St.GetSetting("anomalies", &st)
	if st.Version < anomalyVersion {
		st = anomalyState{List: st.List, Version: anomalyVersion}
	}
	if st.Seen == nil {
		st.Seen = map[string]int64{}
	}
	if st.Hist == nil {
		st.Hist = map[string][]backupSample{}
	}
	if st.LastScan == nil {
		st.LastScan = map[string]int64{}
	}
	if st.Behind == nil {
		st.Behind = map[string]bool{}
	}
	return st
}

// Anomalies lists the unusual backups, newest first.
func (c *Core) Anomalies() []BackupAnomaly {
	list := slices.Clone(c.anomalyState().List)
	sort.Slice(list, func(i, j int) bool { return list[i].Time > list[j].Time })
	return list
}

// OpenAnomalies are the anomalies nobody has looked at yet.
func (c *Core) OpenAnomalies() []BackupAnomaly {
	out := []BackupAnomaly{}
	for _, a := range c.Anomalies() {
		if a.AckAt == 0 {
			out = append(out, a)
		}
	}
	return out
}

// scheduleAnomalies starts a scan in the background when none runs (called every minute;
// each server is read at most every ten minutes).
func (c *Core) scheduleAnomalies(now time.Time) {
	if c.anomaly.busy.Load() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		c.ScanAnomalies(ctx, now)
	}()
}

// ScanAnomalies reads the new backup tasks of every Backup Server.
func (c *Core) ScanAnomalies(ctx context.Context, now time.Time) {
	if !c.anomaly.busy.CompareAndSwap(false, true) {
		return
	}
	defer c.anomaly.busy.Store(false)
	servers, _ := c.Servers()
	for _, srv := range servers {
		st := c.anomalyState()
		if now.Unix()-st.LastScan[srv.ID] < int64(anomalyScanEvery/time.Second) {
			continue
		}
		c.scanServerAnomalies(ctx, srv, now)
	}
}

func (c *Core) scanServerAnomalies(ctx context.Context, srv PBSServer, now time.Time) {
	pbs, _, err := c.PBS(srv.ID)
	if err != nil {
		return
	}
	st := c.anomalyState()
	since := now.Add(-anomalyBackfill).Unix()
	if last := st.LastScan[srv.ID]; last > 0 && !st.Behind[srv.ID] {
		since = max(since, last-24*3600)
	}
	tasks, err := pbs.BackupTasks(ctx, since)
	if err != nil {
		return
	}
	var todo []prox.Task
	for _, t := range tasks {
		if t.Type == "backup" && t.EndTime > 0 && t.Status == "OK" && st.Seen[t.UPID] == 0 {
			todo = append(todo, t)
		}
	}
	sort.Slice(todo, func(i, j int) bool { return todo[i].StartTime < todo[j].StartTime })
	behind := len(todo) > anomalyPerScan
	if behind {
		todo = todo[:anomalyPerScan]
	}
	names := map[string]string{}
	if len(todo) > 0 {
		names = c.guestNames(ctx)
	}
	snaps := map[string][]prox.Snapshot{} // store|ns → snapshots
	snapsOf := func(store, ns string) []prox.Snapshot {
		k := store + "|" + ns
		if s, ok := snaps[k]; ok {
			return s
		}
		s, _ := pbs.Snapshots(ctx, store, ns)
		snaps[k] = s
		return s
	}
	var found []BackupAnomaly
	for _, t := range todo {
		bs, ok, err := pbs.BackupStatsOf(ctx, t.UPID)
		if err != nil {
			continue // read again next time
		}
		st.Seen[t.UPID] = now.Unix()
		if !ok || bs.Type != "vm" && bs.Type != "ct" { // host backups (configuration) are no guests
			continue
		}
		store, _, _ := strings.Cut(t.WorkerID, ":")
		var size int64
		for _, s := range snapsOf(store, bs.NS) {
			if s.Type == bs.Type && s.ID == bs.ID && s.Time == bs.Time {
				size = s.Size
			}
		}
		key := srv.ID + "|" + store + "|" + bs.NS + "|" + bs.Type + "/" + bs.ID
		hist := st.Hist[key]
		sample := backupSample{T: bs.Time, Up: bs.Upload, Size: size}
		if now.Unix()-t.EndTime <= int64(anomalyFresh/time.Second) {
			if kind, normUp, normSize := judgeBackup(hist, sample); kind != "" {
				a := BackupAnomaly{ID: strconv.FormatInt(bs.Time, 36) + "-" + bs.Type + bs.ID + "-" + srv.ID + "-" + store, ServerID: srv.ID, ServerName: srv.Name,
					Store: store, NS: bs.NS, Type: bs.Type, GuestID: bs.ID, Name: names[bs.Type+"/"+bs.ID], Time: bs.Time, Kind: kind,
					Upload: bs.Upload, NormalUp: normUp, Size: size, NormalSize: normSize, Found: now.Unix()}
				a.Kept, a.KeptOwn = c.keepLastNormal(ctx, pbs, store, bs, snapsOf(store, bs.NS))
				found = append(found, a)
			}
		}
		hist = append(hist, sample)
		sort.Slice(hist, func(i, j int) bool { return hist[i].T < hist[j].T })
		if len(hist) > anomalyKeep {
			hist = hist[len(hist)-anomalyKeep:]
		}
		st.Hist[key] = hist
	}
	// Forget read tasks after twice the backfill window; drop old answered anomalies.
	for k, v := range st.Seen {
		if now.Unix()-v > int64(2*anomalyBackfill/time.Second) {
			delete(st.Seen, k)
		}
	}
	st.List = slices.DeleteFunc(st.List, func(a BackupAnomaly) bool { return a.AckAt > 0 && now.Unix()-a.AckAt > 30*24*3600 })
	st.List = append(st.List, found...)
	st.LastScan[srv.ID] = now.Unix()
	st.Behind[srv.ID] = behind
	_ = c.St.SetSetting("anomalies", st)
	for _, a := range found {
		c.reportAnomaly(a)
	}
}

// judgeBackup compares a backup with the earlier ones of the same guest.
func judgeBackup(hist []backupSample, s backupSample) (kind string, normalUp, normalSize int64) {
	if len(hist) < anomalyMinHistory {
		return "", 0, 0
	}
	base := hist[max(len(hist)-anomalyBase, 0):]
	var ups, sizes []int64
	for _, h := range base {
		ups = append(ups, h.Up)
		if h.Size > 0 {
			sizes = append(sizes, h.Size)
		}
	}
	normalUp = median(ups)
	if len(sizes) >= anomalyMinHistory {
		normalSize = median(sizes)
	}
	if s.Up >= anomalyBurstMin && s.Up >= max(normalUp, anomalyBurstFloor)*anomalyBurstFactor {
		return "burst", normalUp, normalSize
	}
	if s.Size > 0 && normalSize >= anomalyShrinkMin && s.Size < normalSize/2 {
		return "shrink", normalUp, normalSize
	}
	return "", normalUp, normalSize
}

func median(v []int64) int64 {
	s := slices.Clone(v)
	slices.Sort(s)
	if len(s) == 0 {
		return 0
	}
	return s[len(s)/2]
}

// keepLastNormal protects the newest backup of the guest before the unusual one.
func (c *Core) keepLastNormal(ctx context.Context, pbs *prox.PBS, store string, bs prox.BackupStats, snaps []prox.Snapshot) (int64, bool) {
	var prev *prox.Snapshot
	for i, s := range snaps {
		if s.Type == bs.Type && s.ID == bs.ID && s.Time < bs.Time && (prev == nil || s.Time > prev.Time) {
			prev = &snaps[i]
		}
	}
	if prev == nil {
		return 0, false
	}
	if prev.Protected {
		return prev.Time, false
	}
	if err := pbs.SetProtected(ctx, store, bs.NS, prev.Type, prev.ID, prev.Time, true); err != nil {
		return 0, false
	}
	prev.Protected = true
	return prev.Time, true
}

// AnomalyText says in plain words what is unusual.
func AnomalyText(a BackupAnomaly) string {
	if a.Kind == "shrink" {
		return fmt.Sprintf("Sicherung nur %s groß, sonst etwa %s", fmtBytes(a.Size), fmtBytes(a.NormalSize))
	}
	return fmt.Sprintf("%s neue Daten, sonst etwa %s", fmtBytes(a.Upload), fmtBytes(a.NormalUp))
}

func anomalyGuest(a BackupAnomaly) string {
	g := strings.ToUpper(a.Type) + " " + a.GuestID
	if a.Name != "" {
		g += " (" + a.Name + ")"
	}
	return g
}

func (c *Core) reportAnomaly(a BackupAnomaly) {
	when := time.Unix(a.Time, 0).Format("02.01.2006 15:04")
	var body string
	if a.Kind == "shrink" {
		body = fmt.Sprintf("Die Sicherung von %s am %s ist nur %s groß – sonst sind es etwa %s. Möglicherweise wurden Daten gelöscht oder eine Platte fehlt in der Sicherung. ",
			anomalyGuest(a), when, fmtBytes(a.Size), fmtBytes(a.NormalSize))
	} else {
		body = fmt.Sprintf("Bei der Sicherung von %s am %s kamen %s neue Daten hinzu – sonst sind es etwa %s. Das kann ein Verschlüsselungstrojaner sein, der Dateien verschlüsselt, oder ein Dienst, der ungewöhnlich viel schreibt (Update, Datenbank, Defragmentierung). ",
			anomalyGuest(a), when, fmtBytes(a.Upload), fmtBytes(a.NormalUp))
	}
	if a.Kept > 0 {
		body += "Die letzte unauffällige Sicherung vom " + time.Unix(a.Kept, 0).Format("02.01.2006 15:04") + " ist geschützt und wird nicht aufgeräumt, bis jemand die Meldung als geprüft markiert. "
	}
	body += "Bitte den Server prüfen."
	facts := []notify.Fact{{Label: "Server", Value: anomalyGuest(a)}, {Label: "Befund", Value: AnomalyText(a)}, {Label: "Backup-Server", Value: a.ServerName + " / " + a.Store}}
	c.EventDetails("warning", anomalySource, "Auffällige Sicherung: "+anomalyGuest(a), body,
		&notify.Details{Status: "warning", Link: "/sicherungen?auffaellig=1", Facts: facts})
}

// AckAnomaly marks an anomaly as checked and lifts the protection the appliance set for it.
func (c *Core) AckAnomaly(ctx context.Context, id, by string) error {
	st := c.anomalyState()
	i := slices.IndexFunc(st.List, func(a BackupAnomaly) bool { return a.ID == id })
	if i < 0 {
		return errors.New("Meldung nicht gefunden.")
	}
	a := &st.List[i]
	if a.AckAt != 0 {
		return nil
	}
	if a.KeptOwn && !keptElsewhere(st, *a) {
		pbs, _, err := c.PBS(a.ServerID)
		if err != nil {
			return err
		}
		snap := prox.Snapshot{Type: a.Type, ID: a.GuestID, Time: a.Kept}
		if _, auto := c.AutoProtected()[snapKey(a.ServerID, a.Store, snap)]; !auto {
			if err := pbs.SetProtected(ctx, a.Store, a.NS, a.Type, a.GuestID, a.Kept, false); err != nil && !prox.IsStatus(err, 404) {
				return fmt.Errorf("Schutz aufheben: %w", err)
			}
		}
	}
	a.AckAt, a.AckBy = time.Now().Unix(), by
	return c.St.SetSetting("anomalies", st)
}

// keptElsewhere reports whether another open anomaly keeps the same backup.
func keptElsewhere(st anomalyState, a BackupAnomaly) bool {
	for _, b := range st.List {
		if b.ID != a.ID && b.AckAt == 0 && b.ServerID == a.ServerID && b.Store == a.Store && b.NS == a.NS && b.Type == a.Type && b.GuestID == a.GuestID && b.Kept == a.Kept {
			return true
		}
	}
	return false
}
