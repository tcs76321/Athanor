package cycle

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func openStore(t *testing.T) (*store.Store, *Repo) {
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
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','demo','document','Write a daily brief.')`); err != nil {
		t.Fatal(err)
	}
	return s, NewRepo(s)
}

func TestCreateAndListTimeline(t *testing.T) {
	_, r := openStore(t)
	ctx := context.Background()
	first, err := r.Create(ctx, "p1", KindDiscovery, "Find a direction.")
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Create(ctx, "p1", KindDesign, "Design the chosen direction.")
	if err != nil {
		t.Fatal(err)
	}
	if first.Ordinal != 0 || second.Ordinal != 1 {
		t.Fatalf("ordinals = %d/%d, want 0/1", first.Ordinal, second.Ordinal)
	}
	if first.State != StateDraft {
		t.Errorf("state = %q, want draft", first.State)
	}
	list, err := r.ListByProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != first.ID || list[1].ID != second.ID {
		t.Fatalf("timeline = %+v", list)
	}
}

func TestSetStateAndGraphRef(t *testing.T) {
	_, r := openStore(t)
	ctx := context.Background()
	c, err := r.Create(ctx, "p1", KindBuild, "Build it.")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetState(ctx, c.ID, StateRunning); err != nil {
		t.Fatal(err)
	}
	if err := r.SetGraphRef(ctx, c.ID, "tpl-1", 3, "trig-1"); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateRunning || got.TemplateID != "tpl-1" || got.TemplateVersion != 3 || got.TriggerID != "trig-1" {
		t.Errorf("cycle = %+v", got)
	}
	if err := r.SetState(ctx, c.ID, "teleported"); err == nil {
		t.Error("unknown state accepted")
	}
	if err := r.SetState(ctx, "missing", StateRunning); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing state = %v, want ErrNotFound", err)
	}
}

func TestTrackGoalMapsTerminalStatus(t *testing.T) {
	_, r := openStore(t)
	ctx := context.Background()
	c, err := r.Create(ctx, "p1", KindDiscovery, "Find a direction.")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.TrackGoal(ctx, c.ID, "decomposed"); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateDraft {
		t.Errorf("non-terminal status changed state to %q", got.State)
	}
	if err := r.TrackGoal(ctx, c.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	got, _ = r.Get(ctx, c.ID)
	if got.State != StateCompleted {
		t.Errorf("state = %q, want completed", got.State)
	}
	if err := r.TrackGoal(ctx, "", "completed"); err != nil {
		t.Errorf("empty cycle: %v", err)
	}
}

func TestCreateRejectsBadInput(t *testing.T) {
	_, r := openStore(t)
	ctx := context.Background()
	if _, err := r.Create(ctx, "", KindBuild, "goal"); err == nil {
		t.Error("empty project accepted")
	}
	if _, err := r.Create(ctx, "p1", "sideways", "goal"); err == nil {
		t.Error("unknown kind accepted")
	}
	if _, err := r.Create(ctx, "p1", KindBuild, ""); err == nil {
		t.Error("empty goal accepted")
	}
}
