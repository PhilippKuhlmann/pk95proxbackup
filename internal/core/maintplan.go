package core

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"pk95proxbackup/internal/notify"
)

// Planned maintenance: windows entered ahead (once, weekly or monthly, like the restore tests).
// At the start of a window the appliance switches the maintenance mode on until its end and
// announces it some hours before.

// MaintPlan is a planned maintenance window.
type MaintPlan struct {
	ID      string `json:"id"`
	Reason  string `json:"reason"`
	Enabled bool   `json:"enabled"`
	Repeat  string `json:"repeat"` // once, weekly, monthly
	Date    string `json:"date"`   // once: YYYY-MM-DD
	Time    string `json:"time"`   // HH:MM
	Minutes int    `json:"minutes"`
	// weekly / monthly as for the restore tests (see rtschedule.go)
	Weekdays     []int  `json:"weekdays"`
	EveryWeeks   int    `json:"every_weeks"`
	StartDate    string `json:"start_date"`
	MonthMode    string `json:"month_mode"`
	MonthWeek    string `json:"month_week"`
	MonthWeekday int    `json:"month_weekday"`
	MonthDay     int    `json:"month_day"`
	EveryMonths  int    `json:"every_months"`
	StartMonth   int    `json:"start_month"`

	PauseJobs     bool `json:"pause_jobs"`
	Mute          bool `json:"mute"`
	AnnounceHours int  `json:"announce_hours"` // 0 = no announcement
}

// sched is the plan as restore test schedule (which has the weekly and monthly rules).
func (p MaintPlan) sched() RestoreTestPlan {
	return RestoreTestPlan{Weekdays: p.Weekdays, Time: p.Time, Repeat: p.Repeat, EveryWeeks: p.EveryWeeks, StartDate: p.StartDate,
		MonthMode: p.MonthMode, MonthWeek: p.MonthWeek, MonthWeekday: p.MonthWeekday, MonthDay: p.MonthDay, EveryMonths: p.EveryMonths, StartMonth: p.StartMonth}
}

// Validate checks and completes the plan.
func (p *MaintPlan) Validate() map[string]string {
	errs := map[string]string{}
	p.Reason = strings.TrimSpace(p.Reason)
	if p.Reason == "" {
		errs["reason"] = "Grund angeben, z. B. „Updates“ oder „Umbau pve1“."
	} else if len([]rune(p.Reason)) > 120 {
		errs["reason"] = "Höchstens 120 Zeichen."
	}
	if !timeRe.MatchString(p.Time) {
		errs["time"] = "Beginn als HH:MM."
	}
	if p.Minutes < 15 || p.Minutes > 3*24*60 {
		errs["minutes"] = "Dauer 15 Minuten bis 3 Tage."
	}
	if p.AnnounceHours < 0 || p.AnnounceHours > 168 {
		errs["announce_hours"] = "0 bis 168 Stunden."
	}
	if p.Repeat == "once" {
		if _, err := time.ParseInLocation("2006-01-02", p.Date, time.Local); err != nil {
			errs["date"] = "Datum wählen."
		}
		return errs
	}
	s := p.sched()
	s.normalizeSchedule(errs)
	p.Repeat, p.EveryWeeks, p.StartDate, p.Weekdays = s.Repeat, s.EveryWeeks, s.StartDate, s.Weekdays
	p.MonthMode, p.MonthWeek, p.EveryMonths, p.StartMonth = s.MonthMode, s.MonthWeek, s.EveryMonths, s.StartMonth
	if p.Repeat == "weekly" && len(p.Weekdays) == 0 {
		errs["weekdays"] = "Mindestens einen Wochentag wählen."
	}
	return errs
}

// Windows returns the windows [start, end) of the plan that end after from, at most n.
func (p MaintPlan) Windows(from time.Time, n int) [][2]int64 {
	dur := int64(p.Minutes) * 60
	var out [][2]int64
	if p.Repeat == "once" {
		t, err := time.ParseInLocation("2006-01-02 15:04", p.Date+" "+p.Time, from.Location())
		if err == nil && t.Unix()+dur > from.Unix() {
			out = append(out, [2]int64{t.Unix(), t.Unix() + dur})
		}
		return out
	}
	for _, s := range p.sched().NextRuns(from.Add(-time.Duration(dur)*time.Second), n+1) {
		if s+dur > from.Unix() && len(out) < n {
			out = append(out, [2]int64{s, s + dur})
		}
	}
	return out
}

// ScheduleText describes when ("Am ersten Samstag jedes Monats um 08:00, 4 Std.").
func (p MaintPlan) ScheduleText() string {
	dur := minutesText(p.Minutes)
	if p.Repeat == "once" {
		t, err := time.ParseInLocation("2006-01-02", p.Date, time.Local)
		if err != nil {
			return p.Date + " " + p.Time
		}
		return weekdays[isoWeekday(t)] + ", " + t.Format("02.01.2006") + " um " + p.Time + ", " + dur
	}
	return p.sched().ScheduleText() + ", " + dur
}

// minutesText writes a duration for people: "45 Min.", "4 Std.", "1 Std. 30 Min.", "2 Tage".
func minutesText(m int) string {
	switch {
	case m >= 1440 && m%1440 == 0:
		if m == 1440 {
			return "1 Tag"
		}
		return strconv.Itoa(m/1440) + " Tage"
	case m < 60:
		return strconv.Itoa(m) + " Min."
	case m%60 == 0:
		return strconv.Itoa(m/60) + " Std."
	}
	return strconv.Itoa(m/60) + " Std. " + strconv.Itoa(m%60) + " Min."
}

