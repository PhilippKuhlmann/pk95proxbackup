package core

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// A proposed time plan: backups, copies, USB disks, restore tests, host backups and updates stay
// where they are. The housekeeping of each datastore moves around them in the order the Backup
// Server wants it – after the last backup the cleanup (prune), then the garbage collection
// that frees what the cleanup removed, the verification where the datastore is quiet. A job is
// moved only when it runs at the same time as something else on its datastore, or when the
// garbage collection runs just before the cleanup; its weekdays stay, times snap to 15 minutes.

// ScheduleChange is a proposed new time for a job.
type ScheduleChange struct {
	Key          string `json:"key"`
	Title        string `json:"title"`
	From         string `json:"from"` // readable
	FromSchedule string `json:"from_schedule"`
	To           string `json:"to"` // calendar event
	ToText       string `json:"to_text"`
	Reason       string `json:"reason"`
}

// TimelineSuggestion is the proposal with the time plan as it would look.
type TimelineSuggestion struct {
	Changes []ScheduleChange `json:"changes"`
	After   Timeline         `json:"after"`
	Notes   []string         `json:"notes"`
}

// SuggestTimeline proposes times for cleanup, garbage collection and verification.
func (c *Core) SuggestTimeline(ctx context.Context, now time.Time) TimelineSuggestion {
	return suggestTimeline(c.Timeline(ctx, now), now.Location())
}

const dayMin = 24 * 60

// weekBusy holds per datastore, weekday (0 = Monday) and minute what runs there.
type weekBusy map[string]*[7][dayMin]string

func (wb weekBusy) walk(store string, wd, start, dur int, f func(slot *string) bool) {
	b := wb[store]
	if b == nil {
		b = &[7][dayMin]string{}
		wb[store] = b
	}
	for m := 0; m < dur && m < 7*dayMin; m++ {
		t := start + m
		if !f(&b[(wd+t/dayMin)%7][t%dayMin]) {
			return
		}
	}
}

func (wb weekBusy) mark(store string, wd, start, dur int, title string) {
	wb.walk(store, wd, start, dur, func(s *string) bool {
		if *s == "" {
			*s = title
		}
		return true
	})
}

// clash is what already runs on the datastore in that time ("" if nothing).
func (wb weekBusy) clash(store string, wd, start, dur int) string {
	found := ""
	wb.walk(store, wd, start, dur, func(s *string) bool {
		found = *s
		return found == ""
	})
	return found
}

type slot struct{ wd, min int }

// slotsOf are the weekdays and times an item starts (from its plan, else from its start times).
func slotsOf(it TimelineItem, loc *time.Location) []slot {
	if it.Simple != nil {
		t, _ := time.Parse("15:04", it.Simple.Time)
		var out []slot
		for _, d := range it.Simple.Weekdays {
			out = append(out, slot{d - 1, t.Hour()*60 + t.Minute()})
		}
		return out
	}
	seen := map[slot]bool{}
	var out []slot
	for _, s := range it.Starts {
		t := time.Unix(s, 0).In(loc)
		sl := slot{(int(t.Weekday()) + 6) % 7, t.Hour()*60 + t.Minute()}
		if !seen[sl] {
			seen[sl] = true
			out = append(out, sl)
		}
	}
	return out
}

// minutesOf is how long an item runs, for planning: the longer of the last and the usual run.
func minutesOf(it TimelineItem) int {
	d := it.Duration
	if it.Typical > d {
		d = it.Typical
	}
	m := int((d + 59) / 60)
	if m < 1 {
		m = 1
	}
	return m
}

func roundUp15(m int) int { return ((m + 14) / 15 * 15) % dayMin }

// workText describes the working time: "Mo–Fr 07:00–18:00".
func workText(w WorkHours) string {
	spec, err := SimpleCalendar(w.From, w.Weekdays)
	if err != nil {
		return w.From + "–" + w.To
	}
	text := CalendarText(spec)
	text = strings.TrimSuffix(strings.TrimPrefix(text, "täglich "), " "+w.From)
	if text == w.From {
		text = "täglich"
	}
	return text + " " + w.From + "–" + w.To
}

