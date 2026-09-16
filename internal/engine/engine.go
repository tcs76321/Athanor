// Package engine drives jobs through the full §8.2 state machine:
// queued → context_building → planning → diverging (N candidates,
// §13.1) → evaluating (security persona, temp 0.0, EvaluationRecord
// persistence) → reflecting (only on no-pass, budgeted retry loop)
// → synthesizing (consumes passing candidates) → comparing
// (§19.3 deterministic rule) → completed/failed.
//
// M3-T1 is the milestone that completed §8.2 exactly. Pre-M3
// simplifications (M1 walking skeleton: single-candidate divergence,
// no evaluation phase, M1 "winner=new by default" comparison) are
// gone. What survives:
//   - Context is still assembled naively (prompt v1) — the MCE
//     arrives in M5. The §12.6 floor rule still applies, so a
//     context-shortage pause-and-recommend still happens here.
//   - The ExplorationPath seam in `llm.ResolveTemperature` is wired
//     but the engine always passes `nil` today (the path table and
//     stage resolution land later per the ROADMAP backlog).
//   - The M2-T4 code-archetype sub-steps (`runCodeInPod`,
//     `runTestsInPod`) run inside the synthesizing phase, with
//     `running_tests` recorded as an event-row substate (not a
//     column) per §8.1's "tracked sub-state" note.
//   - Per-phase audits still flow into the append-only events log.
//   - The §19.3 comparison rule is enforced as a guard on the
//     security-persona verdict, not a free-form "LLM said new wins":
//     if no EvaluationRecord has `better_than_previous: true` and
//     `confidence > min_judge_confidence`, the verdict is downgraded
//     to `previous` (or `none` when no prior exists).
//
// Crash safety (§8.2): state is committed after every transition, so a
// killed run resumes from its last committed phase via Recover. The
// EvaluationRecord table is append-only; recovery resumes into
// `evaluating` and the engine re-emits records (or reads existing
// ones via `ListByJob`).
package engine

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// Freezer is the kill-switch surface the engine consults (§22.1: frozen
// means no work proceeds).
type Freezer interface {
	Frozen() bool
}

// ConcurrencyCap is the surface the engine reads on every Enqueue to
// learn how many jobs may run in parallel (ROADMAP §24: the power
// profile throttles background work; ARCHITECTURE §24: autonomous vs
// interactive profiles). Satisfied by *power.PowerManager. The cap is
// read live, not cached at construction time, so a profile change
// takes effect on the next enqueue.
type ConcurrencyCap interface {
	MaxConcurrentJobs() int
}

// ToolRunner is the engine's window onto the internal API
// (ROADMAP M2-T4, ADR-0009). The production impl is
// internal/internalapi/runner.HTTPClient; tests pass a fake. The
// interface is the structural seam that lets M3-T2 drop in
// EvaluationRecord capture without changing call sites.
//
// A nil ToolRunner is a valid configuration: the engine
// short-circuits the code-archetype sub-steps without making any
// HTTP call, and the job completes via the M1 walking skeleton.
// This is what unit tests use to keep the M1 path runnable
// without a Job Pod manager.
type ToolRunner interface {
	// RunCode executes language+code inside the job's pod and
	// returns the result. The implementation is responsible
	// for auth (bearer token), timeout enforcement, and the
	// per-job envelope check (this method is only called for
	// tools the envelope allows).
	RunCode(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error)
	// RunTests runs the test command in the job's pod. Same
	// contract as RunCode.
	RunTests(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error)
	// FetchURL fetches one task-declared source through the
	// §21.5 gateway (M4-T7, ADR-0019 §7). The route is
	// envelope-gated server-side; a 403 surfaces as
	// toolenvelope.ErrToolDisallowed and the research sub-step
	// soft-fails that source.
	FetchURL(ctx context.Context, jobID string, req toolenvelope.FetchURLRequest) (toolenvelope.FetchURLResponse, error)
}

// ErrPaused reports that the job was paused instead of failed — the
// context floor was violated (recommend-or-escalate, §12.3) or the kill
// switch froze the daemon mid-run.
var ErrPaused = errors.New("job paused")

// Engine executes jobs.
type Engine struct {
	cfg       *config.Config
	db        *store.Store
	jobs      *job.Repository
	projects  *project.Repo
	artifacts *artifact.Store
	eval      *evaluation.Repo
	client    *llm.Client
	registry  *llm.Registry
	freezer   Freezer
	cap       ConcurrencyCap
	// runner is the engine's window onto the Job Pod internal
	// API (M2-T4). nil means "no Job Pod manager"; the code
	// archetype's sub-steps short-circuit without HTTP calls.
	// Production wires *internalapi/runner.HTTPClient; tests
	// pass a fake.
	runner ToolRunner
	// inFlight is the count of running job goroutines. The cap is
	// read from cap.MaxConcurrentJobs() on every Enqueue; the atomic
	// counter is the only source of truth for the running count.
	// We use a counter (not a channel) so the cap can change without
	// reallocating the semaphore mid-flight.
	inFlight int64
	mu       sync.Mutex // guards running set to avoid double-running a job
	running  map[string]bool
}

