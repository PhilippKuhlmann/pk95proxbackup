package core

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Calendar events of Proxmox VE and the Backup Server (systemd style): "21:00",
// "mon..fri 02:30", "sat 18:15", "*-*-01 03:00", "daily", "*:0/15". The appliance evaluates
// them itself to draw the time plan; it understands the forms the Proxmox interfaces offer
// (weekdays, date with lists, ranges and repetitions, hours and minutes), not every corner of
// the syntax (no time zones, no "~" for the end of a month).

// Calendar is a parsed calendar event.
type Calendar struct {
	weekdays [7]bool // Monday … Sunday
	years    []int   // nil: every year
	months   [13]bool
	days     [32]bool
	hours    [24]bool
	minutes  [60]bool
}

var calShortcuts = map[string]string{
	"minutely": "*:*", "hourly": "*:00", "daily": "00:00", "weekly": "mon 00:00", "monthly": "*-*-01 00:00",
	"yearly": "*-01-01 00:00", "annually": "*-01-01 00:00", "quarterly": "*-01,04,07,10-01 00:00",
	"semiannually": "*-01,07-01 00:00",
}

var calWeekdays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

// ParseCalendar parses a calendar event.
func ParseCalendar(s string) (Calendar, error) {
	var c Calendar
	s = strings.ToLower(strings.TrimSpace(s))
	if v, ok := calShortcuts[s]; ok {
		s = v
	}
	if s == "" {
		return c, errors.New("leerer Zeitplan")
	}
	var wd, date, tm string
	for _, tok := range strings.Fields(s) {
		switch {
		case strings.Contains(tok, ":"):
			if tm != "" {
				return c, fmt.Errorf("zwei Uhrzeiten in „%s“", s)
			}
			tm = tok
		case strings.Contains(tok, "-") && !strings.ContainsAny(tok, "abcdefghijklmnopqrstuvwxyz"):
			date = tok
		case wd == "" && date == "" && tm == "":
			wd = tok
		default:
			return c, fmt.Errorf("„%s“ verstehe ich nicht", tok)
		}
	}
	// weekdays
	if wd == "" {
		for i := range c.weekdays {
			c.weekdays[i] = true
		}
	} else if err := parseWeekdays(wd, &c.weekdays); err != nil {
		return c, err
	}
	// date: [year-]month-day
	for i := 1; i <= 12; i++ {
		c.months[i] = true
	}
	for i := 1; i <= 31; i++ {
		c.days[i] = true
	}
	if date != "" {
		parts := strings.Split(date, "-")
		if len(parts) == 2 {
			parts = append([]string{"*"}, parts...)
		}
		if len(parts) != 3 {
			return c, fmt.Errorf("Datum „%s“ verstehe ich nicht", date)
		}
		if parts[0] != "*" {
			ys, err := calField(parts[0], 1970, 2199)
			if err != nil {
				return c, err
			}
			for y, on := range ys {
				if on {
					c.years = append(c.years, y)
				}
			}
		}
		ms, err := calField(parts[1], 1, 12)
		if err != nil {
			return c, err
		}
		ds, err := calField(parts[2], 1, 31)
		if err != nil {
			return c, err
		}
		for i := range c.months {
			c.months[i] = ms[i]
		}
		for i := range c.days {
			c.days[i] = ds[i]
		}
	}
	// time: hour:minute[:second]; without time midnight
	if tm == "" {
		tm = "00:00"
	}
	hm := strings.Split(tm, ":")
	if len(hm) < 2 || len(hm) > 3 {
		return c, fmt.Errorf("Uhrzeit „%s“ verstehe ich nicht", tm)
	}
	hs, err := calField(hm[0], 0, 23)
	if err != nil {
		return c, err
	}
	mins, err := calField(hm[1], 0, 59)
	if err != nil {
		return c, err
	}
	for i := range c.hours {
		c.hours[i] = hs[i]
	}
	for i := range c.minutes {
		c.minutes[i] = mins[i]
	}
	return c, nil
}

func parseWeekdays(s string, out *[7]bool) error {
	idx := func(name string) int {
		for i, w := range calWeekdays {
			if strings.HasPrefix(name, w) {
				return i
			}
		}
		return -1
	}
	for _, part := range strings.Split(s, ",") {
		a, b, isRange := strings.Cut(part, "..")
		i := idx(a)
		if i < 0 {
			return fmt.Errorf("Wochentag „%s“ verstehe ich nicht", a)
		}
		j := i
		if isRange {
			if j = idx(b); j < 0 {
				return fmt.Errorf("Wochentag „%s“ verstehe ich nicht", b)
			}
		}
		for k := i; ; k = (k + 1) % 7 {
			out[k] = true
			if k == j {
				break
			}
		}
	}
	return nil
}

