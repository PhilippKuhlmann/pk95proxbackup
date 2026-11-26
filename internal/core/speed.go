package core

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"pk95proxbackup/internal/notify"
	"pk95proxbackup/internal/prox"
)

// Backup speed per guest: how much a backup read (changed data of a VM, all of a container) per
// second, from the logs of the backup tasks. When the last two backups of a guest that read a
// fair amount ran at less than half its usual speed, the appliance says so (once a week per
// guest): slow or full disks of the Backup Server, the network, or a verification or garbage
// collection at the same time (see the time plan).

const (
	speedKeep    = 30
	speedMinRead = 1 << 30 // smaller backups say little about speed
	speedMinSecs = 20
)

// SpeedSample is one backup of a guest.
type SpeedSample struct {
	T     int64 `json:"t"`
	Secs  int64 `json:"secs"`
	Bytes int64 `json:"bytes"`
}

func (s SpeedSample) mibs() float64 { return float64(s.Bytes) / (1 << 20) / float64(s.Secs) }

func (s SpeedSample) meaningful() bool { return s.Bytes >= speedMinRead && s.Secs >= speedMinSecs }

type speedState struct {
	Name    string        `json:"name"`
	Samples []SpeedSample `json:"samples"`
}

// GuestSpeed is what the list of guests shows.
type GuestSpeed struct {
	Last   float64      `json:"last"`  // MiB/s of the newest backup that read enough
	Usual  float64      `json:"usual"` // median of the ones before (0: too few yet)
	Slow   bool         `json:"slow"`
	Points [][2]float64 `json:"points"` // [time, MiB/s] of the last backups, oldest first
}

// judgeSpeed compares the last two meaningful backups with the median of the ten before.
func judgeSpeed(samples []SpeedSample) GuestSpeed {
	var m []SpeedSample
	for _, s := range samples {
		if s.meaningful() {
			m = append(m, s)
		}
	}
	g := GuestSpeed{Points: [][2]float64{}}
	for _, s := range m[max(0, len(m)-14):] {
		g.Points = append(g.Points, [2]float64{float64(s.T), s.mibs()})
	}
	if len(m) == 0 {
		return g
	}
	g.Last = m[len(m)-1].mibs()
	if len(m) < 6 {
		return g
	}
	base := m[max(0, len(m)-12) : len(m)-2]
	v := make([]float64, len(base))
	for i, s := range base {
		v[i] = s.mibs()
	}
	sort.Float64s(v)
	g.Usual = v[len(v)/2]
	g.Slow = m[len(m)-1].mibs() < g.Usual/2 && m[len(m)-2].mibs() < g.Usual/2
	return g
}

func speedKey(clusterID, vmid string) string { return clusterID + "/" + vmid }

// recordSpeeds adds the guests of one backup task.
func (c *Core) recordSpeeds(cluster PVECluster, end int64, guests []prox.GuestBackup) {
	var st map[string]*speedState
	_ = c.St.GetSetting("backupspeed", &st)
	if st == nil {
		st = map[string]*speedState{}
	}
	changed := []string{}
	for _, g := range guests {
		secs, bytes := g.Seconds(), g.ReadBytes()
		if !g.OK || secs <= 0 || bytes <= 0 {
			continue
		}
		k := speedKey(cluster.ID, g.VMID)
		s := st[k]
		if s == nil {
			s = &speedState{}
			st[k] = s
		}
		if g.Name != "" {
			s.Name = g.Name
		}
		sample := SpeedSample{T: end, Secs: secs, Bytes: bytes}
		if slices.Contains(s.Samples, sample) {
			continue // a task read again (its run was not complete the first time)
		}
		s.Samples = append(s.Samples, sample)
		sort.Slice(s.Samples, func(i, j int) bool { return s.Samples[i].T < s.Samples[j].T })
		if len(s.Samples) > speedKeep {
			s.Samples = s.Samples[len(s.Samples)-speedKeep:]
		}
		changed = append(changed, k)
	}
	if len(changed) == 0 {
		return
	}
	_ = c.St.SetSetting("backupspeed", st)
	c.warnSlow(cluster, st, changed, time.Now())
}

// warnSlow reports guests whose backups got much slower – once a week each, and only for
// current backups (not when old logs are read the first time).
func (c *Core) warnSlow(cluster PVECluster, st map[string]*speedState, keys []string, now time.Time) {
	var warned map[string]int64
	_ = c.St.GetSetting("speedwarned", &warned)
	if warned == nil {
		warned = map[string]int64{}
	}
	dirty := false
	for _, k := range keys {
		s := st[k]
		j := judgeSpeed(s.Samples)
		last := s.Samples[len(s.Samples)-1]
		if !j.Slow || now.Unix()-last.T > 3*24*3600 || now.Unix()-warned[k] < 7*24*3600 {
			continue
		}
		warned[k], dirty = now.Unix(), true
		_, vmid, _ := strings.Cut(k, "/")
		who := strings.TrimSpace(vmid + " " + s.Name)
		c.EventDetails("warning", cluster.Name, "Sicherung von "+who+" ist deutlich langsamer geworden",
			fmt.Sprintf("Die letzten zwei Sicherungen von %s liefen mit etwa %.0f MB/s, sonst sind es %.0f MB/s. Mögliche Ursachen: langsame oder volle Platten am Backup-Server, das Netzwerk oder eine Prüfung bzw. „Speicher freigeben“ zur selben Zeit (siehe Zeitplan).", who, j.Last*1.048576, j.Usual*1.048576),
			&notify.Details{Status: "warning", Link: "/sicherungen"})
	}
	if dirty {
		_ = c.St.SetSetting("speedwarned", warned)
	}
}

// GuestSpeeds returns the speed of every guest ("cluster/vmid").
func (c *Core) GuestSpeeds() map[string]GuestSpeed {
	var st map[string]*speedState
	_ = c.St.GetSetting("backupspeed", &st)
	out := map[string]GuestSpeed{}
	for k, s := range st {
		out[k] = judgeSpeed(s.Samples)
	}
	return out
}
