package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
)

func readerTask(upid, status string, start, end time.Time) core.TaskInfo {
	return core.TaskInfo{Source: "pbs:local", Server: "PBS01", UPID: upid, Node: "localhost", Type: "reader",
		ID: "local:vm/101/6ABFFF8B", StartTime: start.Unix(), EndTime: end.Unix(), Status: status}
}

// A reader error that fixes itself (the client closed the connection, or the same backup was
// read fine afterwards) sends no error; one that stays is reported after the waiting time.
func TestReaderErrorsThatFixThemselves(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	if err := c.St.SetSetting("seen-failed", map[string]bool{"-": true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	check := func(tasks ...core.TaskInfo) {
		t.Helper()
		c.CheckFailedForTest(ctx, &core.Overview{Tasks: tasks})
		if titles := eventTitles(t, c); strings.Contains(titles, "Lesezugriff fehlgeschlagen") {
			t.Fatalf("reported:\n%s", titles)
		}
	}
	// the restore test closed the connection: never an error
	check(readerTask("UPID:gone", "connection error: not connected", now.Add(-3*time.Minute), now.Add(-time.Minute)))

	// another error waits; a later read of the same backup without error settles it
	failed := readerTask("UPID:fail", "unable to read chunk – timeout", now.Add(-3*time.Minute), now.Add(-2*time.Minute))
	check(failed)
	check(failed, readerTask("UPID:retry", "OK", now.Add(-time.Minute), now))
	check(failed)

	// an error nothing fixes: reported once the waiting time is over, even when the task has left
	// the list of recent tasks meanwhile
	stuck := readerTask("UPID:stuck", "unable to read chunk – timeout", now.Add(-3*time.Minute), now.Add(-time.Minute))
	stuck.ID = "local:vm/102/6AC00000"
	check(stuck)
	var pending map[string]core.TaskInfo
	if err := c.St.GetSetting("pending-readers", &pending); err != nil || len(pending) != 1 {
		t.Fatalf("held back: %v %v", pending, err)
	}
	stuck.EndTime = now.Add(-20 * time.Minute).Unix()
	if err := c.St.SetSetting("pending-readers", map[string]core.TaskInfo{stuck.UPID: stuck}); err != nil {
		t.Fatal(err)
	}
	c.CheckFailedForTest(ctx, &core.Overview{})
	if titles := eventTitles(t, c); strings.Count(titles, "Lesezugriff fehlgeschlagen") != 1 {
		t.Fatalf("lasting error not reported once:\n%s", titles)
	}
	c.CheckFailedForTest(ctx, &core.Overview{Tasks: []core.TaskInfo{stuck}})
	if titles := eventTitles(t, c); strings.Count(titles, "Lesezugriff fehlgeschlagen") != 1 {
		t.Fatalf("reported twice:\n%s", titles)
	}
}
