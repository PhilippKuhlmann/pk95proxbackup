package core_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/fake"
	"pk95proxbackup/internal/hostexec"
	"pk95proxbackup/internal/prox"
)

func hostView(t *testing.T, c *core.Core) core.HostBackupView {
	t.Helper()
	vs := c.HostBackups(context.Background())
	if len(vs) != 1 || vs[0].Error != "" {
		t.Fatalf("views: %+v", vs)
	}
	return vs[0]
}

func hostNode(v core.HostBackupView, n string) core.HostNodeView {
	for _, x := range v.Nodes {
		if x.Node == n {
			return x
		}
	}
	return core.HostNodeView{}
}

// The nodes of the demo cluster back up /etc, /root and the system information into the
// datastore of the appliance with the login of the PVE storage.
func TestHostBackup(t *testing.T) {
	old := fake.HostBackupDuration
	fake.HostBackupDuration = 0
	t.Cleanup(func() { fake.HostBackupDuration = old })
	c := newCore(t)
	ctx := context.Background()

	v := hostView(t, c)
	if !v.Conf.Enabled || v.Target == nil || v.Target.Storage != "pbs-appliance" || v.Target.ServerID != "local" || v.Target.Datastore != "backup" || v.Next == 0 {
		t.Fatalf("view: %+v target %+v", v, v.Target)
	}
	p1 := hostNode(v, "pve1")
	if p1.Detected != "192.168.10.11" || p1.Addr != "192.168.10.11" || !p1.Online || len(p1.Snapshots) < 11 || p1.State.LastOK == 0 || !p1.HasKey || p1.State.HostKey != "" {
		t.Fatalf("pve1: %+v", p1)
	}
	before := len(p1.Snapshots)

	if err := c.RunHostBackup(ctx, "cluster", "pve1"); err != nil {
		t.Fatal(err)
	}
	p1 = hostNode(hostView(t, c), "pve1")
	if len(p1.Snapshots) != before+1 || time.Since(time.Unix(p1.State.LastOK, 0)) > time.Minute || p1.State.LastError != "" {
		t.Fatalf("after run: %d snapshots, %+v", len(p1.Snapshots), p1.State)
	}

	// Retention: only the newest five stay.
	conf := v.Conf
	conf.Keep = 5
	if errs, err := c.SetHostBackup(ctx, "cluster", conf); err != nil || errs != nil {
		t.Fatal(errs, err)
	}
	if err := c.RunHostBackup(ctx, "cluster", ""); err != nil {
		t.Fatal(err)
	}
	v = hostView(t, c)
	if n1, n2 := len(hostNode(v, "pve1").Snapshots), len(hostNode(v, "pve2").Snapshots); n1 != 5 || n2 != 5 {
		t.Fatalf("kept %d / %d", n1, n2)
	}

	// A failing node: one message, then the recovery.
	bad := &fake.NodeShell{PBS: fake.Current.Local, Store: "backup", Node: "pve2", Fail: "Kennwort des Speichers pbs-appliance fehlt (/etc/pve/priv/storage/pbs-appliance.pw)"}
	good := c.NodeRunner
	c.NodeRunner = func(cl core.PVECluster, node string) hostexec.Runner {
		if node == "pve2" {
			return bad
		}
		return good(cl, node)
	}
	for range 2 {
		if err := c.RunHostBackup(ctx, "cluster", "pve2"); err == nil || !strings.Contains(err.Error(), "Kennwort des Speichers") {
			t.Fatalf("failure: %v", err)
		}
	}
	if st := hostNode(hostView(t, c), "pve2").State; !strings.HasPrefix(st.LastError, "Kennwort") || st.Tries != 1 { // a manual run starts the count anew
		t.Fatalf("state: %+v", st)
	}
	if n := strings.Count(eventTitles(t, c), "Host-Sicherung von pve2 fehlgeschlagen"); n != 1 {
		t.Fatalf("%d messages:\n%s", n, eventTitles(t, c))
	}
	if got := core.EventCategory("Cluster Zuhause", "Host-Sicherung von pve2 fehlgeschlagen"); got != "backups" {
		t.Fatalf("category %s", got)
	}
	bad.SetFail("")
	if err := c.RunHostBackup(ctx, "cluster", "pve2"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(eventTitles(t, c), "Host-Sicherung von pve2 wieder in Ordnung") {
		t.Fatalf("no recovery:\n%s", eventTitles(t, c))
	}

	res, err := c.CheckHostAccess(ctx, "cluster", "pve1")
	if err != nil || !strings.HasPrefix(res.Version, "pve-manager/9") || len(res.Problems) != 0 {
		t.Fatalf("check: %+v %v", res, err)
	}
	if _, err := c.CheckHostAccess(ctx, "cluster", "pve9"); err == nil {
		t.Fatal("unknown node accepted")
	}
}

func TestHostBackupSettings(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	errs, err := c.SetHostBackup(ctx, "cluster", core.HostBackupConf{Enabled: true, Storage: "local", Time: "25:00", Keep: 0, User: "admin", Addrs: map[string]string{"pve1": "root@10.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"time", "keep", "user", "storage", "addr_pve1"} {
		if errs[k] == "" {
			t.Errorf("no error for %s: %v", k, errs)
		}
	}
	errs, err = c.SetHostBackup(ctx, "cluster", core.HostBackupConf{Enabled: true, Storage: "pbs-appliance", Time: "3:05", Keep: 14, Addrs: map[string]string{"pve2": " 10.0.0.12 ", "pve1": ""}})
	if err != nil || errs != nil {
		t.Fatal(errs, err)
	}
	v := hostView(t, c)
	if v.Conf.Time != "03:05" || v.Conf.User != "root" || v.Conf.Port != 22 || hostNode(v, "pve2").Addr != "10.0.0.12" || hostNode(v, "pve1").Addr != "192.168.10.11" {
		t.Fatalf("stored: %+v %+v", v.Conf, v.Nodes)
	}
}

// The daily run starts once per day (also late, after the appliance was off) and a failed
// node is tried again after an hour, three times at most.
func TestHostBackupSchedule(t *testing.T) {
	old := fake.HostBackupDuration
	fake.HostBackupDuration = 0
	t.Cleanup(func() { fake.HostBackupDuration = old })
	c := newCore(t)
	shells := map[string]*fake.NodeShell{}
	for _, n := range []string{"pve1", "pve2"} {
		shells[n] = &fake.NodeShell{PBS: fake.Current.Local, Store: "backup", Node: n}
	}
	shells["pve2"].Fail = "kaputt"
	c.NodeRunner = func(_ core.PVECluster, node string) hostexec.Runner { return shells[node] }
	wait := func(want1, want2 int) {
		t.Helper()
		for i := 0; i < 200; i++ {
			if shells["pve1"].Runs() == want1 && shells["pve2"].Runs() == want2 {
				time.Sleep(20 * time.Millisecond)
				if shells["pve1"].Runs() == want1 && shells["pve2"].Runs() == want2 {
					return
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("runs %d/%d, want %d/%d", shells["pve1"].Runs(), shells["pve2"].Runs(), want1, want2)
	}
	if err := c.St.SetSetting("hostbackup-sched", map[string]int64{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	c.ScheduleHostBackupsForTest(now) // missed run: at once
	wait(1, 1)
	c.ScheduleHostBackupsForTest(now.Add(10 * time.Minute)) // nothing new yet
	wait(1, 1)
	c.ScheduleHostBackupsForTest(now.Add(61 * time.Minute)) // retry of pve2 only
	wait(1, 2)
	c.ScheduleHostBackupsForTest(now.Add(122 * time.Minute))
	wait(1, 3)
	c.ScheduleHostBackupsForTest(now.Add(183 * time.Minute)) // three tries are enough
	wait(1, 3)
	c.ScheduleHostBackupsForTest(now.Add(25 * time.Hour)) // next day
	wait(2, 4)
}

// The script must be valid shell and use the login of the storage, never a secret of its own.
func TestHostBackupScript(t *testing.T) {
	st := prox.Storage{Storage: "pbs-1", Type: "pbs", Server: "fd00::5", Port: 8008, Datastore: "backup", Namespace: "kunde/a", Username: "pk95-pve@pbs!c1", Fingerprint: "aa:bb"}
	s := core.HostBackupScriptForTest(st, "pve1")
	for _, want := range []string{"PBS_REPOSITORY='pk95-pve@pbs!c1@[fd00::5]:8008:backup'", "STORE=pbs-1", "NS=kunde/a", "--backup-id \"$ID\"",
		"/etc/pve/priv/storage/$STORE.enc", "--include-dev /etc/pve", "etc.pxar:/etc", "LIESMICH.txt", "PK95-OK"} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(s, "PBS_PASSWORD=") && !strings.Contains(s, `PBS_PASSWORD="$(cat "$PW")"`) {
		t.Error("password not from the node")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	for _, sc := range []string{s, core.HostCheckScriptForTest("pbs-1")} {
		if out, err := exec.Command("bash", "-n", "-c", sc).CombinedOutput(); err != nil {
			t.Fatalf("bash -n: %v\n%s", err, out)
		}
	}
	if got := core.HostBackupScriptForTest(prox.Storage{Storage: "s", Server: "10.0.0.5", Datastore: "d", Username: "u@pbs"}, "n"); !strings.Contains(got, "PBS_REPOSITORY=u@pbs@10.0.0.5:d") {
		t.Errorf("plain repository: %s", got[:400])
	}
}

// The overview reports a node only after the retries are used up, or when its backup is old.
func TestHostBackupIssues(t *testing.T) {
	c := newCore(t)
	if is := c.HostBackupIssues(); len(is) != 0 {
		t.Fatalf("demo: %+v", is)
	}
	now := time.Now().Unix()
	set := func(s map[string]core.HostNodeState) {
		if err := c.St.SetSetting("hostbackup-state", map[string]map[string]core.HostNodeState{"cluster": s}); err != nil {
			t.Fatal(err)
		}
	}
	set(map[string]core.HostNodeState{"pve1": {LastOK: now - 3600, LastRun: now, LastError: "x", Tries: 1}, "pve2": {LastOK: now - 3*86400, LastRun: now - 3*86400}})
	is := c.HostBackupIssues()
	if len(is) != 1 || is[0].Node != "pve2" || is[0].Error != "" {
		t.Fatalf("issues: %+v", is)
	}
	set(map[string]core.HostNodeState{"pve1": {LastOK: now - 3600, LastRun: now, LastError: "x", Tries: 3}})
	if is := c.HostBackupIssues(); len(is) != 1 || is[0].Node != "pve1" || is[0].Error != "x" {
		t.Fatalf("issues: %+v", is)
	}
}
