package trigger

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func openRepo(t *testing.T) (*store.Store, *Repo) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','demo','document','Write a brief.')`); err != nil {
		t.Fatal(err)
	}
	return s, NewRepo(s)
}

func makeDue(t *testing.T, s *store.Store, id string, at time.Time) {
	t.Helper()
	if _, err := s.DB().Exec(`UPDATE triggers SET next_run_at = ? WHERE id = ?`, at.UTC().Format(timeFormat), id); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureScheduleAndUpdate(t *testing.T) {
	_, r := openRepo(t)
	ctx := context.Background()
	tr, err := r.Ensure(ctx, "p1", "daily-brief", KindSchedule, "0 7 * * *", true)
	if err != nil {
		t.Fatal(err)
	}
	if tr.ID == "" || tr.Kind != KindSchedule || !tr.Enabled {
		t.Fatalf("trigger = %+v", tr)
	}
	next, err := time.Parse(timeFormat, tr.NextRunAt)
	if err != nil {
		t.Fatalf("next_run_at = %q: %v", tr.NextRunAt, err)
	}
	if next.Hour() != 7 || next.Minute() != 0 || !next.After(time.Now().UTC()) {
		t.Errorf("next = %s, want the next 07:00 UTC", tr.NextRunAt)
	}

	again, err := r.Ensure(ctx, "p1", "daily-brief", KindSchedule, "30 8 * * *", false)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != tr.ID || again.Spec != "30 8 * * *" || again.Enabled {
		t.Errorf("update = %+v", again)
	}
	list, err := r.ListByProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("triggers = %d, want 1 (upsert)", len(list))
	}
	if _, err := r.Ensure(ctx, "p1", "daily-brief", KindManual, "", true); err != nil {
		t.Fatal(err)
	}
	list, err = r.ListByProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Errorf("triggers = %d, want 2 (schedule + manual)", len(list))
	}
}

func TestEnsureRejectsBadInput(t *testing.T) {
	_, r := openRepo(t)
	ctx := context.Background()
	cases := []struct {
		name string
		fn   func() error
	}{
		{"empty project", func() error { _, err := r.Ensure(ctx, "", "t", KindManual, "", true); return err }},
		{"unknown kind", func() error { _, err := r.Ensure(ctx, "p1", "t", "cron", "", true); return err }},
		{"bad schedule", func() error { _, err := r.Ensure(ctx, "p1", "t", KindSchedule, "not cron", true); return err }},
		{"impossible", func() error {
			_, err := r.Ensure(ctx, "p1", "t", KindSchedule, "0 0 30 2 *", true)
			return err
		}},
	}
	for _, tc := range cases {
		if err := tc.fn(); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
}

func TestDueAndMarkRan(t *testing.T) {
	s, r := openRepo(t)
	ctx := context.Background()
	tr, err := r.Ensure(ctx, "p1", "daily-brief", KindSchedule, "0 7 * * *", true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 2, 7, 0, 0, 0, time.UTC)
	if due, err := r.Due(ctx, now, 10); err != nil || len(due) != 0 {
		t.Fatalf("due before slot = %v/%v", due, err)
	}
	makeDue(t, s, tr.ID, now)
	due, err := r.Due(ctx, now, 10)
	if err != nil || len(due) != 1 || due[0].ID != tr.ID {
		t.Fatalf("due = %+v err=%v", due, err)
	}
	next, ok := NextRun(tr.Spec, now)
	if !ok {
		t.Fatal("no next slot")
	}
	if err := r.MarkRan(ctx, tr.ID, due[0].NextRunAt, now.Format(timeFormat), next); err != nil {
		t.Fatal(err)
	}
	if due, err := r.Due(ctx, now, 10); err != nil || len(due) != 0 {
		t.Fatalf("due after mark = %v/%v", due, err)
	}
	got, err := r.Get(ctx, tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.IdempotencyKey != tr.ID+"@"+now.Format(timeFormat) || got.LastRunAt == "" {
		t.Errorf("after mark = %+v", got)
	}
	if err := r.MarkRan(ctx, "missing", "slot", now.Format(timeFormat), next); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing mark = %v", err)
	}
}

type fakeLauncher struct {
	calls int
	id    string
	err   error
}

func (f *fakeLauncher) Launch(context.Context, Record) (string, error) {
	f.calls++
	return f.id, f.err
}

func TestRunnerFiresOncePerSlot(t *testing.T) {
	s, r := openRepo(t)
	ctx := context.Background()
	tr, err := r.Ensure(ctx, "p1", "daily-brief", KindSchedule, "* * * * *", true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 2, 7, 30, 0, 0, time.UTC)
	makeDue(t, s, tr.ID, now)
	launch := &fakeLauncher{id: "cycle-1"}
	runner := NewRunner(r, launch)
	runner.now = func() time.Time { return now }

	n, err := runner.Tick(ctx)
	if err != nil || n != 1 || launch.calls != 1 {
		t.Fatalf("first tick = %d/%v calls=%d", n, err, launch.calls)
	}
	if n, err := runner.Tick(ctx); err != nil || n != 0 || launch.calls != 1 {
		t.Fatalf("second tick = %d/%v calls=%d", n, err, launch.calls)
	}
	got, err := r.Get(ctx, tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.NextRunAt != "2026-01-02T07:31:00Z" {
		t.Errorf("next = %q, want 07:31", got.NextRunAt)
	}
}

func TestRunnerAdvancesAfterLaunchFailure(t *testing.T) {
	s, r := openRepo(t)
	ctx := context.Background()
	tr, err := r.Ensure(ctx, "p1", "daily-brief", KindSchedule, "* * * * *", true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 2, 7, 30, 0, 0, time.UTC)
	makeDue(t, s, tr.ID, now)
	launch := &fakeLauncher{err: errors.New("scheduler down")}
	runner := NewRunner(r, launch)
	runner.now = func() time.Time { return now }
	if n, err := runner.Tick(ctx); err != nil || n != 0 {
		t.Fatalf("tick = %d/%v", n, err)
	}
	got, err := r.Get(ctx, tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastRunAt == "" {
		t.Error("failed launch did not claim the slot")
	}
	if due, _ := r.Due(ctx, now, 10); len(due) != 0 {
		t.Error("failed launch left the slot due")
	}
}

func TestSetEnabledRearms(t *testing.T) {
	s, r := openRepo(t)
	ctx := context.Background()
	tr, err := r.Ensure(ctx, "p1", "daily-brief", KindSchedule, "* * * * *", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetEnabled(ctx, tr.ID, false); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.NextRunAt != "" {
		t.Errorf("disabled = %+v", got)
	}
	makeDue(t, s, tr.ID, time.Now().Add(-time.Hour))
	if due, _ := r.Due(ctx, time.Now().UTC(), 10); len(due) != 0 {
		t.Error("disabled trigger is due")
	}
	if err := r.SetEnabled(ctx, tr.ID, true); err != nil {
		t.Fatal(err)
	}
	got, _ = r.Get(ctx, tr.ID)
	if !got.Enabled || got.NextRunAt == "" {
		t.Errorf("re-enabled = %+v", got)
	}
}
