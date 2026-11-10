package core

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/prox"
)

// RunningTask is a task that is running right now, with its progress.
type RunningTask struct {
	Source    string        `json:"source"` // pbs:<id> / pve:<id> / app
	Server    string        `json:"server"`
	UPID      string        `json:"upid,omitempty"`
	Type      string        `json:"type"`
	Label     string        `json:"label"`
	ID        string        `json:"id,omitempty"`
	Node      string        `json:"node,omitempty"`
	StartTime int64         `json:"starttime"`
	Progress  prox.Progress `json:"progress"`
}

var phaseLabel = map[string]string{
	"pruning": "räumt alte Sicherungen auf", "gc": "gibt Speicher frei",
	"unmounting": "wird ausgehängt", "mounting": "wird eingehängt",
}

// RunningTasks collects the running tasks of all servers and the appliance's own work
// (aftercare of rotation disks). Each log is read from its tail to find the progress.
func (c *Core) RunningTasks(ctx context.Context) []RunningTask {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	servers, _ := c.Servers()
	clusters, _ := c.Clusters()
	rots, _ := c.Rotations()
	// sync job id → "Wechselplatte <label>"
	diskOf := map[string]string{}
	for _, r := range rots {
		for _, d := range r.Disks {
			diskOf[r.SyncJobID(d)] = "Wechselplatte " + d.Label
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	out := []RunningTask{}
	add := func(t RunningTask) {
		mu.Lock()
		out = append(out, t)
		mu.Unlock()
	}
	for _, s := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pbs := c.PBSClient(s)
			list, err := pbs.RunningTasks(ctx)
			if err != nil {
				return
			}
			var inner sync.WaitGroup
			for _, t := range list {
				if t.EndTime != 0 {
					continue
				}
				inner.Add(1)
				go func() {
					defer inner.Done()
					rt := RunningTask{Source: "pbs:" + s.ID, Server: s.Name, UPID: t.UPID, Type: t.Type, ID: t.WorkerID, StartTime: t.StartTime,
						Label: TaskLabel(t.Type), Progress: prox.Progress{Percent: -1}}
					if t.Type == "syncjob" {
						// PBS: "<remote>:<remote-store>:<store>:<ns>:<job id>" (systemd-escaped)
						for _, part := range strings.Split(unescapeWorkerID(t.WorkerID), ":") {
							if l, ok := diskOf[part]; ok {
								rt.Label = "Kopie auf " + l
							}
						}
					}
					if lines, err := pbs.TailLog(ctx, t.UPID, 60); err == nil {
						rt.Progress = prox.ParseProgress(lines)
					}
					add(rt)
				}()
			}
			inner.Wait()
		}()
	}
	for _, cl := range clusters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pve := c.PVEClient(cl)
			list, err := pve.Tasks(ctx)
			if err != nil {
				return
			}
			var inner sync.WaitGroup
			for _, t := range list {
				if t.EndTime != 0 || t.Status != "" {
					continue
				}
				inner.Add(1)
				go func() {
					defer inner.Done()
					rt := RunningTask{Source: "pve:" + cl.ID, Server: cl.Name, UPID: t.UPID, Type: t.Type, ID: t.ID, Node: t.Node,
						StartTime: t.StartTime, Label: TaskLabel(t.Type), Progress: prox.Progress{Percent: -1}}
					if lines, err := pve.TailLog(ctx, t.UPID, 60); err == nil {
						rt.Progress = prox.ParseProgress(lines)
					}
					add(rt)
				}()
			}
			inner.Wait()
		}()
	}
	wg.Wait()
	// The appliance's own steps after a copy (no PBS task while it waits between them).
	c.mu.Lock()
	for _, r := range rots {
		for _, d := range r.Disks {
			rt := c.rotRT[rtKey(r, d)]
			if rt == nil || rt.Phase == "" {
				continue
			}
			out = append(out, RunningTask{Source: "app", Server: "Appliance", Type: "rotation", Label: "Wechselplatte " + d.Label,
				ID: r.Name, StartTime: rt.PhaseSince, Progress: prox.Progress{Percent: -1, Step: phaseLabel[rt.Phase]}})
		}
	}
	c.mu.Unlock()
	if r := c.ActiveRestoreTest(); r != nil {
		done, cur := 0, ""
		for _, x := range r.Results {
			if x.Status != "pending" && x.Status != "running" {
				done++
			}
			if x.Status == "running" {
				cur = strings.TrimSpace(x.Name + " (" + x.VMID + "): " + x.Phase)
			}
		}
		pct := -1.0
		if len(r.Results) > 0 {
			pct = float64(done) * 100 / float64(len(r.Results))
		}
		out = append(out, RunningTask{Source: "app", Server: "Appliance", Type: "restoretest", Label: "Wiederherstellungstest " + r.PlanName,
			StartTime: r.Start, Progress: prox.Progress{Percent: pct, Step: cur, Detail: fmt.Sprintf("%d von %d Servern", done, len(r.Results))}})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartTime < out[j].StartTime })
	// A rotation copy or a backup that just ended: let the monitor start the aftercare and
	// send the report right away instead of at its next minute.
	now := map[string]bool{}
	for _, t := range out {
		if t.Type == "syncjob" || t.Type == "vzdump" {
			now[t.UPID] = true
		}
	}
	c.mu.Lock()
	ended := false
	for u := range c.runningSync {
		if !now[u] {
			ended = true
		}
	}
	c.runningSync = now
	c.mu.Unlock()
	if ended {
		c.RequestRefresh()
	}
	return out
}

// unescapeWorkerID undoes the systemd-style escaping (\x2d → -) of task worker ids.
func unescapeWorkerID(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x' {
			if v, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
