package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/power"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

// fakeDaydreamCompactor is a cmd-local Compactor that counts calls.
type fakeDaydreamCompactor struct{ calls int }

func (f *fakeDaydreamCompactor) Compact(context.Context, mce.MemoryItem, mce.CompactionKind) (string, error) {
	f.calls++
	return "memo", nil
}

func (f *fakeDaydreamCompactor) TemplateVersion(mce.CompactionKind) string { return "test-v1" }

type fakePower struct{ allow bool }

func (f fakePower) GetLimits() power.Limits { return power.Limits{AllowDaydreaming: f.allow} }

type fakeFreezer struct{ frozen bool }

func (f fakeFreezer) Frozen() bool { return f.frozen }

// migratedStore opens and migrates a temporary database.
func migratedStore(t *testing.T) *store.Store {
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

// seedJob inserts a project/task/job and one event, returning the job id.
func seedJob(t *testing.T, st *store.Store, state string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','proj','code','build things that last')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO tasks (id, project_id, title) VALUES ('t1','p1','do work')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO jobs (id, task_id, project_id, state) VALUES ('j1','t1','p1',?)`, state); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendEvent(ctx, store.Event{Category: "jobs", JobID: "j1", ProjectID: "p1",
		Data: map[string]any{"event": "completed", "detail": "exit code 0"}}); err != nil {
		t.Fatal(err)
	}
	return "j1"
}

func countCategory(t *testing.T, st *store.Store, category string) int {
	t.Helper()
	evs, err := st.QueryEvents(context.Background(), store.EventFilter{Category: category})
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	return len(evs)
}

func newDaydreamRunner(t *testing.T, st *store.Store, comp mce.Compactor, allow, frozen bool) *daydreamRunner {
	t.Helper()
	cfg := &config.Config{Power: config.Power{
		DaydreamOnIdle:             boolPtr(true),
		DaydreamMaxWallTimeMinutes: 1,
		IdleResumeAfter:            config.Duration(time.Hour),
	}}
	return &daydreamRunner{deps: daydreamDeps{
		cfg:         cfg,
		jobs:        job.NewRepository(st),
		events:      st,
		artifacts:   artifact.NewStore(st, t.TempDir()),
		compactions: mce.NewCompactStore(st),
		compactor:   comp,
		power:       fakePower{allow: allow},
		freezer:     fakeFreezer{frozen: frozen},
	}}
}

// TestDaydreamConsolidatesIdleTerminalJob is the M5-T6 driver acceptance: on an
// idle daemon with daydreaming allowed, a pass consolidates a terminal job's
// event log and audits it; a second pass is a no-op.
func TestDaydreamConsolidatesIdleTerminalJob(t *testing.T) {
	st := migratedStore(t)
	seedJob(t, st, "completed")
	comp := &fakeDaydreamCompactor{}
	r := newDaydreamRunner(t, st, comp, true, false)

	if err := r.pass(context.Background()); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if comp.calls != 1 {
		t.Fatalf("compactor calls = %d, want 1", comp.calls)
	}
	if got := countCategory(t, st, "daydream"); got != 1 {
		t.Fatalf("daydream events = %d, want 1", got)
	}

	// The job is now consolidated: a second pass does no work and no audit.
	if err := r.pass(context.Background()); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if comp.calls != 1 {
		t.Fatalf("second pass re-compacted: calls = %d", comp.calls)
	}
	if got := countCategory(t, st, "daydream"); got != 1 {
		t.Fatalf("daydream events after second pass = %d, want 1", got)
	}
}

// TestDaydreamGatesBlock pins each of the four gates: config, power profile,
// kill switch.
func TestDaydreamGatesBlock(t *testing.T) {
	cases := []struct {
		name              string
		cfgOn, allow, frz bool
	}{
		{"config off", false, true, false},
		{"profile disallows", true, false, false},
		{"frozen", true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := migratedStore(t)
			seedJob(t, st, "completed")
			comp := &fakeDaydreamCompactor{}
			r := newDaydreamRunner(t, st, comp, tc.allow, tc.frz)
			if !tc.cfgOn {
				r.deps.cfg.Power.DaydreamOnIdle = boolPtr(false)
			}
			if err := r.pass(context.Background()); err != nil {
				t.Fatalf("pass: %v", err)
			}
			if comp.calls != 0 {
				t.Fatalf("a gate let a compaction through: calls = %d", comp.calls)
			}
			if got := countCategory(t, st, "daydream"); got != 0 {
				t.Fatalf("daydream events = %d, want 0", got)
			}
		})
	}
}

