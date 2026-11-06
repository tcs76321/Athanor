package ui

import (
	"testing"
	"time"
)

func TestWhenLabel(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"empty", time.Time{}, ""},
		{"future", now.Add(time.Minute), "just now"},
		{"just now", now.Add(-30 * time.Second), "just now"},
		{"minutes", now.Add(-5 * time.Minute), "5m ago"},
		{"today", now.Add(-3 * time.Hour), "09:00"},
		{"yesterday", now.AddDate(0, 0, -1), "Yesterday 12:00"},
		{"this year", now.AddDate(0, -2, 0), "Sep 6 12:00"},
		{"older", now.AddDate(-1, 0, 0), "Nov 6 2025"},
	}
	for _, tc := range cases {
		if got := whenLabel(tc.t, now); got != tc.want {
			t.Errorf("%s: whenLabel = %q, want %q", tc.name, got, tc.want)
		}
	}
}
