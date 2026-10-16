package backup

import (
	"context"
	"testing"
	"time"
)

func TestParseCronAndMatch(t *testing.T) {
	at := func(y int, mo time.Month, d, h, mi int) time.Time {
		return time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
	}
	cases := []struct {
		spec string
		t    time.Time
		want bool
	}{
		{"0 3 * * *", at(2026, 1, 2, 3, 0), true},
		{"0 3 * * *", at(2026, 1, 2, 3, 1), false},
		{"0 3 * * *", at(2026, 1, 2, 4, 0), false},
		{"*/15 * * * *", at(2026, 1, 2, 10, 0), true},
		{"*/15 * * * *", at(2026, 1, 2, 10, 45), true},
		{"*/15 * * * *", at(2026, 1, 2, 10, 10), false},
		{"0 9-17 * * 1-5", at(2026, 1, 5, 9, 0), true},  // Monday
		{"0 9-17 * * 1-5", at(2026, 1, 4, 9, 0), false}, // Sunday
		{"0 0 1,15 * *", at(2026, 1, 15, 0, 0), true},
		{"0 0 1,15 * *", at(2026, 1, 14, 0, 0), false},
	}
	for _, c := range cases {
		s, err := ParseCron(c.spec)
		if err != nil {
			t.Fatalf("ParseCron(%q): %v", c.spec, err)
		}
		if got := s.Matches(c.t); got != c.want {
			t.Errorf("%q.Matches(%s) = %v, want %v", c.spec, c.t.Format(time.RFC3339), got, c.want)
		}
	}
}

func TestParseCronRejectsBadSpecs(t *testing.T) {
	for _, spec := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "*/0 * * * *", "a * * * *", "* * * * 8"} {
		if _, err := ParseCron(spec); err == nil {
			t.Errorf("ParseCron(%q) accepted an invalid spec", spec)
		}
	}
}

func TestSchedulerTickFiresOncePerMinute(t *testing.T) {
	st := newTestStore(t)
	s, err := NewScheduler(SchedulerDeps{
		DB: st.DB(), Dir: t.TempDir(), Version: st.Version,
		Schedule: "0 3 * * *", Keep: 2, Events: st,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Date(2026, 1, 2, 3, 0, 10, 0, time.UTC)
	s.Tick(ctx, now)
	s.Tick(ctx, now.Add(10*time.Second)) // same minute: no second run
	if got := countBackupEvents(t, st); got != 1 {
		t.Fatalf("backup events = %d, want 1 (once per minute)", got)
	}
	// A different matching day fires again.
	s.Tick(ctx, now.Add(24*time.Hour))
	if got := countBackupEvents(t, st); got != 2 {
		t.Fatalf("backup events = %d, want 2", got)
	}
}

// TestCronSundaySevenAndDomDowOr pins known-issues L17: 7 is Sunday, and
// day-of-month ORs with day-of-week when both are restricted (Vixie cron).
func TestCronSundaySevenAndDomDowOr(t *testing.T) {
	seven, err := ParseCron("0 3 * * 7")
	if err != nil {
		t.Fatal(err)
	}
	if !seven.Matches(time.Date(2026, 1, 4, 3, 0, 0, 0, time.UTC)) { // 2026-01-04 is a Sunday
		t.Error("day-of-week 7 did not match Sunday (known-issues L17)")
	}

	both, err := ParseCron("0 3 1 * 1") // the 1st OR Monday
	if err != nil {
		t.Fatal(err)
	}
	if !both.Matches(time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)) { // the 1st (Thursday)
		t.Error("dom/dow OR: the 1st did not match")
	}
	if !both.Matches(time.Date(2026, 1, 5, 3, 0, 0, 0, time.UTC)) { // a Monday
		t.Error("dom/dow OR: Monday did not match")
	}
	if both.Matches(time.Date(2026, 1, 6, 3, 0, 0, 0, time.UTC)) { // Tuesday, not the 1st
		t.Error("dom/dow OR matched a non-1st non-Monday")
	}
}
