package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/control"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/power"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

// stepOnce performs exactly one phase step (test helper for driving a job
// to a chosen crash point).
func stepOnce(e *Engine, jobID string) error {
	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		return err
	}
	return e.step(context.Background(), j)
}

// TestRecoverResumesMidFlightJob simulates a daemon crash mid-job (the
// engine disappears; only the store survives) and proves the job resumes
// from its last committed phase and completes, re-reading the persisted
// proposal artifact instead of relying on memory.
func TestRecoverResumesMidFlightJob(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(db.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Default()
	ollama := newCountingOllama(t)
	cfg.Inference.OllamaURL = ollama.URL
	freezer, _ := control.NewKillSwitch(db)
	projects := project.NewRepo(db)
	jobs := job.NewRepository(db)
	artifacts := artifact.NewStore(db, filepath.Join(dir, "artifacts"))
	registry, _ := llm.NewRegistry(cfg.Personas)

	_, task, err := projects.Create(context.Background(), "demo", "text",
		"Write a short essay about local-first software.", nil)
	if err != nil {
		t.Fatal(err)
	}
	j, err := jobs.Create(context.Background(), task.ID, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	// Run up to synthesizing, then "crash" (drop the engine, keep state).
	eng1 := New(cfg, db, jobs, projects, artifacts, evaluation.NewRepo(db), llm.NewClient(cfg.Inference.OllamaURL, nil), registry, freezer, power.NewPowerManager(nil), newFakeRunner(), nil, nil)
	for {
		cur, err := jobs.Get(context.Background(), j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if cur.State == job.StateSynthesizing {
			break
		}
		if cur.State.Terminal() {
			t.Fatalf("job reached %s before crash point", cur.State)
		}
		if err := stepOnce(eng1, j.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Restart: recovery marks the job interrupted and resumes it.
	db2, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	jobs2 := job.NewRepository(db2)
	artifacts2 := artifact.NewStore(db2, filepath.Join(dir, "artifacts"))
	eng2 := New(cfg, db2, jobs2, project.NewRepo(db2), artifacts2,
		evaluation.NewRepo(db2),
		llm.NewClient(cfg.Inference.OllamaURL, nil), registry, freezer, power.NewPowerManager(nil), newFakeRunner(), nil, nil)
	eng2.Recover(context.Background())

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		final, err := jobs2.Get(context.Background(), j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if final.State == job.StateCompleted {
			if final.RecoveryFlag != "" {
				t.Errorf("recovery flag not cleared after successful resume: %q", final.RecoveryFlag)
			}
			if _, err := artifacts2.LatestForJob(context.Background(), j.ID, artifact.KindDocument); err != nil {
				t.Errorf("final artifact missing after resume: %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job never completed after recovery")
}

// TestRecoverResumesJob_MidDiverging (M3-T6 commit 6.1)
// simulates a crash in StateDiverging. Each divergence
// phase call is atomic (one phase call produces N
// candidates and transitions in one step), so the
// "mid-phase" test is necessarily "phase entered but
// not yet completed" — the job sits in StateDiverging
// with zero candidate artifacts. Recovery must resume
// the job and complete the divergence phase.
func TestRecoverResumesJob_MidDiverging(t *testing.T) {
	env := newEnv(t)
	jobID := env.submit(t)

	// Drive to StateDiverging. The next stepOnce would
	// complete the divergence phase and transition, so we
	// crash here, before that step.
	for {
		cur, err := env.jobs.Get(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if cur.State == job.StateDiverging {
			break
		}
		if cur.State.Terminal() {
			t.Fatalf("job reached %s before diverging", cur.State)
		}
		if err := stepOnce(env.eng, jobID); err != nil {
			t.Fatal(err)
		}
	}
	cur, err := env.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.State != job.StateDiverging {
		t.Fatalf("crash point state = %s, want diverging", cur.State)
	}

	if err := env.db.Close(); err != nil {
		t.Fatal(err)
	}
	db2, err := store.Open(env.db.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	jobs2 := job.NewRepository(db2)
	artifacts2 := artifact.NewStore(db2, filepath.Join(filepath.Dir(env.db.Path()), "artifacts"))
	freezer2, _ := control.NewKillSwitch(db2)
	registry, _ := llm.NewRegistry(env.cfg.Personas)
	eng2 := New(env.cfg, db2, jobs2, project.NewRepo(db2), artifacts2,
		evaluation.NewRepo(db2),
		llm.NewClient(env.cfg.Inference.OllamaURL, nil), registry, freezer2, power.NewPowerManager(nil), newFakeRunner(), nil, nil)
	eng2.Recover(context.Background())

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		final, err := jobs2.Get(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if final.State == job.StateCompleted {
			proposals, err := artifacts2.ListByProject(context.Background(), cur.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, p := range proposals {
				if p.JobID == jobID && p.Kind == artifact.KindProposal {
					count++
				}
			}
			if count < 1 {
				t.Errorf("proposal artifacts for job = %d, want ≥ 1", count)
			}
			return
		}
		if final.State == job.StateFailed {
			t.Fatalf("job failed after recovery, want completed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job never completed after mid-diverging recovery")
}

// TestRecoverResumesJob_MidEvaluating (M3-T6 commit 6.1)
// simulates a crash in StateEvaluating. Each evaluating
// phase call is atomic (one phase call evaluates all
// candidates and transitions), so the "mid-phase" test
// is "phase entered but not yet completed" — the job
// sits in StateEvaluating with zero evaluation records.
// Recovery must resume the job and complete the
// evaluating phase.
func TestRecoverResumesJob_MidEvaluating(t *testing.T) {
	env := newEnv(t)
	jobID := env.submit(t)

	// Drive to StateEvaluating. The next stepOnce would
	// complete the evaluating phase and transition, so we
	// crash here, before that step.
	for {
		cur, err := env.jobs.Get(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if cur.State == job.StateEvaluating {
			break
		}
		if cur.State.Terminal() {
			t.Fatalf("job reached %s before evaluating", cur.State)
		}
		if err := stepOnce(env.eng, jobID); err != nil {
			t.Fatal(err)
		}
	}

	if err := env.db.Close(); err != nil {
		t.Fatal(err)
	}
	db2, err := store.Open(env.db.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	jobs2 := job.NewRepository(db2)
	artifacts2 := artifact.NewStore(db2, filepath.Join(filepath.Dir(env.db.Path()), "artifacts"))
	eval2 := evaluation.NewRepo(db2)
	freezer2, _ := control.NewKillSwitch(db2)
	registry, _ := llm.NewRegistry(env.cfg.Personas)
	eng2 := New(env.cfg, db2, jobs2, project.NewRepo(db2), artifacts2, eval2,
		llm.NewClient(env.cfg.Inference.OllamaURL, nil), registry, freezer2, power.NewPowerManager(nil), newFakeRunner(), nil, nil)
	eng2.Recover(context.Background())

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		final, err := jobs2.Get(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if final.State == job.StateCompleted {
			return
		}
		if final.State == job.StateFailed {
			t.Fatalf("job failed after mid-evaluating recovery, want completed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job never completed after mid-evaluating recovery")
}

// TestRecoverResumesJob_ArtifactWrittenBeforeTransition (M1-T8.3) covers
// the narrow atomicity window in phaseSynthesize: the final-kind
// artifact row and content file exist on disk, but the state
// transition to StateComparing has not yet been committed. Recovery
// must resume the job in StateSynthesizing, version the existing
// artifact (not create a duplicate), and complete.
func TestRecoverResumesJob_ArtifactWrittenBeforeTransition(t *testing.T) {
	env := newEnv(t)
	jobID := env.submit(t)

	// Drive to synthesizing via the normal phase chain (this also
	// writes the proposal artifact at the end of diverging).
	for {
		cur, err := env.jobs.Get(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if cur.State == job.StateSynthesizing {
			break
		}
		if cur.State.Terminal() {
			t.Fatalf("job reached %s before crash point", cur.State)
		}
		if err := stepOnce(env.eng, jobID); err != nil {
			t.Fatal(err)
		}
	}

	// Simulate the atomicity window: write the final-kind artifact
	// (mimicking what phaseSynthesize does before Transition), but
	// leave the job in StateSynthesizing.
	cur, err := env.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.artifacts.CreateDraftFor(context.Background(),
		cur.ProjectID, "", jobID, artifact.KindDocument,
		[]byte("half-written final draft (pre-crash)"),
	); err != nil {
		t.Fatal(err)
	}

	// Crash + recover.
	if err := env.db.Close(); err != nil {
		t.Fatal(err)
	}
	db2, err := store.Open(env.db.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	jobs2 := job.NewRepository(db2)
	artifacts2 := artifact.NewStore(db2, filepath.Join(filepath.Dir(env.db.Path()), "artifacts"))
	freezer2, _ := control.NewKillSwitch(db2)
	registry, _ := llm.NewRegistry(env.cfg.Personas)
	eng2 := New(env.cfg, db2, jobs2, project.NewRepo(db2), artifacts2,
		evaluation.NewRepo(db2),
		llm.NewClient(env.cfg.Inference.OllamaURL, nil), registry, freezer2, power.NewPowerManager(nil), newFakeRunner(), nil, nil)
	eng2.Recover(context.Background())

	// Wait for completion. Recovery must version the existing final
	// artifact (v1 superseded + v2 draft = 2 rows) rather than
	// create a fresh v1 alongside the pre-crash one (3 rows, with
	// two v1 drafts). ListByJob does not exist in M1; filter
	// through ListByProject.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		final, err := jobs2.Get(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if final.State == job.StateCompleted {
			docs, err := artifacts2.ListByProject(context.Background(), cur.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			versions := map[int]bool{}
			for _, d := range docs {
				if d.JobID == jobID && d.Kind == artifact.KindDocument {
					count++
					versions[d.Version] = true
				}
			}
			if count != 2 {
				t.Errorf("final-kind artifacts for job = %d, want 2 (v1 superseded + v2 draft)", count)
			}
			if versions[1] && versions[2] {
				// correct: recovery versioned, not duplicated
			} else if versions[1] && !versions[2] {
				t.Errorf("final-kind artifacts are only v1; recovery failed to create v2 after pre-existing v1")
			} else if !versions[1] && versions[2] {
				t.Errorf("final-kind artifacts skip v1; recovery created v2 without superseding the pre-crash v1")
			} else {
				t.Errorf("final-kind artifacts have unexpected versions: %v", versions)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job never completed after pre-transition recovery")
}
