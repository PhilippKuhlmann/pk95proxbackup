package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Schedule of a restore test plan:
//   weekly:  weekdays, every N weeks counted from the start date,
//   monthly: the n-th or last given weekday, or a day of the month (last day if shorter),
//            every N months counted from a start month.

var monthShort = []string{"", "Jan", "Feb", "Mär", "Apr", "Mai", "Jun", "Jul", "Aug", "Sep", "Okt", "Nov", "Dez"}

// normalizeSchedule fills defaults and checks the schedule fields.
func (p *RestoreTestPlan) normalizeSchedule(errs map[string]string) {
	switch p.Repeat {
	case "monthly":
		if p.MonthMode != "day" {
			p.MonthMode = "weekday"
		}
		if p.MonthMode == "weekday" {
			if p.MonthWeek != "last" {
				if n, err := strconv.Atoi(p.MonthWeek); err != nil || n < 1 || n > 4 {
					p.MonthWeek = "1"
				}
			}
			if p.MonthWeekday < 1 || p.MonthWeekday > 7 {
				errs["month_weekday"] = "Wochentag wählen."
			}
		} else if p.MonthDay < 1 || p.MonthDay > 31 {
			errs["month_day"] = "Tag 1 bis 31 (31 = letzter Tag des Monats)."
		}
		switch p.EveryMonths {
		case 1, 2, 3, 4, 6, 12:
		default:
			p.EveryMonths = 1
		}
		if p.StartMonth < 1 || p.StartMonth > 12 {
			p.StartMonth = int(time.Now().Month())
		}
	default:
		p.Repeat = "weekly"
		if p.EveryWeeks < 1 || p.EveryWeeks > 8 {
			p.EveryWeeks = 1
		}
		if p.StartDate == "" {
			p.StartDate = time.Now().Format("2006-01-02")
		}
		if _, err := time.ParseInLocation("2006-01-02", p.StartDate, time.Local); err != nil {
			errs["start_date"] = "Datum als JJJJ-MM-TT."
		}
		sort.Ints(p.Weekdays)
	}
}

// DueOn reports whether the plan runs on the day of t (ignoring the time of day).
func (p RestoreTestPlan) DueOn(t time.Time) bool {
	wd := isoWeekday(t)
	if p.Repeat == "monthly" {
		every := p.EveryMonths
		if every < 1 {
			every = 1
		}
		start := p.StartMonth
		if start < 1 {
			start = 1
		}
		if ((int(t.Month())-start)%every+every)%every != 0 {
			return false
		}
		last := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
		if p.MonthMode == "day" {
			day := p.MonthDay
			if day > last {
				day = last
			}
			return t.Day() == day
		}
		if wd != p.MonthWeekday {
			return false
		}
		if p.MonthWeek == "last" {
			return t.Day()+7 > last
		}
		n, _ := strconv.Atoi(p.MonthWeek)
		return (t.Day()-1)/7+1 == n
	}
	if !contains(p.Weekdays, wd) {
		return false
	}
	if p.EveryWeeks <= 1 {
		return true
	}
	start, err := time.ParseInLocation("2006-01-02", p.StartDate, t.Location())
	if err != nil {
		return true
	}
	// Weeks between the Mondays of the start week and of this week.
	monday := func(x time.Time) time.Time {
		x = time.Date(x.Year(), x.Month(), x.Day(), 12, 0, 0, 0, x.Location())
		return x.AddDate(0, 0, 1-isoWeekday(x))
	}
	weeks := int(monday(t).Sub(monday(start)).Hours()/24+0.5) / 7
	if weeks < 0 {
		return false
	}
	return weeks%p.EveryWeeks == 0
}

// NextRuns returns the next n start times from now on.
func (p RestoreTestPlan) NextRuns(now time.Time, n int) []int64 {
	out := []int64{}
	if p.Time == "" || (p.Repeat != "monthly" && len(p.Weekdays) == 0) {
		return out
	}
	tm, err := time.ParseInLocation("15:04", p.Time, now.Location())
	if err != nil {
		return out
	}
	for i := 0; i < 800 && len(out) < n; i++ {
		d := time.Date(now.Year(), now.Month(), now.Day()+i, tm.Hour(), tm.Minute(), 0, 0, now.Location())
		if d.After(now) && p.DueOn(d) {
			out = append(out, d.Unix())
		}
	}
	return out
}

// ScheduleText describes the schedule ("Am letzten Sonntag alle 3 Monate (Jan, Apr, Jul, Okt) um 02:00").
func (p RestoreTestPlan) ScheduleText() string {
	at := ""
	if p.Time != "" {
		at = " um " + p.Time
	}
	if p.Repeat == "monthly" {
		var day string
		if p.MonthMode == "day" {
			if p.MonthDay >= 31 {
				day = "Am letzten Tag"
			} else {
				day = fmt.Sprintf("Am %d.", p.MonthDay)
			}
		} else {
			nth := map[string]string{"1": "ersten", "2": "zweiten", "3": "dritten", "4": "vierten", "last": "letzten"}[p.MonthWeek]
			day = "Am " + nth + " " + weekdays[p.MonthWeekday]
		}
		switch p.EveryMonths {
		case 0, 1:
			return day + " jedes Monats" + at
		case 12:
			return day + " im " + monthNames[p.StartMonth] + at + " (einmal im Jahr)"
		}
		var months []string
		for m := p.StartMonth; len(months) < 12/p.EveryMonths; m += p.EveryMonths {
			months = append(months, monthShort[(m-1)%12+1])
		}
		return fmt.Sprintf("%s alle %d Monate (%s)%s", day, p.EveryMonths, strings.Join(months, ", "), at)
	}
	var names []string
	for _, d := range p.Weekdays {
		if d >= 1 && d <= 7 {
			names = append(names, weekdays[d])
		}
	}
	if len(names) == 0 {
		return "Kein Zeitplan"
	}
	days := strings.Join(names, ", ")
	if i := strings.LastIndex(days, ", "); i > 0 {
		days = days[:i] + " und " + days[i+2:]
	}
	if p.EveryWeeks > 1 {
		return fmt.Sprintf("%s alle %d Wochen%s", days, p.EveryWeeks, at)
	}
	return "Jeden " + days + at
}