// calField parses one numeric field: "*", "5", "1,15", "8..17", "0/15", "*/2", "8..18/2".
func calField(s string, lo, hi int) (map[int]bool, error) {
	out := map[int]bool{}
	bad := fmt.Errorf("„%s“ verstehe ich nicht", s)
	for _, part := range strings.Split(s, ",") {
		base, stepS, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepS)
			if err != nil || n < 1 {
				return nil, bad
			}
			step = n
		}
		from, to := lo, hi
		switch {
		case base == "*":
		case strings.Contains(base, ".."):
			a, b, _ := strings.Cut(base, "..")
			x, err1 := strconv.Atoi(a)
			y, err2 := strconv.Atoi(b)
			if err1 != nil || err2 != nil {
				return nil, bad
			}
			from, to = x, y
		default:
			x, err := strconv.Atoi(base)
			if err != nil {
				return nil, bad
			}
			from = x
			if !hasStep {
				to = x
			}
		}
		if from < lo || to > hi || from > to {
			return nil, fmt.Errorf("„%s“ liegt außerhalb von %d–%d", part, lo, hi)
		}
		for v := from; v <= to; v += step {
			out[v] = true
		}
	}
	return out, nil
}

// Between returns the start times in [from, to), at most max.
func (c Calendar) Between(from, to time.Time, max int) []time.Time {
	var out []time.Time
	loc := from.Location()
	day := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	for ; day.Before(to) && len(out) < max; day = day.AddDate(0, 0, 1) {
		if !c.weekdays[(int(day.Weekday())+6)%7] || !c.months[int(day.Month())] || !c.days[day.Day()] {
			continue
		}
		if c.years != nil && !slices.Contains(c.years, day.Year()) {
			continue
		}
		for h := 0; h < 24; h++ {
			if !c.hours[h] {
				continue
			}
			for m := 0; m < 60; m++ {
				if !c.minutes[m] {
					continue
				}
				t := time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, loc)
				if !t.Before(from) && t.Before(to) {
					out = append(out, t)
					if len(out) >= max {
						return out
					}
				}
			}
		}
	}
	return out
}

// Simple returns time and weekdays (1 = Monday) of a plan that runs once on chosen weekdays
// ("mon..fri 21:00"); ok is false for anything else (dates, several times a day).
func (c Calendar) Simple() (hhmm string, weekdays []int, ok bool) {
	if c.years != nil {
		return "", nil, false
	}
	for i := 1; i <= 12; i++ {
		if !c.months[i] {
			return "", nil, false
		}
	}
	for i := 1; i <= 31; i++ {
		if !c.days[i] {
			return "", nil, false
		}
	}
	h, m := -1, -1
	for i, on := range c.hours {
		if on {
			if h >= 0 {
				return "", nil, false
			}
			h = i
		}
	}
	for i, on := range c.minutes {
		if on {
			if m >= 0 {
				return "", nil, false
			}
			m = i
		}
	}
	for i, on := range c.weekdays {
		if on {
			weekdays = append(weekdays, i+1)
		}
	}
	return fmt.Sprintf("%02d:%02d", h, m), weekdays, h >= 0 && m >= 0 && len(weekdays) > 0
}

// SimpleCalendar writes a plan "once at hhmm on these weekdays" as a calendar event:
// every day "21:00", else "mon..fri 21:00" or "mon,wed,fri 21:00".
func SimpleCalendar(hhmm string, weekdays []int) (string, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(hhmm))
	if err != nil {
		return "", fmt.Errorf("Uhrzeit „%s“: bitte als HH:MM angeben", hhmm)
	}
	on := [7]bool{}
	for _, d := range weekdays {
		if d < 1 || d > 7 {
			return "", fmt.Errorf("Wochentag %d gibt es nicht", d)
		}
		on[d-1] = true
	}
	tm := t.Format("15:04")
	n := 0
	for _, x := range on {
		if x {
			n++
		}
	}
	switch n {
	case 0:
		return "", errors.New("mindestens einen Wochentag wählen")
	case 7:
		return tm, nil
	}
	// runs of consecutive days: three or more as a range
	var parts []string
	for i := 0; i < 7; {
		if !on[i] {
			i++
			continue
		}
		j := i
		for j+1 < 7 && on[j+1] {
			j++
		}
		switch {
		case j-i >= 2:
			parts = append(parts, calWeekdays[i]+".."+calWeekdays[j])
		case j > i:
			parts = append(parts, calWeekdays[i], calWeekdays[j])
		default:
			parts = append(parts, calWeekdays[i])
		}
		i = j + 1
	}
	return strings.Join(parts, ",") + " " + tm, nil
}
