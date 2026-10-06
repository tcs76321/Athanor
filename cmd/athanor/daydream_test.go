package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/corrections"
	"github.com/tcs76321/athanor/internal/daydream"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/power"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/strategy"
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

// seedProject inserts a repository-less project row.
func seedProject(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if _, err := st.DB().ExecContext(context.Background(),
		`INSERT INTO projects (id, name, archetype, goal) VALUES (?, ?, 'code', 'build things that last')`,
		id, "proj-"+id); err != nil {
		t.Fatal(err)
	}
}

// TestDaydreamFeedbackReviewProposesGlobal covers the M7-T2 Feedback Review
// action: a rule recurring across two projects becomes one global correction,
// and a second pass does not duplicate it.
func TestDaydreamFeedbackReviewProposesGlobal(t *testing.T) {
	st := migratedStore(t)
	seedProject(t, st, "p1")
	seedProject(t, st, "p2")
	corrRepo := corrections.NewRepo(st)
	ctx := context.Background()
	const rule = "Prefer dependency injection over package-level state."
	for _, pid := range []string{"p1", "p2"} {
		if _, err := corrRepo.Capture(ctx, corrections.CaptureInput{
			Source: corrections.SourceUserRejection, ProjectID: pid, Scope: corrections.ScopeProject,
			Category: corrections.CategoryArchitecture, Severity: corrections.SeverityHigh,
			UserFeedback: "no package-level state", DerivedRule: rule,
		}); err != nil {
			t.Fatal(err)
		}
	}
	r := newDaydreamRunner(t, st, &fakeDaydreamCompactor{}, true, false)
	r.deps.corrections = corrRepo

	countGlobal := func() int {
		recs, err := corrRepo.Active(ctx)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, rec := range recs {
			if rec.Scope == corrections.ScopeGlobal && rec.DerivedRule == rule {
				n++
			}
		}
		return n
	}

	if err := r.pass(ctx); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if got := countGlobal(); got != 1 {
		t.Fatalf("global corrections = %d, want 1", got)
	}
	if err := r.pass(ctx); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if got := countGlobal(); got != 1 {
		t.Fatalf("second pass duplicated the global correction: %d", got)
	}
}

// fakeGenerator is a cmd-local daydream generator.
type fakeGenerator struct {
	content string
	err     error
	calls   int
}

func (f *fakeGenerator) Generate(context.Context, string) (string, error) {
	f.calls++
	return f.content, f.err
}

// TestDaydreamProactiveDocumentation covers the M7-T2 Proactive Documentation
// action: a repository-backed project with no document artifact gets one draft
// README, and a second pass is idempotent.
func TestDaydreamProactiveDocumentation(t *testing.T) {
	st := migratedStore(t)
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(context.Background(),
		`INSERT INTO projects (id, name, archetype, goal, repository_path)
		 VALUES ('p1','proj','code','build things that last',?)`, repoDir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r := newDaydreamRunner(t, st, &fakeDaydreamCompactor{}, true, false)
	r.deps.projects = project.NewRepo(st)
	r.deps.artifacts = artifact.NewStore(st, t.TempDir())
	gen := &fakeGenerator{content: "# Proj\n\nA readme.\n"}
	r.deps.generator = gen
	r.deps.logs = daydream.NewRepo(st)

	if err := r.pass(ctx); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if gen.calls != 1 {
		t.Fatalf("generator calls = %d, want 1", gen.calls)
	}
	arts, err := r.deps.artifacts.ListByProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	var docs int
	for _, a := range arts {
		if a.Kind == artifact.KindDocument && a.Status == artifact.StatusDraft {
			docs++
		}
	}
	if docs != 1 {
		t.Fatalf("draft document artifacts = %d, want 1", docs)
	}
	logs, err := r.deps.logs.Recent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range logs {
		if l.Action == daydream.ActionProactiveDocumentation && len(l.ArtifactsProduced) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("no proactive_documentation DaydreamLog: %+v", logs)
	}

	// Idempotent: the document now exists, so no second generation.
	if err := r.pass(ctx); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if gen.calls != 1 {
		t.Fatalf("second pass regenerated: calls = %d", gen.calls)
	}
}

// TestDaydreamStrategyMining covers the M7-T2 Strategy Mining action: seeded
// outcomes crossing the cohort floor produce proposed insights.
func TestDaydreamStrategyMining(t *testing.T) {
	st := migratedStore(t)
	stratRepo := strategy.NewRepo(st)
	ctx := context.Background()
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO projects (id, name, archetype, goal) VALUES ('p1','proj','code','build things that last')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO tasks (id, project_id, title) VALUES ('t1','p1','do work')`); err != nil {
		t.Fatal(err)
	}
	for _, j := range []string{"j1", "j2", "j3", "j4"} {
		if _, err := st.DB().ExecContext(ctx,
			`INSERT INTO jobs (id, task_id, project_id, state) VALUES (?, 't1','p1','completed')`, j); err != nil {
			t.Fatal(err)
		}
	}
	seed := func(jobID, persona string, accepted bool) {
		t.Helper()
		if _, err := stratRepo.CreateProfile(ctx, strategy.Profile{
			JobID: jobID, ProjectID: "p1", Archetype: "code",
			Signature: []strategy.SignatureEntry{{Phase: "diverging", Persona: persona, Candidates: 3}},
		}); err != nil {
			t.Fatal(err)
		}
		result := strategy.ResultRejected
		conf := 0.2
		if accepted {
			result = strategy.ResultAcceptedNew
			conf = 0.9
		}
		if _, err := stratRepo.CreateOutcome(ctx, strategy.Outcome{
			JobID: jobID, Result: result, Score: 0.5, EvaluatorConfidence: conf,
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed("j1", "alternative", true)
	seed("j2", "alternative", true)
	seed("j3", "main", false)
	seed("j4", "main", false)

	r := newDaydreamRunner(t, st, &fakeDaydreamCompactor{}, true, false)
	r.deps.strategy = stratRepo
	r.deps.cfg.StrategyAnalysis = config.StrategyAnalysis{
		Enabled: boolPtr(true), MinCohortSize: 2, MinAcceptRateDelta: 0.15,
	}
	r.deps.logs = daydream.NewRepo(st)

	if err := r.pass(ctx); err != nil {
		t.Fatalf("pass: %v", err)
	}
	insights, err := stratRepo.ListInsights(ctx, strategy.InsightProposed)
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) == 0 {
		t.Fatal("strategy mining proposed no insights from a clear cohort")
	}
}

// TestDaydreamLogPersistedForConsolidation covers the §17.3 DaydreamLog for
// the M5-T6 action.
func TestDaydreamLogPersistedForConsolidation(t *testing.T) {
	st := migratedStore(t)
	seedJob(t, st, "completed")
	r := newDaydreamRunner(t, st, &fakeDaydreamCompactor{}, true, false)
	r.deps.logs = daydream.NewRepo(st)
	if err := r.pass(context.Background()); err != nil {
		t.Fatalf("pass: %v", err)
	}
	logs, err := r.deps.logs.Recent(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Action != daydream.ActionMemoryConsolidation {
		t.Fatalf("logs = %+v, want one memory_consolidation row", logs)
	}
}
