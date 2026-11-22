package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pk95proxbackup/internal/core"
)

// timeline returns the time plan (what runs when, how long it took).
func (s *Server) timeline(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.C.Timeline(ctx, time.Now()))
}

// timelineSuggest proposes times for cleanup, garbage collection and verification, with the
// time plan as it would look (nothing is changed).
func (s *Server) timelineSuggest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.C.SuggestTimeline(ctx, time.Now()))
}

// setWorkHours stores the working time the time plan keeps housekeeping out of.
func (s *Server) setWorkHours(w http.ResponseWriter, r *http.Request) {
	var p core.WorkHours
	if !decode(w, r, &p) {
		return
	}
	if err := s.C.SetWorkHours(p); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	got := s.C.WorkHours()
	text := "keine Arbeitszeit mehr berücksichtigt"
	if got.Enabled {
		spec, _ := core.SimpleCalendar(got.From, got.Weekdays)
		text = "Arbeitszeit " + strings.TrimSuffix(core.CalendarText(spec), " "+got.From) + " " + got.From + "–" + got.To
	}
	s.C.Event("info", "Zeitplan", "Arbeitszeit geändert", text+" (von "+currentUser(r).Username+").")
	writeJSON(w, http.StatusOK, got)
}

// scheduleSpec is a calendar event, given as such or as time and weekdays.
type scheduleSpec struct {
	Key      string `json:"key"`
	Schedule string `json:"schedule"`
	Time     string `json:"time"`
	Weekdays []int  `json:"weekdays"`
}

func (p scheduleSpec) spec() (string, error) {
	if strings.TrimSpace(p.Schedule) != "" {
		_, err := core.ParseCalendar(p.Schedule)
		return strings.TrimSpace(p.Schedule), err
	}
	return core.SimpleCalendar(p.Time, p.Weekdays)
}

// timelinePreview shows when a schedule would run (while editing it).
func (s *Server) timelinePreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := scheduleSpec{Schedule: q.Get("schedule"), Time: q.Get("time")}
	for _, d := range strings.Split(q.Get("weekdays"), ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(d)); err == nil {
			p.Weekdays = append(p.Weekdays, n)
		}
	}
	spec, err := p.spec()
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	c, _ := core.ParseCalendar(spec)
	now := time.Now()
	next := []int64{}
	for _, t := range c.Between(now, now.AddDate(1, 0, 0), 5) {
		next = append(next, t.Unix())
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedule": spec, "text": core.CalendarText(spec), "next": next})
}

// setTimeline changes when a job runs.
func (s *Server) setTimeline(w http.ResponseWriter, r *http.Request) {
	var p scheduleSpec
	if !decode(w, r, &p) {
		return
	}
	spec, err := p.spec()
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	title, err := s.C.SetSchedule(ctx, p.Key, spec)
	if err != nil {
		fail(w, err)
		return
	}
	text := core.CalendarText(spec)
	s.C.Event("info", "Zeitplan", "Zeitplan geändert: "+title, "Läuft jetzt "+text+" ("+spec+"), geändert von "+currentUser(r).Username+".")
	writeJSON(w, http.StatusOK, map[string]string{"title": title, "schedule": spec, "text": text})
}
