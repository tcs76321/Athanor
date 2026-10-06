package alarms

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "athanor.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

type fakeFreezer struct {
	frozen  bool
	freezes int
}

func (f *fakeFreezer) Frozen() bool { return f.frozen }
func (f *fakeFreezer) Freeze(context.Context) error {
	f.frozen = true
	f.freezes++
	return nil
}

func TestValidCategoryAndLevel(t *testing.T) {
	for _, c := range []Category{CategoryLoop, CategoryResource, CategorySecurity, CategoryQuality,
		CategoryStuck, CategoryHallucination, CategoryBudget, CategorySelfModification, CategoryDrift} {
		if !ValidCategory(c) {
			t.Errorf("ValidCategory(%q) = false", c)
		}
	}
	if ValidCategory("bogus") || ValidLevel("bogus") {
		t.Error("unknown category/level accepted")
	}
	if DefaultLevel(CategorySecurity) != LevelCritical || DefaultLevel(CategoryQuality) != LevelWarning {
		t.Error("default levels wrong")
	}
}

func TestRaisePersistsAndDedups(t *testing.T) {
	svc := NewService(testStore(t), nil)
	ctx := context.Background()
	first, err := svc.Raise(ctx, Alarm{Category: CategoryLoop, Message: "same call repeated", JobID: "j1"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Level != LevelAlert {
		t.Fatalf("default level = %s, want alert", first.Level)
	}
	second, err := svc.Raise(ctx, Alarm{Category: CategoryLoop, Message: "same call repeated", JobID: "j1"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("duplicate alarm created a new row: %s != %s", second.ID, first.ID)
	}
	active, err := svc.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 {
		t.Fatalf("active alarms = %d, want 1", len(active))
	}
}

func TestRaiseCriticalFreezes(t *testing.T) {
	frz := &fakeFreezer{}
	svc := NewService(testStore(t), frz)
	ctx := context.Background()
	if _, err := svc.Raise(ctx, Alarm{Category: CategorySecurity, Message: "prompt injection detected"}); err != nil {
		t.Fatal(err)
	}
	if !frz.frozen || frz.freezes != 1 {
		t.Fatalf("critical alarm did not freeze exactly once: %+v", frz)
	}
}

func TestResolve(t *testing.T) {
	svc := NewService(testStore(t), nil)
	ctx := context.Background()
	a, err := svc.Raise(ctx, Alarm{Category: CategoryBudget, Message: "over budget"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Resolve(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	active, err := svc.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("active alarms after resolve = %d, want 0", len(active))
	}
	if err := svc.Resolve(ctx, "does-not-exist"); err == nil {
		t.Fatal("resolving an unknown id should error")
	}
}

func TestActiveOrdersBySeverity(t *testing.T) {
	svc := NewService(testStore(t), nil)
	ctx := context.Background()
	for _, c := range []Category{CategoryQuality, CategorySecurity, CategoryStuck} {
		if _, err := svc.Raise(ctx, Alarm{Category: c, Message: "x:" + string(c)}); err != nil {
			t.Fatal(err)
		}
	}
	active, err := svc.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 3 || active[0].Category != CategorySecurity {
		t.Fatalf("active order = %+v, want security first", active)
	}
}

func TestDetectEveryCategory(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	th := DefaultThresholds()

	cases := []struct {
		name string
		snap Snapshot
		want Category
	}{
		{
			"quality",
			Snapshot{RecentResults: []string{"rejected", "failed", "rejected", "cancelled", "rejected",
				"accepted_new", "rejected", "failed", "rejected", "rejected"}},
			CategoryQuality,
		},
		{
			"stuck",
			Snapshot{ActiveJobs: []JobActivity{{ID: "j1", ProjectID: "p1", State: "evaluating",
				LastProgress: now.Add(-time.Hour)}}},
			CategoryStuck,
		},
		{
			"loop",
			Snapshot{LoopRepeats: map[string]int{"j1|execute_code|{}": 6}},
			CategoryLoop,
		},
		{
			"budget",
			Snapshot{JobTokens: map[string]int{"j1": 999_999}},
			CategoryBudget,
		},
		{
			"resource",
			Snapshot{MemoryPressure: 0.99},
			CategoryResource,
		},
		{
			"security",
			Snapshot{SecurityEvents: 2},
			CategorySecurity,
		},
		{
			"hallucination",
			Snapshot{EventFlags: map[string]int{"hallucinated_path": 1}},
			CategoryHallucination,
		},
		{
			"self_modification",
			Snapshot{EventFlags: map[string]int{"self_modification_attempt": 1}},
			CategorySelfModification,
		},
		{
			"drift",
			Snapshot{EventFlags: map[string]int{"drift_attempt": 1}},
			CategoryDrift,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Detect(tc.snap, th, now)
			found := false
			for _, a := range got {
				if a.Category == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("Detect(%s) = %+v, want a %s alarm", tc.name, got, tc.want)
			}
		})
	}
}

func TestDetectQuietOnHealthySnapshot(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	healthy := Snapshot{
		RecentResults: []string{"accepted_new", "accepted_previous"},
		ActiveJobs:    []JobActivity{{ID: "j1", LastProgress: now}},
	}
	if got := Detect(healthy, DefaultThresholds(), now); len(got) != 0 {
		t.Fatalf("healthy snapshot produced alarms: %+v", got)
	}
}

type fixedLoader struct{ snap Snapshot }

func (f fixedLoader) Load(context.Context, time.Time) (Snapshot, error) { return f.snap, nil }

func TestMonitorRaisesFromLoader(t *testing.T) {
	svc := NewService(testStore(t), nil)
	loader := fixedLoader{snap: Snapshot{EventFlags: map[string]int{"self_modification_attempt": 1}}}
	m := NewMonitor(svc, loader, DefaultThresholds(), time.Minute, nil)
	m.Tick(context.Background())
	active, err := svc.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].Category != CategorySelfModification {
		t.Fatalf("monitor active = %+v, want one self_modification alarm", active)
	}
}

func TestStoreLoaderReadsEventFlags(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if _, err := st.AppendEvent(ctx, store.Event{Category: "jobs",
		Data: map[string]any{"event": "hallucinated_path", "path": "nope.go"}}); err != nil {
		t.Fatal(err)
	}
	snap, err := NewStoreLoader(st, DefaultThresholds()).Load(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if snap.EventFlags["hallucinated_path"] != 1 {
		t.Fatalf("EventFlags = %+v, want one hallucinated_path", snap.EventFlags)
	}
}