// TestDaydreamYieldsToActiveJobs pins §17.2: a queued (non-terminal) job means
// real work, so the loop yields.
func TestDaydreamYieldsToActiveJobs(t *testing.T) {
	st := migratedStore(t)
	seedJob(t, st, "queued") // non-terminal
	comp := &fakeDaydreamCompactor{}
	r := newDaydreamRunner(t, st, comp, true, false)

	if err := r.pass(context.Background()); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if comp.calls != 0 {
		t.Fatalf("daydreamed while a job was active: calls = %d", comp.calls)
	}
}

// TestDaydreamRunnerStopsOnClose pins the loop lifecycle: Close stops the
// goroutine promptly.
func TestDaydreamRunnerStopsOnClose(t *testing.T) {
	st := migratedStore(t)
	r := startDaydream(context.Background(), daydreamDeps{
		cfg: &config.Config{Power: config.Power{
			DaydreamOnIdle:  boolPtr(true),
			IdleResumeAfter: config.Duration(time.Hour),
		}},
		jobs:        job.NewRepository(st),
		events:      st,
		artifacts:   artifact.NewStore(st, t.TempDir()),
		compactions: mce.NewCompactStore(st),
		compactor:   &fakeDaydreamCompactor{},
		power:       fakePower{allow: true},
		freezer:     fakeFreezer{},
	})
	r.Close()
}

// fakeDaydreamIndexer records each IndexOnce call.
type fakeDaydreamIndexer struct {
	calls   int
	gotPath []string
	res     mce.IndexResult
	err     error
}

func (f *fakeDaydreamIndexer) IndexOnce(_ context.Context, projectID, path string) (mce.IndexResult, error) {
	f.calls++
	f.gotPath = append(f.gotPath, projectID+":"+path)
	return f.res, f.err
}

// TestDaydreamExploresRepositories is the M5-T8 driver acceptance: on an idle
// daemon, a pass runs one bounded indexing pass for each project with a
// repository and audits a repository_exploration event.
func TestDaydreamExploresRepositories(t *testing.T) {
	st := migratedStore(t)
	if _, err := st.DB().ExecContext(context.Background(),
		`INSERT INTO projects (id, name, archetype, goal, repository_path)
		 VALUES ('p1','proj','code','build things that last','/tmp/repo')`); err != nil {
		t.Fatal(err)
	}
	idx := &fakeDaydreamIndexer{res: mce.IndexResult{Indexed: 2, Chunks: 5, Embedded: 5}}
	r := newDaydreamRunner(t, st, &fakeDaydreamCompactor{}, true, false)
	r.deps.projects = project.NewRepo(st)
	r.deps.indexer = idx

	if err := r.pass(context.Background()); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if idx.calls != 1 {
		t.Fatalf("IndexOnce calls = %d, want 1", idx.calls)
	}
	if len(idx.gotPath) != 1 || idx.gotPath[0] != "p1:/tmp/repo" {
		t.Fatalf("IndexOnce args = %v, want [p1:/tmp/repo]", idx.gotPath)
	}
	evs, err := st.QueryEvents(context.Background(), store.EventFilter{Category: "daydream"})
	if err != nil {
		t.Fatal(err)
	}
	var exploration int
	for _, e := range evs {
		if strings.Contains(e.DataJSON, "repository_exploration") {
			exploration++
		}
	}
	if exploration != 1 {
		t.Fatalf("repository_exploration events = %d, want 1", exploration)
	}
}

// TestDaydreamSkipsExplorationWithoutIndexer pins the nil-safe default: the
// M5-T6 runner (no indexer wired) does not panic and audits nothing extra.
func TestDaydreamSkipsExplorationWithoutIndexer(t *testing.T) {
	st := migratedStore(t)
	if _, err := st.DB().ExecContext(context.Background(),
		`INSERT INTO projects (id, name, archetype, goal, repository_path)
		 VALUES ('p1','proj','code','build things that last','/tmp/repo')`); err != nil {
		t.Fatal(err)
	}
	r := newDaydreamRunner(t, st, &fakeDaydreamCompactor{}, true, false)
	if err := r.pass(context.Background()); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if got := countCategory(t, st, "daydream"); got != 0 {
		t.Fatalf("daydream events = %d, want 0", got)
	}
}