// New wires an engine. Concurrency is bounded by cap.MaxConcurrentJobs,
// read live on every Enqueue so the power profile can throttle the
// engine without a restart.
//
// runner is the engine's window onto the Job Pod internal API
// (M2-T4). A nil runner is valid: code-archetype sub-steps
// short-circuit without HTTP calls, and the M1 walking skeleton
// remains usable in unit tests without a pod manager. Production
// wires *internal/internalapi/runner.HTTPClient.
//
// eval is the EvaluationRecord repository (M3-T1). A nil eval is
// accepted for unit tests that pre-date the evaluation layer; the
// engine's evaluate/compare phases will return a clear error if
// reached, which is the right fail-loud behavior (no silent loss of
// audit data).
func New(cfg *config.Config, db *store.Store, jobs *job.Repository, projects *project.Repo,
	artifacts *artifact.Store, eval *evaluation.Repo, client *llm.Client, registry *llm.Registry,
	freezer Freezer, cap ConcurrencyCap, runner ToolRunner) *Engine {
	if cap == nil {
		// No power source: fall back to a static cap derived from
		// cfg.Limits so the engine remains usable in tests and
		// minimal configs.
		cap = staticCap{max: cfg.Limits.MaxConcurrentJobs}
	}
	return &Engine{
		cfg: cfg, db: db, jobs: jobs, projects: projects, artifacts: artifacts,
		eval: eval, client: client, registry: registry, freezer: freezer, cap: cap,
		runner:  runner,
		running: map[string]bool{},
	}
}

// staticCap is the fallback ConcurrencyCap when no PowerManager is
// wired. It returns a fixed value derived from cfg.Limits at construction.
type staticCap struct{ max int }

func (s staticCap) MaxConcurrentJobs() int {
	if s.max < 1 {
		return 1
	}
	return s.max
}

// Enqueue starts asynchronous execution of a job. Returns immediately;
// callers watch progress through the job state and event log. The
// concurrency cap is read at enqueue time, not at construction, so
// profile changes take effect immediately. We block on a goroutine-local
// ticker until the in-flight count drops below the live cap, then run.
//
// Concurrency reservation is atomic: a single CAS increments
// `inFlight` only when the current value is below the live cap, so two
// goroutines polling the same `cap` cannot both pass the
// `< cap` check and then both increment — the second CAS sees the
// first increment and retries. A non-atomic
// `LoadInt64 → AddInt64` here lets the cap be silently exceeded
// under contention, which is exactly what the §24 throttle promises
// it will not do.
func (e *Engine) Enqueue(jobID string) {
	go func() {
		defer atomic.AddInt64(&e.inFlight, -1)
		for {
			max := e.cap.MaxConcurrentJobs()
			if max < 1 {
				max = 1
			}
			// Try to reserve a slot with a single CAS. If
			// the cap is exceeded, sleep and retry; if the
			// CAS lost a race, retry immediately so we
			// do not burn a sleep cycle on a transient
			// race.
			cur := atomic.LoadInt64(&e.inFlight)
			if cur >= int64(max) {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			if !atomic.CompareAndSwapInt64(&e.inFlight, cur, cur+1) {
				continue
			}
			e.Run(context.Background(), jobID)
			return
		}
	}()
}

// Recover resumes every active job after a daemon restart or an unfreeze
// (§23.6): each job continues from its last committed state, paused jobs
// resume to paused_from.
func (e *Engine) Recover(ctx context.Context) {
	active, err := e.jobs.Active(ctx)
	if err != nil {
		slog.Error("recovery: listing active jobs", "err", err)
		return
	}
	for _, j := range active {
		if j.State.Terminal() {
			continue
		}
		if err := e.jobs.SetRecoveryFlag(ctx, j.ID, "interrupted"); err != nil {
			slog.Error("recovery: marking interrupted", "job", j.ID, "err", err)
		}
		slog.Info("resuming job after restart", "job", j.ID, "state", j.State)
		e.Enqueue(j.ID)
	}
}

// audit appends an engine event to the append-only log.
func (e *Engine) audit(ctx context.Context, jobID string, data map[string]any) {
	if _, err := e.db.AppendEvent(ctx, store.Event{Category: "jobs", JobID: jobID, Data: data}); err != nil {
		slog.Error("engine: appending event", "job", jobID, "err", err)
	}
}

// Run drives one job from its current state to a terminal state (or
// paused). Safe to call on an already-terminal or already-running job.
func (e *Engine) Run(ctx context.Context, jobID string) {
	e.mu.Lock()
	if e.running[jobID] {
		e.mu.Unlock()
		return // another goroutine owns this job
	}
	e.running[jobID] = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.running, jobID)
		e.mu.Unlock()
	}()

	for {
		j, err := e.jobs.Get(ctx, jobID)
		if err != nil {
			slog.Error("engine: loading job", "job", jobID, "err", err)
			return
		}
		if j.State.Terminal() || j.State == job.StatePaused {
			return
		}

		// §22.1: frozen means no work proceeds. Active jobs pause; queued
		// jobs simply stay queued (nothing has started). Either way,
		// unfreeze resumes them via Recover.
		if e.freezer.Frozen() {
			if job.CanTransition(j.State, job.StatePaused) {
				if _, err := e.jobs.Transition(ctx, jobID, job.StatePaused); err != nil {
					slog.Error("engine: pausing for freeze", "job", jobID, "err", err)
				}
			}
			return
		}

		if err := e.step(ctx, j); err != nil {
			if errors.Is(err, ErrPaused) {
				return
			}
			slog.Error("engine: phase failed", "job", jobID, "state", j.State, "err", err)
			if _, terr := e.jobs.Transition(ctx, jobID, job.StateFailed); terr != nil {
				slog.Error("engine: marking job failed", "job", jobID, "err", terr)
			}
			e.audit(ctx, jobID, map[string]any{
				"event": "job_failed", "state": string(j.State), "error": err.Error(),
			})
			return
		}
		// A successful step is a successful resume (§8.3: the recovery
		// flag clears once the job makes progress again). M3-T4 commit
		// 4.1 moved the reflection counter to `system_state`, so
		// `RecoveryFlag` is now solely the engine's "interrupted
		// after a crash" annotation; clearing it is unconditional.
		if j.RecoveryFlag != "" {
			_ = e.jobs.SetRecoveryFlag(ctx, jobID, "")
		}
	}
}
