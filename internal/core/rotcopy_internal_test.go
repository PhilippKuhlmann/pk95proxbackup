package core

import (
	"path/filepath"
	"testing"
	"time"

	"pk95proxbackup/internal/store"
)

// The daily copy starts once at its time; a disk plugged in later waits for the next day.
func TestCopyDue(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c := &Core{St: st}
	r := Rotation{ID: "r1", Time: "14:00"}
	at := func(day int, hhmm string) time.Time {
		tm, _ := time.ParseInLocation("15:04", hhmm, time.Local)
		return time.Date(2026, 11, day, tm.Hour(), tm.Minute(), 0, 0, time.Local)
	}
	if c.copyDue(r, at(18, "13:59")) {
		t.Fatal("due before its time")
	}
	if !c.copyDue(r, at(18, "14:00")) {
		t.Fatal("not due at its time")
	}
	if c.copyDue(r, at(18, "14:01")) || c.copyDue(r, at(18, "18:34")) {
		t.Fatal("a disk plugged in after the copy time must not get a second copy that day")
	}
	// The appliance was down at 14:00 and comes back at 18:00: nothing is caught up.
	if c.copyDue(r, at(19, "18:00")) {
		t.Fatal("a missed copy time is not caught up")
	}
	if c.copyDue(r, at(19, "18:30")) {
		t.Fatal("still not due the same day")
	}
	// A late monitor tick within the grace still counts as on time.
	if !c.copyDue(r, at(20, "14:10")) {
		t.Fatal("a tick 10 minutes late should still start the copy")
	}
	if c.copyDue(Rotation{ID: "r2"}, at(20, "14:00")) {
		t.Fatal("no time, no scheduled copy")
	}
}
