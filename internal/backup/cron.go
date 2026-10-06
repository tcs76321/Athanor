// Package backup implements §23.4 backups (ROADMAP M7-T4): scheduled
// snapshots via store.Backup (VACUUM INTO), retention pruning, and an offline
// restore. The scheduler is a minimal 5-field cron matcher — no dependency.
package backup

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed 5-field cron spec: minute hour day-of-month month
// day-of-week. Supported field syntax: "*", "N", "a-b", "*/n", "a-b/n", and
// comma-separated lists of those.
type Schedule struct {
	minute, hour, dom, month, dow fieldSet
}

type fieldSet struct {
	any    bool
	values map[int]bool
}

func (f fieldSet) has(v int) bool { return f.any || f.values[v] }

// ParseCron parses a 5-field cron specification.
func ParseCron(spec string) (Schedule, error) {
	fields := strings.Fields(strings.TrimSpace(spec))
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf("backup: cron spec %q must have 5 fields", spec)
	}
	var s Schedule
	var err error
	if s.minute, err = parseField(fields[0], 0, 59); err != nil {
		return Schedule{}, fmt.Errorf("minute: %w", err)
	}
	if s.hour, err = parseField(fields[1], 0, 23); err != nil {
		return Schedule{}, fmt.Errorf("hour: %w", err)
	}
	if s.dom, err = parseField(fields[2], 1, 31); err != nil {
		return Schedule{}, fmt.Errorf("day-of-month: %w", err)
	}
	if s.month, err = parseField(fields[3], 1, 12); err != nil {
		return Schedule{}, fmt.Errorf("month: %w", err)
	}
	if s.dow, err = parseField(fields[4], 0, 6); err != nil {
		return Schedule{}, fmt.Errorf("day-of-week: %w", err)
	}
	return s, nil
}

// Matches reports whether t is within the schedule (to the minute).
func (s Schedule) Matches(t time.Time) bool {
	return s.minute.has(t.Minute()) &&
		s.hour.has(t.Hour()) &&
		s.dom.has(t.Day()) &&
		s.month.has(int(t.Month())) &&
		s.dow.has(int(t.Weekday()))
}

func parseField(spec string, min, max int) (fieldSet, error) {
	f := fieldSet{values: map[int]bool{}}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "*" {
			f.any = true
			continue
		}
		step := 1
		if i := strings.IndexByte(part, '/'); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return fieldSet{}, fmt.Errorf("bad step in %q", part)
			}
			step = n
			part = part[:i]
		}
		lo, hi := min, max
		if part != "*" {
			if i := strings.IndexByte(part, '-'); i >= 0 {
				var err error
				if lo, err = strconv.Atoi(part[:i]); err != nil {
					return fieldSet{}, fmt.Errorf("bad range in %q", part)
				}
				if hi, err = strconv.Atoi(part[i+1:]); err != nil {
					return fieldSet{}, fmt.Errorf("bad range in %q", part)
				}
			} else {
				n, err := strconv.Atoi(part)
				if err != nil {
					return fieldSet{}, fmt.Errorf("bad value %q", part)
				}
				lo, hi = n, n
			}
		}
		if lo < min || hi > max || lo > hi {
			return fieldSet{}, fmt.Errorf("value out of range in %q (want %d-%d)", part, min, max)
		}
		for v := lo; v <= hi; v += step {
			f.values[v] = true
		}
	}
	return f, nil
}
