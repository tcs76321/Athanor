package trigger

import (
	"context"
	"testing"
	"time"
)

func TestNextRunInRespectsTimezone(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skipf("timezone database unavailable: %v", err)
	}
	from := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	got, ok := NextRunIn("0 7 * * *", from, la)
	if !ok {
		t.Fatal("NextRunIn returned no slot")
	}
	want := time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC) // 07:00 PST
	if !got.Equal(want) {
		t.Errorf("next = %s, want %s", got, want)
	}
}

func TestNextRunInHandlesDST(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skipf("timezone database unavailable: %v", err)
	}
	// 2026-03-08 is the US spring-forward date; 07:00 local is PDT (UTC-7).
	from := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)
	got, ok := NextRunIn("0 7 * * *", from, la)
	if !ok {
		t.Fatal("NextRunIn returned no slot")
	}
	want := time.Date(2026, 3, 8, 14, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("next = %s, want %s", got, want)
	}
}

func TestEnsureInStoresTimezone(t *testing.T) {
	_, r := openRepo(t)
	tr, err := r.EnsureIn(context.Background(), "p1", "daily-brief", KindSchedule, "0 7 * * *", true, "America/Los_Angeles")
	if err != nil {
		t.Fatalf("EnsureIn: %v", err)
	}
	if tr.Timezone != "America/Los_Angeles" {
		t.Errorf("timezone = %q", tr.Timezone)
	}
	if tr.NextRunAt == "" {
		t.Error("next run not set")
	}
}

func TestEnsureInRejectsBadTimezone(t *testing.T) {
	_, r := openRepo(t)
	if _, err := r.EnsureIn(context.Background(), "p1", "t", KindSchedule, "0 7 * * *", true, "Mars/Olympus"); err == nil {
		t.Fatal("bad timezone accepted")
	}
}
