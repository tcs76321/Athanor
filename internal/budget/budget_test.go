package budget_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/budget"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func TestWindowForUsesTheConfiguredBoundary(t *testing.T) {
	s := budget.Settings{Window: 24 * time.Hour, WindowStart: "18:00"}
	loc := time.UTC

	before, err := budget.WindowFor(time.Date(2026, 10, 29, 17, 0, 0, 0, loc), s)
	if err != nil {
		t.Fatal(err)
	}
	wantStart := time.Date(2026, 10, 28, 18, 0, 0, 0, loc)
	if !before.Start.Equal(wantStart) {
		t.Errorf("start = %s, want %s", before.Start, wantStart)
	}
	if got := before.End.Sub(before.Start); got != 24*time.Hour {
		t.Errorf("window length = %s", got)
	}

	after, err := budget.WindowFor(time.Date(2026, 10, 29, 19, 0, 0, 0, loc), s)
	if err != nil {
		t.Fatal(err)
	}
	wantAfter := time.Date(2026, 10, 29, 18, 0, 0, 0, loc)
	if !after.Start.Equal(wantAfter) {
		t.Errorf("start = %s, want %s", after.Start, wantAfter)
	}
}

func TestWindowForRejectsBadClock(t *testing.T) {
	for _, v := range []string{"24:00", "18:60", "1800", "aa:bb"} {
		if _, err := budget.WindowFor(time.Now(), budget.Settings{Window: time.Hour, WindowStart: v}); err == nil {
			t.Errorf("window_start %q accepted", v)
		}
	}
}

func TestBucketCeilings(t *testing.T) {
	s := budget.Settings{Window: 24 * time.Hour, Tokens: 1000, ReservePct: 40, BackgroundPct: 60}
	if got := s.CeilingFor(budget.Interactive, 1000); got != 1000 {
		t.Errorf("interactive ceiling = %d, want 1000", got)
	}
	if got := s.CeilingFor(budget.Background, 1000); got != 600 {
		t.Errorf("background ceiling = %d, want 600", got)
	}
	if got := budget.GrantSize(500, 300, 1000); got != 300 {
		t.Errorf("grant = %d, want the per-grant cap 300", got)
	}
	if got := budget.GrantSize(500, 0, 120); got != 120 {
		t.Errorf("grant = %d, want remaining 120", got)
	}
	if got := budget.GrantSize(500, 0, 0); got != 0 {
		t.Errorf("grant = %d, want 0", got)
	}
}

