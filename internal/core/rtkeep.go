package core

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pk95proxbackup/internal/prox"
)

// Kept test guests: a restore test plan can leave its test guests on the node – a test system
// with the newest backups, apart from production (network cut, or on a bridge of its own). Each
// run replaces a kept copy by a fresh one; guests the plan no longer tests, or all of them when
// keeping is switched off or the plan deleted, are removed. A kept copy carries the tag
// pk95-testsystem instead of pk95-restoretest, so the sweep for left-over test guests leaves it.

// KeptGuest is a test guest that stayed after its test.
type KeptGuest struct {
	Cluster  string `json:"cluster"`
	Node     string `json:"node"`
	Type     string `json:"type"` // qemu, lxc
	VMID     int    `json:"vmid"`
	Name     string `json:"name"`      // name of the copy in Proxmox VE
	Orig     string `json:"orig"`      // vm/100
	OrigName string `json:"orig_name"` // name of the original
	Backup   int64  `json:"backup"`    // time of the backup it came from
	Restored int64  `json:"restored"`
	Status   string `json:"status"` // result of its test: ok, warning, error
	Running  bool   `json:"running"`
	Network  string `json:"network"` // "getrennt" or the bridge
}

var keptMu sync.Mutex

func (c *Core) keptAll() map[string]map[string]KeptGuest {
	var m map[string]map[string]KeptGuest
	_ = c.St.GetSetting("rtkept", &m)
	if m == nil {
		m = map[string]map[string]KeptGuest{}
	}
	return m
}

// updateKept changes the record of one plan under the lock.
func (c *Core) updateKept(plan string, f func(m map[string]KeptGuest)) {
	keptMu.Lock()
	defer keptMu.Unlock()
	all := c.keptAll()
	m := all[plan]
	if m == nil {
		m = map[string]KeptGuest{}
	}
	f(m)
	if len(m) == 0 {
		delete(all, plan)
	} else {
		all[plan] = m
	}
	_ = c.St.SetSetting("rtkept", all)
}

func (c *Core) keptOf(plan string) map[string]KeptGuest {
	keptMu.Lock()
	defer keptMu.Unlock()
	return c.keptAll()[plan]
}

func (c *Core) keptGuest(plan, key string) (KeptGuest, bool) {
	k, ok := c.keptOf(plan)[key]
	return k, ok
}

// KeptGuests lists the kept test guests per plan, by ID.
func (c *Core) KeptGuests() map[string][]KeptGuest {
	keptMu.Lock()
	all := c.keptAll()
	keptMu.Unlock()
	out := map[string][]KeptGuest{}
	for plan, m := range all {
		list := make([]KeptGuest, 0, len(m))
		for _, k := range m {
			list = append(list, k)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].VMID < list[j].VMID })
		out[plan] = list
	}
	return out
}

// removeKept deletes a kept copy – only while it still carries the tag (the ID may belong to
// another guest by now) – and forgets it. A copy that is gone already is just forgotten.
func (c *Core) removeKept(ctx context.Context, plan, key string, k KeptGuest) error {
	forget := func() { c.updateKept(plan, func(m map[string]KeptGuest) { delete(m, key) }) }
	pve, _, err := c.PVE(k.Cluster)
	if err != nil {
		return err
	}
	cfg, err := pve.GuestConfig(ctx, k.Node, k.Type, k.VMID)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			forget()
			return nil
		}
		return err
	}
	if !hasTag(fmt.Sprint(cfg["tags"]), rtKeptTag) {
		forget()
		return fmt.Errorf("Gast %d trägt die Markierung %s nicht mehr – nicht gelöscht", k.VMID, rtKeptTag)
	}
	if err := c.stopAndDestroy(ctx, pve, k.Node, k.Type, k.VMID); err != nil {
		return err
	}
	forget()
	return nil
}

// dropUnwantedKept removes kept copies of guests the plan no longer tests (all of them when it
// keeps none).
func (c *Core) dropUnwantedKept(ctx context.Context, plan RestoreTestPlan, todo []prox.Snapshot) {
	want := map[string]bool{}
	if plan.Keep {
		for _, s := range todo {
			want[s.Type+"/"+s.ID] = true
		}
	}
	for key, k := range c.keptOf(plan.ID) {
		if want[key] {
			continue
		}
		if err := c.removeKept(ctx, plan.ID, key, k); err != nil {
			c.Event("warning", "Wiederherstellungstest", fmt.Sprintf("Behaltenen Test-Gast %d nicht entfernt", k.VMID), err.Error())
		}
	}
}

