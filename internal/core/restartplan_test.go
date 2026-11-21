package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
)

func eventBody(t *testing.T, c *core.Core, title string) string {
	t.Helper()
	evs, err := c.St.Events(context.Background(), "", 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Title == title {
			return e.Body
		}
	}
	return ""
}

// A restart the appliance triggered: no "not reachable", one message when the server is back,
// an alarm when it stays away longer than 15 minutes, a warning when it never went down.
func TestPlannedRestart(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	srv, _ := c.Server("buero")
	state := func(online bool, uptime int64) *core.Overview {
		return &core.Overview{Servers: []core.ServerState{{PBSServer: srv, Online: online, Uptime: uptime, Error: map[bool]string{false: "connection refused"}[online]}}}
	}
	if _, err := c.RebootServer(ctx, "buero", "admin", true); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if r, ok := c.PlannedRestartsForTest()["buero"]; !ok || r.By != "admin" {
		t.Fatalf("not planned: %v", c.PlannedRestartsForTest())
	}
	// Away: the reachability check stays quiet.
	c.CheckForTest(ctx, state(true, 864000), state(false, 0))
	c.FollowRestartsForTest(state(false, 0), start.Add(time.Minute))
	if titles := eventTitles(t, c); strings.Contains(titles, "nicht erreichbar") {
		t.Fatalf("planned restart reported as outage:\n%s", titles)
	}
	// Back after two minutes.
	c.CheckForTest(ctx, state(false, 0), state(true, 60))
	c.FollowRestartsForTest(state(true, 60), start.Add(2*time.Minute))
	if b := eventBody(t, c, "Backup-Server neu gestartet"); !strings.Contains(b, "nach etwa 2 Minuten wieder erreichbar") || strings.Contains(eventTitles(t, c), "wieder erreichbar\n") {
		t.Fatalf("back: %q\n%s", b, eventTitles(t, c))
	}
	if len(c.PlannedRestartsForTest()) != 0 {
		t.Fatal("record left")
	}

	// Does not come back in time: alarm, later the normal "reachable again".
	if _, err := c.RebootServer(ctx, "buero", "admin", true); err != nil {
		t.Fatal(err)
	}
	c.FollowRestartsForTest(state(false, 0), time.Now().Add(16*time.Minute))
	if !strings.Contains(eventTitles(t, c), "Backup-Server nach Neustart nicht wieder erreichbar") {
		t.Fatalf("no alarm:\n%s", eventTitles(t, c))
	}
	c.FollowRestartsForTest(state(true, 30), time.Now().Add(20*time.Minute))
	if !strings.Contains(eventTitles(t, c), "Backup-Server wieder erreichbar") {
		t.Fatalf("not back:\n%s", eventTitles(t, c))
	}

	// Never went down (uptime goes on): the restart did not happen.
	if _, err := c.RebootServer(ctx, "buero", "admin", true); err != nil {
		t.Fatal(err)
	}
	c.FollowRestartsForTest(state(true, 864000), time.Now().Add(5*time.Minute)) // still waiting
	if strings.Contains(eventTitles(t, c), "Neustart hat nicht stattgefunden") {
		t.Fatal("too early")
	}
	c.FollowRestartsForTest(state(true, 864000), time.Now().Add(16*time.Minute))
	if !strings.Contains(eventTitles(t, c), "Neustart hat nicht stattgefunden") {
		t.Fatalf("not noticed:\n%s", eventTitles(t, c))
	}

	// Without a plan, an outage is reported as before.
	c.CheckForTest(ctx, state(true, 864000), state(false, 0))
	if !strings.Contains(eventTitles(t, c), "Backup-Server nicht erreichbar") {
		t.Fatalf("outage not reported:\n%s", eventTitles(t, c))
	}
}
