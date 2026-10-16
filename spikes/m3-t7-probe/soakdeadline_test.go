package main

import (
	"testing"
	"time"
)

func TestResolveDeadline(t *testing.T) {
	loc := time.Local
	now := time.Date(2026, 10, 15, 18, 30, 0, 0, loc)

	got, err := resolveDeadline("midnight", now)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 16, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Errorf("midnight = %s, want %s", got, want)
	}

	got, err = resolveDeadline("23:00", now)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 15, 23, 0, 0, 0, loc); !got.Equal(want) {
		t.Errorf("23:00 = %s, want %s", got, want)
	}

	// A time already past rolls to tomorrow.
	got, err = resolveDeadline("06:00", now)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 16, 6, 0, 0, 0, loc); !got.Equal(want) {
		t.Errorf("06:00 = %s, want %s", got, want)
	}

	got, err = resolveDeadline("2026-10-16T02:00:00Z", now)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(time.Date(2026, 10, 16, 2, 0, 0, 0, time.UTC)) {
		t.Errorf("rfc3339 = %s", got)
	}

	if _, err := resolveDeadline("not-a-time", now); err == nil {
		t.Error("garbage -until accepted")
	}
}