// DeleteKept removes all kept copies of a plan in the background (button, plan deleted).
func (c *Core) DeleteKept(plan, by string) error {
	rtMu.Lock()
	busy := rtCur != nil && rtCur.run.PlanID == plan
	rtMu.Unlock()
	if busy {
		return fmt.Errorf("Der Plan testet gerade – bitte warten, bis der Test fertig ist.")
	}
	m := c.keptOf(plan)
	if len(m) == 0 {
		return nil
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		var done, failed []string
		for key, k := range m {
			if err := c.removeKept(ctx, plan, key, k); err != nil {
				failed = append(failed, fmt.Sprintf("%d %s: %s", k.VMID, k.Name, err.Error()))
			} else {
				done = append(done, strconv.Itoa(k.VMID))
			}
		}
		sort.Strings(done)
		c.SyncTestExcludes(ctx)
		if len(failed) > 0 {
			c.Event("warning", "Wiederherstellungstest", "Behaltene Test-Gäste nicht alle gelöscht", strings.Join(failed, "\n"))
			return
		}
		c.Event("info", "Wiederherstellungstest", "Behaltene Test-Gäste gelöscht", strings.Join(done, ", ")+" (von "+by+")")
	}()
	return nil
}

var nameBadRe = regexp.MustCompile(`[^A-Za-z0-9-]+`)

// dnsName makes a valid host name of at most 50 characters (empty when nothing is left).
func dnsName(s string) string {
	n := strings.Trim(nameBadRe.ReplaceAllString(s, "-"), "-")
	if len(n) > 50 {
		n = strings.TrimRight(n[:50], "-")
	}
	return n
}

// keptName is the name of a kept VM in Proxmox VE: "test-dc01".
func keptName(orig, id string) string {
	if n := dnsName(orig); n != "" {
		return "test-" + n
	}
	return "test-" + id
}

// keepTestGuest turns a tested guest into a kept copy: own tag, name, description; shut down
// unless the plan keeps it running. False: it could not be marked and is removed as usual.
func (c *Core) keepTestGuest(ctx context.Context, plan RestoreTestPlan, pve *prox.PVE, typ string, testID int,
	snap prox.Snapshot, run *RestoreTestRun, idx int) bool {
	var res RestoreTestResult
	c.rtUpdate(run, func() { res = run.Results[idx] })
	set := func(fn func(r *RestoreTestResult)) { c.rtUpdate(run, func() { fn(&run.Results[idx]) }) }
	net := "getrennt"
	if plan.Network == "bridge" {
		net = plan.netText()
	}
	name := keptName(res.Name, snap.ID)
	if typ == "lxc" {
		// a container keeps its own host name (services may depend on it)
		if n := dnsName(res.Name); n != "" {
			name = n
		}
	}
	params := prox.Params{"tags": rtKeptTag, "onboot": false,
		"description": fmt.Sprintf("PK95 ProxBackup – Testsystem: Kopie von %s aus der Sicherung vom %s, wiederhergestellt am %s (Restore-Test-Plan „%s“, Netzwerk %s). Wird beim nächsten Lauf durch eine frische Kopie ersetzt.",
			strings.TrimSpace(snap.ID+" "+res.Name), fmtWhen(snap.Time), fmtWhen(time.Now().Unix()), plan.Name, net)}
	if typ == "lxc" {
		params["hostname"] = name
	} else {
		params["name"] = name
	}
	if err := pve.SetGuestConfig(ctx, plan.Node, typ, testID, params); err != nil {
		set(func(r *RestoreTestResult) {
			r.Info += " – nicht als Testsystem markierbar, wird gelöscht: " + err.Error()
		})
		return false
	}
	running := false
	if st, err := pve.GuestStatus(ctx, plan.Node, typ, testID); err == nil && st.Status == "running" {
		running = plan.KeepRunning
		if !running {
			if upid, err := pve.GuestAction(ctx, plan.Node, typ, testID, "shutdown", prox.Params{"timeout": 120, "forceStop": 1}); err == nil {
				_, _ = pve.WaitTask(ctx, upid, 4*time.Minute)
			}
		}
	}
	k := KeptGuest{Cluster: plan.Cluster, Node: plan.Node, Type: typ, VMID: testID, Name: name, Orig: snap.Type + "/" + snap.ID,
		OrigName: res.Name, Backup: snap.Time, Restored: time.Now().Unix(), Status: res.Status, Running: running, Network: net}
	c.updateKept(plan.ID, func(m map[string]KeptGuest) { m[k.Orig] = k })
	state := "ausgeschaltet"
	if running {
		state = "läuft"
	}
	set(func(r *RestoreTestResult) {
		r.Info += fmt.Sprintf(" – bleibt als %d %s (%s, Netzwerk %s)", testID, name, state, net)
	})
	return true
}

