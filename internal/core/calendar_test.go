package core

import (
	"strings"
	"testing"
	"time"
)

func TestCalendarBetween(t *testing.T) {
	loc := time.UTC
	from := time.Date(2026, 11, 23, 0, 0, 0, 0, loc) // a Monday
	to := from.AddDate(0, 0, 7)
	fmtAll := func(ts []time.Time) string {
		var s []string
		for _, x := range ts {
			s = append(s, x.Format("Mon 02 15:04"))
		}
		return strings.Join(s, ", ")
	}
	for spec, want := range map[string]string{
		"21:00":          "Mon 23 21:00, Tue 24 21:00, Wed 25 21:00, Thu 26 21:00, Fri 27 21:00, Sat 28 21:00, Sun 29 21:00",
		"mon..fri 02:30": "Mon 23 02:30, Tue 24 02:30, Wed 25 02:30, Thu 26 02:30, Fri 27 02:30",
		"sat 04:00":      "Sat 28 04:00",
		"sat,sun 6:5":    "Sat 28 06:05, Sun 29 06:05",
		"fri..mon 1:00":  "Mon 23 01:00, Fri 27 01:00, Sat 28 01:00, Sun 29 01:00",
		"*-*-25 03:00":   "Wed 25 03:00",
		"*-11-24":        "Tue 24 00:00",
		"2026-11-26 8:0": "Thu 26 08:00",
		"weekly":         "Mon 23 00:00",
		"sun 0/12:00":    "Sun 29 00:00, Sun 29 12:00",
		"daily":          "Mon 23 00:00, Tue 24 00:00, Wed 25 00:00, Thu 26 00:00, Fri 27 00:00, Sat 28 00:00, Sun 29 00:00",
	} {
		c, err := ParseCalendar(spec)
		if err != nil {
			t.Fatalf("%s: %v", spec, err)
		}
		if got := fmtAll(c.Between(from, to, 100)); got != want {
			t.Errorf("%s:\n got  %s\n want %s", spec, got, want)
		}
	}
	c, _ := ParseCalendar("*:0/15")
	if n := len(c.Between(from, from.AddDate(0, 0, 1), 1000)); n != 96 {
		t.Fatalf("every 15 minutes: %d", n)
	}
	for _, bad := range []string{"", "25:00", "mon..xyz 01:00", "21:00 22:00", "*-13-01"} {
		if _, err := ParseCalendar(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSimpleCalendar(t *testing.T) {
	for _, x := range []struct {
		hhmm string
		days []int
		want string
	}{
		{"21:00", []int{1, 2, 3, 4, 5, 6, 7}, "21:00"},
		{"2:30", []int{1, 2, 3, 4, 5}, "mon..fri 02:30"},
		{"04:00", []int{6}, "sat 04:00"},
		{"04:00", []int{6, 7}, "sat,sun 04:00"},
		{"04:00", []int{1, 3, 5}, "mon,wed,fri 04:00"},
	} {
		got, err := SimpleCalendar(x.hhmm, x.days)
		if err != nil || got != x.want {
			t.Errorf("%s %v: %q %v", x.hhmm, x.days, got, err)
		}
		c, _ := ParseCalendar(got)
		if hm, days, ok := c.Simple(); !ok || len(days) != len(x.days) || hm[3:] != x.want[len(x.want)-2:] {
			t.Errorf("%s back: %s %v %v", got, hm, days, ok)
		}
	}
	if _, err := SimpleCalendar("21:00", nil); err == nil {
		t.Fatal("no weekday accepted")
	}
	for _, spec := range []string{"*-*-01 03:00", "0/6:00"} {
		c, _ := ParseCalendar(spec)
		if _, _, ok := c.Simple(); ok {
			t.Errorf("%s is not simple", spec)
		}
	}
}
