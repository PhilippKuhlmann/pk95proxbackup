package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/fake"
)

// At the planned time the appliance installs a new version of itself – after the work a
// restart would interrupt has ended, or not at all that day when it runs past the window.
func TestSelfAutoUpdate(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	gh := fake.NewGitHub("1.5.0")
	defer gh.Close()
	oldAPI, oldWeb := core.GitHubAPI, core.GitHubWeb
	core.GitHubAPI, core.GitHubWeb = gh.URL, gh.URL
	defer func() { core.GitHubAPI, core.GitHubWeb = oldAPI, oldWeb }()
	c.Version = "1.4.0"
	installed := make(chan string, 4)
	c.InstallBinary = func(_ context.Context, _ []byte, version string) error {
		installed <- version
		return nil
	}
	if errs, err := c.SetSelfAutoUpdate(core.AutoUpdate{Enabled: true, Time: "5:00"}); err != nil || errs["time"] == "" || errs["weekdays"] == "" {
		t.Fatalf("invalid plan accepted: %v %v", errs, err)
	}
	if errs, err := c.SetSelfAutoUpdate(core.AutoUpdate{Enabled: true, Weekdays: []int{1, 2, 3, 4, 5, 6, 7}, Time: "05:00"}); err != nil || errs != nil {
		t.Fatalf("plan: %v %v", errs, err)
	}
	day := time.Date(2026, 11, 20, 0, 0, 0, 0, time.Local)
	at := func(d int, hh, mm int) time.Time {
		return day.AddDate(0, 0, d).Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute)
	}
	none := func(when string) {
		t.Helper()
		select {
		case v := <-installed:
			t.Fatalf("%s: installed %s", when, v)
		case <-time.After(300 * time.Millisecond):
		}
	}

	c.RunSelfAutoUpdate(ctx, at(0, 4, 59))
	none("before the planned time")
	if st := c.SelfAutoUpdateState(at(0, 4, 59)); st.Next != at(0, 5, 0).Unix() {
		t.Fatalf("next planned time: %+v", st)
	}

	// A system update runs: the appliance waits.
	c.SetUpgradingForTest("local", true)
	c.RunSelfAutoUpdate(ctx, at(0, 5, 0))
	none("while a system update runs")
	if st := c.SelfAutoUpdateState(at(0, 5, 0)); len(st.Waiting) != 1 || !strings.Contains(st.Waiting[0], "Systemupdate auf") {
		t.Fatalf("waiting for: %+v", st)
	}
	// It ends: the update follows within the window, once.
	c.SetUpgradingForTest("local", false)
	c.RunSelfAutoUpdate(ctx, at(0, 5, 40))
	select {
	case v := <-installed:
		if v != "1.5.0" {
			t.Fatalf("installed %s", v)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("not installed: %+v", c.Update())
	}
	if !strings.Contains(eventTitles(t, c), "Update auf 1.5.0 wird automatisch installiert") {
		t.Fatalf("not announced:\n%s", eventTitles(t, c))
	}
	c.RunSelfAutoUpdate(ctx, at(0, 5, 50))
	none("a second time the same day")

	// Next day the work runs past the window: postponed, with a message.
	c.SetUpgradingForTest("local", true)
	c.RunSelfAutoUpdate(ctx, at(1, 5, 0))
	c.RunSelfAutoUpdate(ctx, at(1, 8, 1))
	none("after the window")
	if !strings.Contains(eventTitles(t, c), "Update der Appliance verschoben") || !strings.Contains(c.SelfAutoUpdateState(at(1, 8, 1)).Postponed, "Systemupdate auf") {
		t.Fatalf("postponement not reported:\n%s", eventTitles(t, c))
	}
}