// ---- kept test guests out of the backup jobs ----

// Backup jobs that save all guests would save the kept copies every night too. The appliance
// adds their IDs to the exclude list of those jobs and remembers which IDs it added, so the
// excludes an administrator set stay as they are and its own come out again when the copies
// go. Setting "rtexclude": cluster → job → IDs the appliance added.

var excludeMu sync.Mutex

func (c *Core) testExcludes() map[string]map[string][]string {
	var m map[string]map[string][]string
	_ = c.St.GetSetting("rtexclude", &m)
	if m == nil {
		m = map[string]map[string][]string{}
	}
	return m
}

// TestExcludesOf returns, per job of a Proxmox VE, the IDs the appliance excluded.
func (c *Core) TestExcludesOf(cluster string) map[string][]string {
	excludeMu.Lock()
	defer excludeMu.Unlock()
	return c.testExcludes()[cluster]
}

func idList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' })
}

// SyncTestExcludes brings the exclude lists of the all-guest backup jobs in line with the kept
// test guests.
func (c *Core) SyncTestExcludes(ctx context.Context) {
	excludeMu.Lock()
	defer excludeMu.Unlock()
	want := map[string][]string{} // cluster → IDs, sorted
	for _, list := range c.KeptGuests() {
		for _, k := range list {
			want[k.Cluster] = append(want[k.Cluster], strconv.Itoa(k.VMID))
		}
	}
	rec := c.testExcludes()
	clusters := map[string]bool{}
	for cl := range want {
		clusters[cl] = true
		sort.Slice(want[cl], func(i, j int) bool {
			a, _ := strconv.Atoi(want[cl][i])
			b, _ := strconv.Atoi(want[cl][j])
			return a < b
		})
	}
	for cl := range rec {
		clusters[cl] = true
	}
	changed := false
	for cl := range clusters {
		pve, _, err := c.PVE(cl)
		if err != nil {
			continue
		}
		jobs, err := pve.BackupJobs(ctx)
		if err != nil {
			continue // try again later; the record stays
		}
		next := map[string][]string{}
		for _, j := range jobs {
			if j.All == 0 {
				continue
			}
			ours := map[string]bool{}
			for _, id := range rec[cl][j.ID] {
				ours[id] = true
			}
			cur := idList(j.Exclude)
			var user []string
			for _, id := range cur {
				if !ours[id] {
					user = append(user, id)
				}
			}
			var add []string
			for _, id := range want[cl] {
				if !slices.Contains(user, id) {
					add = append(add, id)
				}
			}
			list := append(append([]string{}, user...), add...)
			if strings.Join(list, ",") != strings.Join(cur, ",") {
				p := prox.Params{"exclude": strings.Join(list, ",")}
				if len(list) == 0 {
					p = prox.Params{"delete": "exclude"}
				}
				if err := pve.UpdateBackupJob(ctx, j.ID, p); err != nil {
					if old := rec[cl][j.ID]; len(old) > 0 {
						next[j.ID] = old
					}
					c.Event("warning", "Wiederherstellungstest", "Test-Gäste nicht aus Sicherungsjob "+j.ID+" ausgenommen", err.Error())
					continue
				}
			}
			if len(add) > 0 {
				next[j.ID] = add
			}
		}
		if len(next) == 0 {
			delete(rec, cl)
		} else {
			rec[cl] = next
		}
		changed = true
	}
	if changed {
		_ = c.St.SetSetting("rtexclude", rec)
	}
}
