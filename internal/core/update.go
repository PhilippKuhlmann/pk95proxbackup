package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Updates: the appliance looks for new releases on GitHub and installs one on request. The
// new program is checked (SHA256SUMS of the release, a test run that must print the expected
// version) before it replaces the running one. If it then fails to start three times, the
// start guard (UpdateGuard) puts the previous program back.

// Release source (tests and the demo point them to a local server).
var (
	UpdateRepo = "PhilippKuhlmann/pk95proxbackup"
	GitHubAPI  = "https://api.github.com"
	GitHubWeb  = "https://github.com"
)

const updateCheckEvery = 12 * time.Hour

// UpdateInfo is what the settings page shows about updates.
type UpdateInfo struct {
	Current    string `json:"current"`
	Latest     string `json:"latest"`
	Tag        string `json:"tag"`
	Available  bool   `json:"available"`
	Notes      string `json:"notes"`
	URL        string `json:"url"`
	Published  int64  `json:"published"`
	Checked    int64  `json:"checked"`
	Error      string `json:"error,omitempty"`
	AutoCheck  bool   `json:"auto_check"`
	Phase      string `json:"phase,omitempty"` // downloading, verifying, installing, restarting, failed
	PhaseError string `json:"phase_error,omitempty"`
	CanInstall bool   `json:"can_install"`
	// Busy is work the appliance follows right now that a restart would interrupt.
	Busy   []string `json:"busy"`
	Reason string   `json:"reason,omitempty"` // why it cannot be installed from here
}

type updateRuntime struct {
	mu       sync.Mutex
	busy     bool
	checking bool
	phase    string
	err      string
}

type updateState struct {
	Latest    string `json:"latest"`
	Tag       string `json:"tag"`
	Notes     string `json:"notes"`
	URL       string `json:"url"`
	Published int64  `json:"published"`
	Checked   int64  `json:"checked"`
	Error     string `json:"error,omitempty"`
	Notified  string `json:"notified,omitempty"`
}

type updateSettings struct {
	AutoCheck bool `json:"auto_check"`
}

func (c *Core) updateSettings() updateSettings {
	s := updateSettings{AutoCheck: true}
	_ = c.St.GetSetting("update-settings", &s)
	return s
}

// SetUpdateAutoCheck turns the regular look for new versions on or off.
func (c *Core) SetUpdateAutoCheck(on bool) error {
	return c.St.SetSetting("update-settings", updateSettings{AutoCheck: on})
}

// Update returns the known update state.
func (c *Core) Update() UpdateInfo {
	var st updateState
	_ = c.St.GetSetting("update-state", &st)
	info := UpdateInfo{Current: c.Version, Latest: st.Latest, Tag: st.Tag, Notes: st.Notes, URL: st.URL, Published: st.Published,
		Checked: st.Checked, Error: st.Error, AutoCheck: c.updateSettings().AutoCheck}
	info.Available = st.Latest != "" && NewerVersion(st.Latest, c.Version)
	info.CanInstall, info.Reason = c.canSelfUpdate()
	info.Busy = c.BusyWork()
	c.update.mu.Lock()
	info.Phase, info.PhaseError = c.update.phase, c.update.err
	c.update.mu.Unlock()
	return info
}

func (c *Core) canSelfUpdate() (bool, string) {
	if c.InstallBinary != nil {
		return true, ""
	}
	if runtime.GOOS != "linux" {
		return false, "Nur auf der Appliance (Linux) möglich."
	}
	exe, err := selfPath()
	if err != nil {
		return false, "Programmdatei nicht gefunden."
	}
	f, err := os.CreateTemp(filepath.Dir(exe), ".pk95-update-*")
	if err != nil {
		return false, "Keine Schreibrechte im Ordner " + filepath.Dir(exe) + "."
	}
	f.Close()
	os.Remove(f.Name())
	return true, ""
}

