package core

import (
	"strings"
	"testing"
	"time"
)

func planned(key, group, store, hhmm string, days []int, secs int64) TimelineItem {
	return TimelineItem{Key: key, Group: group, Title: key, Stores: []string{store}, Enabled: true, Editable: group != "backup",
		Simple: &SimplePlan{Time: hhmm, Weekdays: days}, Duration: secs, Text: hhmm}
}

// Cleanup, garbage collection and verification move around the backups of their datastore; jobs
// that run fine stay where they are.
func TestSuggestTimeline(t *testing.T) {
	every := []int{1, 2, 3, 4, 5, 6, 7}
	loc := time.UTC
	from := time.Date(2026, 11, 22, 0, 0, 0, 0, loc)
	tl := Timeline{From: from.Unix(), To: from.AddDate(0, 0, 9).Unix(), Items: []TimelineItem{
		planned("nightly", "backup", "s/a", "21:00", every, 40*60),
		planned("prune-a", "prune", "s/a", "00:00", every, 40),        // fine
		planned("gc-a", "gc", "s/a", "00:00", every, 10*60),           // at the same time as the cleanup
		planned("verify-a", "verify", "s/a", "21:15", []int{6}, 1800), // during the backup
		planned("prune-b", "prune", "s/b", "02:00", every, 60),
		planned("gc-b", "gc", "s/b", "01:00", every, 5*60), // just before the cleanup
		planned("gc-c", "gc", "s/c", "21:00", every, 5*60), // other datastore: no clash with the backup
	}}
	s := suggestTimeline(tl, loc)
	got := map[string]string{}
	for _, c := range s.Changes {
		got[c.Key] = c.To
		if c.Reason == "" {
			t.Errorf("%s: no reason", c.Key)
		}
	}
	want := map[string]string{"gc-a": "00:30", "verify-a": "sat 01:00", "gc-b": "02:30"}
	if len(got) != len(want) {
		t.Fatalf("changes %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
	// the plan as it would look carries the new times
	for _, it := range s.After.Items {
		if it.Key == "gc-a" && (it.Text != "täglich 00:30" || len(it.Starts) == 0) {
			t.Fatalf("after: %+v", it)
		}
	}
	// a plan without problems stays as it is
	if again := suggestTimeline(s.After, loc); len(again.Changes) != 0 {
		t.Fatalf("the proposal is not stable: %+v", again.Changes)
	}
}

// With working hours the housekeeping goes outside of them, even when nothing else runs.
func TestSuggestTimelineWorkHours(t *testing.T) {
	loc := time.UTC
	from := time.Date(2026, 11, 22, 0, 0, 0, 0, loc)
	tl := Timeline{From: from.Unix(), To: from.AddDate(0, 0, 9).Unix(),
		Work: WorkHours{Enabled: true, Weekdays: []int{1, 2, 3, 4, 5}, From: "07:00", To: "18:00"},
		Items: []TimelineItem{
			planned("daily", "backup", "s/a", "12:00", []int{1, 2, 3, 4, 5, 6, 7}, 20*60),
			planned("prune-a", "prune", "s/a", "12:30", []int{1, 2, 3, 4, 5, 6, 7}, 60), // after the backup, but at work
			planned("verify-a", "verify", "s/a", "09:00", []int{6}, 3600),               // Saturday: no work then
		}}
	s := suggestTimeline(tl, loc)
	if len(s.Changes) != 1 || s.Changes[0].Key != "prune-a" || s.Changes[0].To != "18:00" {
		t.Fatalf("changes: %+v", s.Changes)
	}
	if !strings.Contains(s.Changes[0].Reason, "Arbeitszeit (Mo–Fr 07:00–18:00)") {
		t.Fatalf("reason: %s", s.Changes[0].Reason)
	}
}