func testLedger(t *testing.T) *budget.Ledger {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	// The ledger tests insert grants without a full project/task/job graph.
	if _, err := st.DB().Exec("PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	return budget.New(st.DB())
}

func TestReserveSettleRelease(t *testing.T) {
	ctx := context.Background()
	l := testLedger(t)
	s := budget.Settings{
		Window: 24 * time.Hour, WindowStart: "18:00",
		Tokens: 1000, ReservePct: 40, BackgroundPct: 60, MaxGrant: 300,
	}
	w := budget.Window{Start: time.Date(2026, 10, 29, 18, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC)}

	g1, ok, err := l.Reserve(ctx, w, s, "job-1", budget.Interactive, 300, 0)
	if err != nil || !ok || g1.Tokens != 300 {
		t.Fatalf("interactive reserve = %+v ok=%v err=%v", g1, ok, err)
	}
	g2, ok, err := l.Reserve(ctx, w, s, "job-2", budget.Background, 300, 0)
	if err != nil || !ok || g2.Tokens != 300 {
		t.Fatalf("background reserve = %+v ok=%v err=%v", g2, ok, err)
	}
	// Background ceiling is 600 and 600 is committed (interactive counts too),
	// so background may not cross the reserve.
	if _, ok, _ := l.Reserve(ctx, w, s, "job-3", budget.Background, 400, 0); ok {
		t.Fatal("background reserve crossed the interactive reserve")
	}
	// Interactive may still spend the rest, clamped by the per-grant cap.
	g4, ok, err := l.Reserve(ctx, w, s, "job-4", budget.Interactive, 400, 0)
	if err != nil || !ok || g4.Tokens != 300 {
		t.Fatalf("per-grant clamp = %+v ok=%v err=%v", g4, ok, err)
	}
	g5, ok, err := l.Reserve(ctx, w, s, "job-5", budget.Interactive, 200, 0)
	if err != nil || !ok || g5.Tokens != 100 {
		t.Fatalf("remaining clamp = %+v ok=%v err=%v", g5, ok, err)
	}
	if _, ok, _ := l.Reserve(ctx, w, s, "job-6", budget.Interactive, 1, 0); ok {
		t.Fatal("reserve succeeded with an exhausted window")
	}

	if err := l.Settle(ctx, "job-1", 250, 0); err != nil {
		t.Fatal(err)
	}
	st, err := l.Status(ctx, w, s)
	if err != nil {
		t.Fatal(err)
	}
	if st.TokensCommitted != 950 {
		t.Errorf("committed = %d, want 950 (250 settled + 300 + 300 + 100)", st.TokensCommitted)
	}
	if st.BackgroundLeft != 0 {
		t.Errorf("background left = %d, want 0", st.BackgroundLeft)
	}

	if err := l.Release(ctx, "job-2"); err != nil {
		t.Fatal(err)
	}
	st, err = l.Status(ctx, w, s)
	if err != nil {
		t.Fatal(err)
	}
	if st.TokensCommitted != 650 {
		t.Errorf("committed after release = %d, want 650", st.TokensCommitted)
	}
	if st.InteractiveLeft != 350 {
		t.Errorf("interactive left after release = %d, want 350", st.InteractiveLeft)
	}
}

func TestReserveIsIdempotent(t *testing.T) {
	ctx := context.Background()
	l := testLedger(t)
	s := budget.Settings{Window: 24 * time.Hour, Tokens: 1000, ReservePct: 40, MaxGrant: 300}
	w := budget.Window{Start: time.Date(2026, 10, 29, 18, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC)}

	first, ok, err := l.Reserve(ctx, w, s, "job", budget.Interactive, 300, 0)
	if err != nil || !ok {
		t.Fatalf("first reserve: ok=%v err=%v", ok, err)
	}
	second, ok, err := l.Reserve(ctx, w, s, "job", budget.Interactive, 300, 0)
	if err != nil || !ok {
		t.Fatalf("second reserve: ok=%v err=%v", ok, err)
	}
	if second.Tokens != first.Tokens {
		t.Errorf("second grant = %d, want %d", second.Tokens, first.Tokens)
	}
	st, _ := l.Status(ctx, w, s)
	if st.TokensCommitted != 300 {
		t.Errorf("committed = %d, want 300 (not double-charged)", st.TokensCommitted)
	}
}

func TestDeferredJobKeepsPartialSpend(t *testing.T) {
	ctx := context.Background()
	l := testLedger(t)
	s := budget.Settings{Window: 24 * time.Hour, Tokens: 1000, ReservePct: 40, MaxGrant: 300}
	w := budget.Window{Start: time.Date(2026, 10, 29, 18, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC)}

	if _, ok, err := l.Reserve(ctx, w, s, "job", budget.Interactive, 300, 0); err != nil || !ok {
		t.Fatalf("reserve: ok=%v err=%v", ok, err)
	}
	if _, err := l.Spend(ctx, "job", 120, 0); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(ctx, "job"); err != nil {
		t.Fatal(err)
	}
	// Re-reserve in the same window after a deferral.
	if _, ok, err := l.Reserve(ctx, w, s, "job", budget.Interactive, 300, 0); err != nil || !ok {
		t.Fatalf("re-reserve: ok=%v err=%v", ok, err)
	}
	st, _ := l.Status(ctx, w, s)
	if st.TokensCommitted != 420 {
		t.Errorf("committed = %d, want 420 (120 already spent + 300 re-reserved)", st.TokensCommitted)
	}
	remaining, err := l.Spend(ctx, "job", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if remaining != 290 {
		t.Errorf("remaining after spend = %d, want 290", remaining)
	}
}

func TestDisabledLedgerGrantsEverything(t *testing.T) {
	l := budget.New(nil)
	s := budget.Settings{}
	if s.Enabled() {
		t.Fatal("zero settings should be disabled")
	}
	if _, ok, err := l.Reserve(context.Background(), budget.Window{}, s, "job", budget.Interactive, 1, 0); err != nil || !ok {
		t.Fatalf("disabled ledger = ok=%v err=%v", ok, err)
	}
}
