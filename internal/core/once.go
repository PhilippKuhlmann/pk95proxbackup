package core

import "time"

// oncePerDay reports true the first time it is asked for key on the day of now, and remembers
// that in the store (survives a restart).
func (c *Core) oncePerDay(key string, now time.Time) bool {
	day := now.Format("2006-01-02")
	var last string
	_ = c.St.GetSetting(key, &last)
	if last == day {
		return false
	}
	_ = c.St.SetSetting(key, day)
	return true
}

// tryNow reports true at most once per interval for key (kept in memory only: for retries of
// work that failed).
func (c *Core) tryNow(key string, every time.Duration, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tries == nil {
		c.tries = map[string]time.Time{}
	}
	if last, ok := c.tries[key]; ok && now.Sub(last) < every {
		return false
	}
	c.tries[key] = now
	return true
}