// MaintPlans lists the planned windows.
func (c *Core) MaintPlans() []MaintPlan {
	var ps []MaintPlan
	_ = c.St.GetSetting("maintplans", &ps)
	sort.Slice(ps, func(i, j int) bool { return ps[i].ID < ps[j].ID })
	return ps
}

// SaveMaintPlan adds or replaces a plan (a new one gets an id).
func (c *Core) SaveMaintPlan(p MaintPlan) (MaintPlan, map[string]string) {
	if errs := p.Validate(); len(errs) > 0 {
		return p, errs
	}
	ps := c.MaintPlans()
	if p.ID == "" {
		p.ID = NewID(3)
		ps = append(ps, p)
	} else {
		found := false
		for i := range ps {
			if ps[i].ID == p.ID {
				ps[i], found = p, true
			}
		}
		if !found {
			return p, map[string]string{"reason": "Diese geplante Wartung gibt es nicht mehr."}
		}
	}
	_ = c.St.SetSetting("maintplans", ps)
	return p, nil
}

// DeleteMaintPlan removes a plan (a maintenance it started goes on until its end).
func (c *Core) DeleteMaintPlan(id string) error {
	ps := c.MaintPlans()
	keep := ps[:0]
	for _, p := range ps {
		if p.ID != id {
			keep = append(keep, p)
		}
	}
	if len(keep) == len(ps) {
		return errors.New("Nicht gefunden.")
	}
	return c.St.SetSetting("maintplans", keep)
}

type maintPlanState struct {
	Started   int64 `json:"started"`   // window start that was switched on
	Announced int64 `json:"announced"` // window start that was announced
}

// scheduleMaintPlans starts planned windows and announces them (called every minute).
func (c *Core) scheduleMaintPlans(ctx context.Context, now time.Time) {
	plans := c.MaintPlans()
	if len(plans) == 0 {
		return
	}
	var st map[string]maintPlanState
	_ = c.St.GetSetting("maintplan-state", &st)
	if st == nil {
		st = map[string]maintPlanState{}
	}
	for _, p := range plans {
		if !p.Enabled {
			continue
		}
		ws := p.Windows(now, 1)
		if len(ws) == 0 {
			continue
		}
		w, s := ws[0], st[p.ID]
		switch {
		case now.Unix() >= w[0] && s.Started != w[0]:
			if c.MaintenanceState().Active {
				continue // one is on already; this window starts when it ends (if still open)
			}
			s.Started = w[0]
			st[p.ID] = s
			_ = c.St.SetSetting("maintplan-state", st)
			if _, err := c.StartMaintenance(ctx, time.Unix(w[1], 0), p.Reason, "Zeitplan", p.PauseJobs, p.Mute); err != nil {
				c.Event("warning", "Wartung", "Geplante Wartung nicht gestartet", p.Reason+": "+err.Error())
			}
		case now.Unix() < w[0] && p.AnnounceHours > 0 && w[0]-now.Unix() <= int64(p.AnnounceHours)*3600 && s.Announced != w[0]:
			s.Announced = w[0]
			st[p.ID] = s
			_ = c.St.SetSetting("maintplan-state", st)
			c.announceMaint(p, w)
		}
	}
}

func (c *Core) announceMaint(p MaintPlan, w [2]int64) {
	from, to := time.Unix(w[0], 0), time.Unix(w[1], 0)
	when := weekdays[isoWeekday(from)] + ", " + from.Format("02.01. 15:04") + " bis "
	if to.Format("2006-01-02") == from.Format("2006-01-02") {
		when += to.Format("15:04") + " Uhr"
	} else {
		when += weekdays[isoWeekday(to)] + ", " + to.Format("02.01. 15:04") + " Uhr"
	}
	var what []string
	if p.PauseJobs {
		what = append(what, "Die Sicherungen und Kopien sind in dieser Zeit ausgesetzt und laufen danach wieder nach Plan.")
	}
	if p.Mute {
		what = append(what, "Benachrichtigungen werden zurückgehalten; am Ende kommt eine Zusammenfassung.")
	}
	c.EventDetails("warning", "Wartung", "Geplante Wartung: "+p.Reason, "Wartung "+when+". "+strings.Join(what, " "),
		&notify.Details{Status: "warning", Link: "/", Facts: []notify.Fact{{Label: "Beginn", Value: fmtWhen(w[0])}, {Label: "Ende", Value: fmtWhen(w[1])}, {Label: "Grund", Value: p.Reason}}})
}

// MaintPlanView is a plan with its next windows (for the UI).
type MaintPlanView struct {
	MaintPlan
	Text string     `json:"text"`
	Next [][2]int64 `json:"next"`
}

// MaintPlanViews lists the plans with their next three windows.
func (c *Core) MaintPlanViews() []MaintPlanView {
	out := []MaintPlanView{}
	now := time.Now()
	for _, p := range c.MaintPlans() {
		v := MaintPlanView{MaintPlan: p, Text: p.ScheduleText(), Next: p.Windows(now, 3)}
		if v.Next == nil {
			v.Next = [][2]int64{}
		}
		out = append(out, v)
	}
	return out
}

// NextMaint is the next window of any enabled plan (zero when none).
func (c *Core) NextMaint(now time.Time) (MaintPlan, [2]int64) {
	var best MaintPlan
	var bw [2]int64
	for _, p := range c.MaintPlans() {
		if !p.Enabled {
			continue
		}
		if ws := p.Windows(now, 1); len(ws) > 0 && (bw[0] == 0 || ws[0][0] < bw[0]) {
			best, bw = p, ws[0]
		}
	}
	return best, bw
}