func suggestTimeline(tl Timeline, loc *time.Location) TimelineSuggestion {
	out := TimelineSuggestion{Changes: []ScheduleChange{}, Notes: []string{}}
	movable := func(it TimelineItem) bool {
		return it.Enabled && it.Editable && it.Simple != nil && it.Problem == "" && len(it.Stores) == 1 &&
			(it.Group == "prune" || it.Group == "gc" || it.Group == "verify")
	}
	busy := weekBusy{}
	backupEnd := map[string]int{}   // datastore → end of the backup that starts latest in the day
	backupStart := map[string]int{} // … and its start
	byStore := map[string][]int{}
	for i, it := range tl.Items {
		if movable(it) {
			byStore[it.Stores[0]] = append(byStore[it.Stores[0]], i)
			continue
		}
		if !it.Enabled || it.Problem != "" {
			continue
		}
		dur := minutesOf(it)
		for _, s := range slotsOf(it, loc) {
			for _, st := range it.Stores {
				busy.mark(st, s.wd, s.min, dur, it.Title)
				if it.Group == "backup" {
					if prev, ok := backupStart[st]; !ok || s.min >= prev {
						backupStart[st], backupEnd[st] = s.min, (s.min+dur)%dayMin
					}
				}
			}
		}
	}
	stores := make([]string, 0, len(byStore))
	for st := range byStore {
		stores = append(stores, st)
	}
	sort.Strings(stores)
	rank := map[string]int{"prune": 0, "gc": 1, "verify": 2}
	after := tl
	after.Items = append([]TimelineItem(nil), tl.Items...)
	from := time.Unix(tl.From, 0).In(loc)
	to := time.Unix(tl.To, 0).In(loc)
	for _, st := range stores {
		idx := byStore[st]
		sort.SliceStable(idx, func(a, b int) bool {
			x, y := tl.Items[idx[a]], tl.Items[idx[b]]
			if rank[x.Group] != rank[y.Group] {
				return rank[x.Group] < rank[y.Group]
			}
			return x.Title < y.Title
		})
		end, hasBackup := backupEnd[st]
		pruneEnd, gcEnd := -1, -1
		for _, i := range idx {
			it := tl.Items[i]
			dur := minutesOf(it)
			cur := slotsOf(it, loc)
			start := cur[0].min
			// what is wrong with the current time
			reason := ""
			for _, s := range cur {
				if x := busy.clash(st, s.wd, s.min, dur); x != "" {
					reason = fmt.Sprintf("läuft gleichzeitig mit „%s“", x)
					break
				}
			}
			if reason == "" {
				for _, s := range cur {
					if tl.Work.overlaps(s.wd, s.min, dur) {
						reason = "läuft in der Arbeitszeit (" + workText(tl.Work) + ")"
						break
					}
				}
			}
			if reason == "" && it.Group == "gc" && pruneEnd >= 0 && (start-pruneEnd+dayMin)%dayMin > dayMin/2 {
				reason = "läuft kurz vor dem Aufräumen – was das Aufräumen löscht, gibt sie erst einen Tag später frei"
			}
			newStart := start
			if reason != "" {
				// where it should go: after the backups, the cleanup, the garbage collection
				want := start
				switch {
				case it.Group == "gc" && pruneEnd >= 0:
					want = pruneEnd + 15
				case it.Group == "verify" && gcEnd >= 0:
					want = gcEnd + 15
				case it.Group == "verify" && pruneEnd >= 0:
					want = pruneEnd + 15
				case hasBackup:
					want = end + 15
				}
				want = roundUp15(want)
				newStart = -1
				for step := 0; step < dayMin/15; step++ {
					t := (want + step*15) % dayMin
					free := true
					for _, s := range cur {
						if busy.clash(st, s.wd, t, dur) != "" || tl.Work.overlaps(s.wd, t, dur) {
							free = false
							break
						}
					}
					if free {
						newStart = t
						break
					}
				}
				if newStart < 0 {
					out.Notes = append(out.Notes, fmt.Sprintf("%s: an diesen Tagen ist auf dem Datastore außerhalb der Arbeitszeit kein freier Platz für %d Minuten", it.Title, dur))
					newStart = start
				}
			}
			for _, s := range cur {
				busy.mark(st, s.wd, newStart, dur, it.Title)
			}
			switch it.Group {
			case "prune":
				pruneEnd = (newStart + dur) % dayMin
			case "gc":
				gcEnd = (newStart + dur) % dayMin
			}
			if newStart == start {
				continue
			}
			spec, err := SimpleCalendar(fmt.Sprintf("%02d:%02d", newStart/60, newStart%60), it.Simple.Weekdays)
			if err != nil {
				continue
			}
			out.Changes = append(out.Changes, ScheduleChange{Key: it.Key, Title: it.Title, From: it.Text, FromSchedule: it.Schedule,
				To: spec, ToText: CalendarText(spec), Reason: reason})
			moved := it
			moved.Simple, moved.Problem = nil, ""
			moved.plan(spec, from, to)
			after.Items[i] = moved
		}
	}
	out.After = after
	return out
}