func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// NewerVersion reports whether version a ("1.5.0") is newer than b. Unparsable versions
// (a development build) are never older.
func NewerVersion(a, b string) bool {
	pa, okA := parseVersion(a)
	pb, okB := parseVersion(b)
	if !okA || !okB {
		return false
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func (c *Core) scheduleUpdateCheck(now time.Time) {
	if !c.updateSettings().AutoCheck {
		return
	}
	var st updateState
	_ = c.St.GetSetting("update-state", &st)
	if now.Unix()-st.Checked < int64(updateCheckEvery.Seconds()) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = c.CheckUpdate(ctx)
	}()
}

func ghRequest(ctx context.Context, url, version string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// No data about the appliance or its owner goes out, only the program version.
	req.Header.Set("User-Agent", "pk95proxbackup/"+version)
	req.Header.Set("Accept", "application/vnd.github+json")
	return (&http.Client{Timeout: 5 * time.Minute}).Do(req)
}

// CheckUpdate asks GitHub for the newest release.
func (c *Core) CheckUpdate(ctx context.Context) (UpdateInfo, error) {
	c.update.mu.Lock()
	if c.update.checking {
		c.update.mu.Unlock()
		return c.Update(), nil
	}
	c.update.checking = true
	c.update.mu.Unlock()
	defer func() {
		c.update.mu.Lock()
		c.update.checking = false
		c.update.mu.Unlock()
	}()
	var st updateState
	_ = c.St.GetSetting("update-state", &st)
	st.Checked = time.Now().Unix()
	err := c.fetchRelease(ctx, &st)
	st.Error = errText(err)
	if err == nil && NewerVersion(st.Latest, c.Version) && st.Notified != st.Latest {
		st.Notified = st.Latest
		c.Event("info", "Appliance", "Version "+st.Latest+" verfügbar",
			"Installiert ist "+c.Version+". Die neue Version lässt sich unter Einstellungen → Aktualisierung mit einem Klick installieren.")
	}
	_ = c.St.SetSetting("update-state", st)
	return c.Update(), err
}

func (c *Core) fetchRelease(ctx context.Context, st *updateState) error {
	resp, err := ghRequest(ctx, GitHubAPI+"/repos/"+UpdateRepo+"/releases/latest", c.Version)
	if err != nil {
		return fmt.Errorf("GitHub nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errors.New("noch kein Release veröffentlicht")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub antwortet mit HTTP %d", resp.StatusCode)
	}
	var rel struct {
		Tag       string    `json:"tag_name"`
		Body      string    `json:"body"`
		URL       string    `json:"html_url"`
		Published time.Time `json:"published_at"`
		Draft     bool      `json:"draft"`
		Pre       bool      `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return errors.New("Antwort von GitHub nicht lesbar")
	}
	if _, ok := parseVersion(rel.Tag); !ok || rel.Draft || rel.Pre {
		return fmt.Errorf("ungewöhnliches Release %q", rel.Tag)
	}
	st.Tag, st.Latest = rel.Tag, strings.TrimPrefix(rel.Tag, "v")
	st.Notes, st.URL, st.Published = truncateText(strings.TrimSpace(rel.Body), 4000), rel.URL, rel.Published.Unix()
	return nil
}

func truncateText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func (c *Core) setUpdatePhase(phase, errMsg string) {
	c.update.mu.Lock()
	c.update.phase, c.update.err = phase, errMsg
	c.update.mu.Unlock()
}

// InstallUpdate downloads, checks and installs the newest release in the background and
// restarts the appliance. Progress shows in Update().Phase.
func (c *Core) InstallUpdate() error {
	info := c.Update()
	if !info.Available {
		return errors.New("Es ist keine neuere Version bekannt. Bitte zuerst nach Updates suchen.")
	}
	if !info.CanInstall {
		return errors.New(info.Reason)
	}
	c.update.mu.Lock()
	if c.update.busy {
		c.update.mu.Unlock()
		return errors.New("Das Update läuft bereits.")
	}
	c.update.busy, c.update.phase, c.update.err = true, "downloading", ""
	c.update.mu.Unlock()
	go func() {
		defer func() {
			c.update.mu.Lock()
			c.update.busy = false
			c.update.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		if err := c.installUpdate(ctx, info); err != nil {
			c.setUpdatePhase("failed", err.Error())
			c.Event("warning", "Appliance", "Update auf "+info.Latest+" nicht installiert", err.Error()+" Die bisherige Version "+c.Version+" läuft weiter.")
		}
	}()
	return nil
}

func (c *Core) installUpdate(ctx context.Context, info UpdateInfo) error {
	asset := "pk95proxbackup-linux-" + runtime.GOARCH
	base := GitHubWeb + "/" + UpdateRepo + "/releases/download/" + info.Tag + "/"
	sums, err := c.download(ctx, base+"SHA256SUMS", 1<<20)
	if err != nil {
		return fmt.Errorf("Prüfsummen laden: %w.", err)
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			want = strings.ToLower(f[0])
		}
	}
	if want == "" {
		return fmt.Errorf("Das Release enthält kein Programm für %s.", runtime.GOARCH)
	}
	bin, err := c.download(ctx, base+asset, 256<<20)
	if err != nil {
		return fmt.Errorf("Programm laden: %w.", err)
	}
	c.setUpdatePhase("verifying", "")
	sum := sha256.Sum256(bin)
	if hex.EncodeToString(sum[:]) != want {
		return errors.New("Die Prüfsumme des geladenen Programms stimmt nicht. Es wurde nichts verändert.")
	}
	c.setUpdatePhase("installing", "")
	if c.InstallBinary != nil {
		if err := c.InstallBinary(ctx, bin, info.Latest); err != nil {
			return err
		}
	} else if err := c.swapBinary(ctx, bin, info); err != nil {
		return err
	}
	if c.InstallBinary != nil {
		// Simulated (demo): the version changed in place, no restart.
		c.setUpdatePhase("", "")
		c.Event("info", "Appliance", "Auf Version "+info.Latest+" aktualisiert", "Demo: das Update wurde nur simuliert.")
		return nil
	}
	c.setUpdatePhase("restarting", "")
	c.Event("info", "Appliance", "Update auf "+info.Latest+" wird installiert", "Die Appliance startet neu. Sicherungen, die Proxmox gerade schreibt, laufen weiter.")
	if c.Restart != nil {
		time.AfterFunc(time.Second, c.Restart)
	}
	return nil
}

func (c *Core) download(ctx context.Context, url string, max int64) ([]byte, error) {
	resp, err := ghRequest(ctx, url, c.Version)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errors.New("Datei zu groß")
	}
	return b, nil
}

// updateMarker is written before the restart into the new version.
type updateMarker struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Exe      string `json:"exe"`
	At       int64  `json:"at"`
	Attempts int    `json:"attempts"`
}

func markerPath(dataDir string) string     { return filepath.Join(dataDir, "update-pending.json") }
func rolledBackPath(dataDir string) string { return filepath.Join(dataDir, "update-rolledback.json") }

func (c *Core) swapBinary(ctx context.Context, bin []byte, info UpdateInfo) error {
	exe, err := selfPath()
	if err != nil {
		return err
	}
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return fmt.Errorf("Programm schreiben: %w", err)
	}
	// Test run: the new program must start and name the expected version.
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	out, err := exec.CommandContext(tctx, tmp, "-version").Output()
	cancel()
	if err != nil || strings.TrimSpace(string(out)) != info.Latest {
		os.Remove(tmp)
		return fmt.Errorf("Das neue Programm startet nicht richtig (Probelauf: %q). Es wurde nichts verändert.", strings.TrimSpace(string(out)))
	}
	// Keep a copy of the database as it was before the update.
	if old, _ := filepath.Glob(filepath.Join(c.DataDir, "before-update-*.db")); len(old) > 0 {
		for _, f := range old {
			os.Remove(f)
		}
	}
	if err := c.St.Backup(filepath.Join(c.DataDir, "before-update-"+c.Version+".db")); err != nil {
		log.Printf("update: db copy: %v", err)
	}
	m := updateMarker{From: c.Version, To: info.Latest, Exe: exe, At: time.Now().Unix()}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(markerPath(c.DataDir), b, 0o600); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(exe, exe+".old"); err != nil {
		os.Remove(tmp)
		os.Remove(markerPath(c.DataDir))
		return fmt.Errorf("bisheriges Programm sichern: %w", err)
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Rename(exe+".old", exe)
		os.Remove(markerPath(c.DataDir))
		return fmt.Errorf("neues Programm einsetzen: %w", err)
	}
	return nil
}

// UpdateGuard runs first thing at program start. While an installed update has not proven
// itself, every start counts; after three failed starts the previous program is put back
// and started instead.
func UpdateGuard(dataDir, version string) {
	b, err := os.ReadFile(markerPath(dataDir))
	if err != nil {
		return
	}
	var m updateMarker
	if json.Unmarshal(b, &m) != nil || m.To != version {
		os.Remove(markerPath(dataDir))
		return
	}
	m.Attempts++
	if m.Attempts <= 3 {
		b, _ = json.Marshal(m)
		_ = os.WriteFile(markerPath(dataDir), b, 0o600)
		return
	}
	if _, err := os.Stat(m.Exe + ".old"); err != nil {
		os.Remove(markerPath(dataDir))
		return
	}
	log.Printf("Update auf %s startet nicht – zurück zu %s", m.To, m.From)
	_ = os.Rename(m.Exe, m.Exe+".failed")
	if err := os.Rename(m.Exe+".old", m.Exe); err != nil {
		log.Printf("Zurückschalten fehlgeschlagen: %v", err)
		return
	}
	rb, _ := json.Marshal(m)
	_ = os.WriteFile(rolledBackPath(dataDir), rb, 0o600)
	os.Remove(markerPath(dataDir))
	_ = execProgram(m.Exe, os.Args, os.Environ())
}

// execProgram replaces the running program (tests replace it).
var execProgram = syscall.Exec

// afterStart reports a finished or rolled back update and a restored configuration once the
// program runs.
func (c *Core) afterStart(ctx context.Context) {
	c.resumeRotations(ctx)
	if b, err := os.ReadFile(rolledBackPath(c.DataDir)); err == nil {
		var m updateMarker
		_ = json.Unmarshal(b, &m)
		os.Remove(rolledBackPath(c.DataDir))
		c.Event("critical", "Appliance", "Update auf "+m.To+" zurückgenommen",
			"Die neue Version startete dreimal nicht. Die Appliance läuft wieder mit "+m.From+". Bitte das Protokoll (journalctl -u pk95proxbackup) prüfen.")
	}
	if b, err := os.ReadFile(restoreDonePath(c.DataDir)); err == nil {
		var m ConfigManifest
		_ = json.Unmarshal(b, &m)
		os.Remove(restoreDonePath(c.DataDir))
		c.Event("info", "Appliance", "Konfiguration wiederhergestellt",
			"Die Einstellungen von "+m.Host+" mit Stand "+fmtTime(m.Created)+" sind eingespielt.")
	}
	if _, err := os.Stat(markerPath(c.DataDir)); err != nil {
		return
	}
	// Stable for a minute: the update has proven itself.
	select {
	case <-ctx.Done():
		return
	case <-time.After(60 * time.Second):
	}
	b, err := os.ReadFile(markerPath(c.DataDir))
	if err != nil {
		return
	}
	var m updateMarker
	_ = json.Unmarshal(b, &m)
	os.Remove(markerPath(c.DataDir))
	c.Event("info", "Appliance", "Auf Version "+m.To+" aktualisiert", "Vorher lief "+m.From+". Die alte Programmdatei liegt als "+filepath.Base(m.Exe)+".old daneben.")
}

var phaseWords = map[string]string{"pruning": "räumt auf", "gc": "gibt Speicher frei", "unmounting": "wird ausgehängt", "mounting": "wird eingehängt",
	"rebuilding": "wird neu aufgesetzt", "verifying": "wird zurückgelesen (Gegentest)"}

// BusyWork lists what the appliance is doing right now that a restart would interrupt.
func (c *Core) BusyWork() []string {
	out := []string{}
	if rots, err := c.Rotations(); err == nil {
		for _, r := range rots {
			for _, d := range r.Disks {
				rt := c.runtime(rtKey(r, d))
				c.mu.Lock()
				phase := rt.Phase
				c.mu.Unlock()
				if phase != "" {
					w := phaseWords[phase]
					if w == "" {
						w = phase
					}
					out = append(out, "Wechselplatte „"+d.Label+"“ "+w)
				}
			}
		}
	}
	for _, a := range c.busyArchives() {
		out = append(out, "Archiv-Platte „"+a.Name+"“ ("+archivePhaseLabel[a.Phase]+")")
	}
	if run := c.ActiveRestoreTest(); run != nil {
		out = append(out, "Restore-Test „"+run.PlanName+"“")
	}
	if c.SelfBackup().Running {
		out = append(out, "Sicherung der Appliance-Konfiguration")
	}
	return out
}
