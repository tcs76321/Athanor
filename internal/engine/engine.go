// Package engine drives jobs through the full §8.2 state machine:
// queued → context_building → planning → diverging (N candidates,
// §13.1) → evaluating (security persona, temp 0.0, EvaluationRecord
// persistence) → reflecting (only on no-pass, budgeted retry loop)
// → synthesizing (consumes passing candidates) → comparing
// (§19.3 deterministic rule) → completed/failed.
//
// M3-T1 completed §8.2; the MCE (§10) landed in M5, so context is
// assembled through the §10.5 tier ladder rather than the naive prompt v1.
// Notes that remain true:
//   - The §12.6 floor rule still applies, so a context-shortage
//     pause-and-recommend still happens here.
//   - The ExplorationPath seam in `llm.ResolveTemperature` is wired
//     but the engine always passes `nil` (the path table and stage
//     resolution remain ROADMAP backlog).
//   - The code-archetype pod sub-steps (`runCodeInPod`, `runTestsInPod`)
//     run inside `phaseEvaluate` (M3-T2, ADR-0014), with
//     `running_tests` recorded as an event-row substate (not a column)
//     per §8.1's "tracked sub-state" note.
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
	"github.com/tcs76321/athanor/internal/corrections"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/interruptions"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/policy"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/toolenvelope"
	"github.com/tcs76321/athanor/internal/verify"
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

// PauseGate is the §24 power surface the engine consults: when Paused() is
// true (on battery without an override, or system sleep) active jobs pause at
// the next phase boundary; a later ResumePaused re-drives them. Satisfied by
// *power.PowerManager. A nil gate never pauses.
type PauseGate interface {
	Paused() bool
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
	// RunLint runs the linter in the job's pod (F4-T3). An empty
	// Command asks the internal API for its closed-set default. Same
	// contract as RunCode.
	RunLint(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error)
	// FetchURL fetches one task-declared source through the
	// §21.5 gateway (M4-T7, ADR-0019 §7). The route is
	// envelope-gated server-side; a 403 surfaces as
	// toolenvelope.ErrToolDisallowed and the research sub-step
	// soft-fails that source.
	FetchURL(ctx context.Context, jobID string, req toolenvelope.FetchURLRequest) (toolenvelope.FetchURLResponse, error)
}

// GitCommitter is the §14 Git-as-undo seam: it records an accepted
// artifact's content to the project repository on an agent-created branch
// and returns the commit SHA. The production adapter
// (cmd/athanor/git_client.go) is the only os/exec git call site; Gate G1
// allowlists it. A nil seam is valid: the engine audits "no git" rather
// than committing.
type GitCommitter interface {
	Commit(ctx context.Context, repoPath, branch, relPath string, content []byte, message string) (string, error)
}

// CorrectionSink is the §18 feedback seam (M6-T6): the engine reports a
// phase failure as a runtime_error CorrectionRecord. The production impl is
// *corrections.Repo; a nil sink is valid (unit tests, the M1 skeleton).
type CorrectionSink interface {
	Capture(ctx context.Context, in corrections.CaptureInput) (corrections.Record, error)
}

// CorrectionSource is the §18.3 retrieval seam (M6-T7): the active
// corrections relevant to a project, severity-ordered, plus the applied-count
// increment. Satisfied by *corrections.Repo; nil leaves tier 4 empty.
type CorrectionSource interface {
	Relevant(ctx context.Context, projectID string, limit int) ([]corrections.Record, error)
	MarkApplied(ctx context.Context, id string) error
}

// TokenSink receives incremental model output for the live watch view
// (M6-T8c, §20.3). The production implementation is the UI hub; nil makes
// every call non-streaming (the pre-T8 path).
type TokenSink interface {
	Publish(jobID, text string)
}

// InterruptionStore is the §20.4 queue seam (M6-T8c): pending user notes are
// injected at the next safe point and marked injected. Satisfied by
// *interruptions.Repo; nil disables injection.
type InterruptionStore interface {
	Pending(ctx context.Context, jobID string) ([]interruptions.Note, error)
	MarkInjected(ctx context.Context, ids []string) error
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
	// pauseGate is the §24 power pause signal (M7-T1). nil never pauses.
	pauseGate PauseGate
	// runner is the engine's window onto the Job Pod internal
	// API (M2-T4). nil means "no Job Pod manager"; the code
	// archetype's sub-steps short-circuit without HTTP calls.
	// Production wires *internalapi/runner.HTTPClient; tests
	// pass a fake.
	runner ToolRunner
	// evictor frees context pressure by flushing lowest-priority
	// §10.5 tiers to Dormant (M5-T4, ADR-0022 §5; filled by the M5-T5
	// ladder, ADR-0023 §6). nil is valid configuration — the ToolRunner
	// precedent: at `critical` the engine treats a nil seam (or one that
	// freed nothing) as a floor breach and pauses.
	evictor Evictor
	// ctxProvider is the engine's window onto the MCE working set
	// (M5-T5, ADR-0023 §7): the §10.1 active chunk and the Dormant
	// Index. nil means "no MCE" — tiers 3 and 6 stay empty and prompts
	// are the pre-T5 assembler exactly.
	ctxProvider ContextProvider
	// podLifecycle ensures/tears down the job's Job Pod (M2-T4b.5,
	// ADR-0024 §2). nil means "no lifecycle seam": the engine never starts
	// a pod, which is the pre-T4b behavior unit tests rely on.
	podLifecycle PodLifecycle
	// git is the §14 Git-as-undo seam (F3-T5, ADR-0030): it records an
	// accepted artifact to the project repository on an agent branch. nil
	// means "no git"; the engine audits the skip and continues.
	git GitCommitter
	// corrections is the §18 feedback seam (M6-T6, ADR-0037): a phase
	// failure is reported as a runtime_error CorrectionRecord. nil is valid.
	corrections CorrectionSink
	// correctionSource is the §18.3 injection seam (M6-T7, ADR-0038): active
	// corrections are ranked and rendered into §11.2 position 8. nil leaves
	// tier 4 empty (byte-identical to pre-M6-T7).
	correctionSource CorrectionSource
	// tokenSink receives incremental model output for the watch view
	// (M6-T8c). nil selects the non-streaming call path.
	tokenSink TokenSink
	// interruptions is the §20.4 queue seam (M6-T8c). nil disables injection.
	interruptions InterruptionStore
	// strategy is the §13.3 capture seam (M6-T10). nil disables capture.
	strategy StrategySink
	// insights is the §13.4 prompt channel (M6-T11). nil disables notes.
	insights StrategyInsightSource
	// policy is the F4-T1 compute/model-selection seam (ADR-0044). nil uses
	// policy.Default, which reproduces pre-F4 behavior exactly.
	policy policy.Policy
	// history is the F4-T2 outcome-history seam: recent per-archetype
	// outcome statistics feed the policy's familiarity signal. nil leaves
	// the signal absent (the criteria heuristic still applies).
	history StrategyHistory
	// verifiers is the F4-T3 deterministic verification registry. nil uses
	// verify.Default(), so the engine always has a verifier set.
	verifiers *verify.Registry
	// inFlight is the count of running job goroutines. The cap is
	// read from cap.MaxConcurrentJobs() on every Enqueue; the atomic
	// counter is the only source of truth for the running count.
	// We use a counter (not a channel) so the cap can change without
	// reallocating the semaphore mid-flight.
	inFlight int64
	mu       sync.Mutex // guards running set to avoid double-running a job
	running  map[string]bool
	// onTerminal is invoked when a job reaches a terminal state, so the
	// M6-T2 scheduler can advance the job's task graph (ADR-0033). A nil
	// seam is the M1 walking-skeleton default: nothing observes the
	// transition.
	onTerminal func(ctx context.Context, jobID string, state job.State)
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
//
// evictor is the §10.4 context-eviction seam (M5-T4, ADR-0022 §5). A
// nil evictor is valid: `critical` pressure with nothing able to evict
// pauses the job rather than sending a prompt that Ollama would
// silently truncate. Production wires NewLadderEvictor() (M5-T5.6).
//
// ctxProvider is the engine's window onto the MCE working set (M5-T5,
// ADR-0023 §7). A nil provider is valid: tiers 3 and 6 are empty and the
// assembler behaves exactly as it did before M5-T5.
//
// podLifecycle ensures/tears down a job's Job Pod (M2-T4b.5, ADR-0024
// §2). A nil seam is valid: the engine never starts a pod — the pre-T4b
// behavior unit tests rely on. Production wires the cmd/athanor adapter
// over jobpod.Manager.
func New(cfg *config.Config, db *store.Store, jobs *job.Repository, projects *project.Repo,
	artifacts *artifact.Store, eval *evaluation.Repo, client *llm.Client, registry *llm.Registry,
	freezer Freezer, cap ConcurrencyCap, runner ToolRunner, evictor Evictor,
	ctxProvider ContextProvider, podLifecycle PodLifecycle) *Engine {
	if cap == nil {
		// No power source: fall back to a static cap derived from
		// cfg.Limits so the engine remains usable in tests and
		// minimal configs.
		cap = staticCap{max: cfg.Limits.MaxConcurrentJobs}
	}
	if freezer == nil {
		// No kill switch wired: never frozen. Production always wires
		// control.KillSwitch; the fallback keeps this seam uniformly
		// nil-safe like cap above, instead of panicking in Run.
		freezer = openFreezer{}
	}
	return &Engine{
		cfg: cfg, db: db, jobs: jobs, projects: projects, artifacts: artifacts,
		eval: eval, client: client, registry: registry, freezer: freezer, cap: cap,
		runner:       runner,
		evictor:      evictor,
		ctxProvider:  ctxProvider,
		podLifecycle: podLifecycle,
		running:      map[string]bool{},
	}
}

// SetGitCommitter wires the §14 Git-as-undo seam (F3-T5). It is a setter
// rather than a New() parameter so existing New call sites are unchanged;
// production wires it in cmd/athanor/serve.go.
func (e *Engine) SetGitCommitter(g GitCommitter) { e.git = g }

// SetCorrectionSink wires the §18 feedback seam (M6-T6). It is a setter so
// existing New call sites are unchanged; production wires the corrections
// repo in cmd/athanor/serve.go. A nil sink is a no-op.
func (e *Engine) SetCorrectionSink(s CorrectionSink) { e.corrections = s }

// SetCorrectionSource wires the §18.3 injection seam (M6-T7). A nil source
// leaves the corrections tier empty.
func (e *Engine) SetCorrectionSource(s CorrectionSource) { e.correctionSource = s }

// SetTokenSink wires the live token stream (M6-T8c). A nil sink makes every
// model call non-streaming.
func (e *Engine) SetTokenSink(s TokenSink) { e.tokenSink = s }

// SetInterruptionStore wires the §20.4 queue (M6-T8c). A nil store disables
// interruption injection.
func (e *Engine) SetInterruptionStore(s InterruptionStore) { e.interruptions = s }

// SetStrategySink wires §13.3 strategy capture (M6-T10). A nil sink disables
// capture.
func (e *Engine) SetStrategySink(s StrategySink) { e.strategy = s }

// SetStrategyInsightSource wires the §13.4 prompt channel (M6-T11). A nil
// source disables strategy notes.
func (e *Engine) SetStrategyInsightSource(s StrategyInsightSource) { e.insights = s }

// SetPolicy wires the F4-T1 compute/model-selection seam (ADR-0044). A nil
// policy uses policy.Default, so behavior is unchanged until one is set.
func (e *Engine) SetPolicy(p policy.Policy) { e.policy = p }

// StrategyHistory is the F4-T2 history seam: recent per-archetype outcome
// statistics so the policy can treat a familiar task class as easy. A nil
// seam leaves the familiarity signal absent.
type StrategyHistory interface {
	RecentStats(ctx context.Context, archetype string, limit int) (samples int, acceptRate float64, err error)
}

// SetStrategyHistory wires the F4-T2 outcome-history seam. A nil seam
// disables the familiarity signal (difficulty and criteria still apply).
func (e *Engine) SetStrategyHistory(h StrategyHistory) { e.history = h }

// SetPauseGate wires the §24 power pause signal (M7-T1). A nil gate means
// the engine never pauses for power.
func (e *Engine) SetPauseGate(g PauseGate) { e.pauseGate = g }

// SetVerifiers wires the F4-T3 deterministic verifier registry. A nil
// registry uses verify.Default().
func (e *Engine) SetVerifiers(r *verify.Registry) { e.verifiers = r }

// verifierRegistry resolves the verifier set, defaulting to the in-tree one.
func (e *Engine) verifierRegistry() *verify.Registry {
	if e.verifiers == nil {
		return verify.Default()
	}
	return e.verifiers
}

// crossFamily reports whether the judge persona's model family differs from
// the generator's. It returns both families (for the audit) and ok=false
// when either is unknown or they match. Consulted only on the F4-T3
// verification-first path, so a single-model config in the default llm mode
// is unaffected.
func (e *Engine) crossFamily(generatorRole, judgeRole string) (gen, judge string, ok bool) {
	if e.registry == nil {
		return "", "", false
	}
	gp, gok := e.registry.Persona(generatorRole)
	jp, jok := e.registry.Persona(judgeRole)
	if !gok || !jok || gp.Family == "" || jp.Family == "" {
		return gp.Family, jp.Family, false
	}
	return gp.Family, jp.Family, gp.Family != jp.Family
}

// difficultyStateKey is the system_state key holding a job's
// planning-derived difficulty hint ("easy" | "hard").
func difficultyStateKey(jobID string) string { return "plan:difficulty:" + jobID }

// planFor resolves a job's compute plan through the policy seam. It gathers
// the task features (archetype, criteria count, the planner's difficulty
// hint, recent outcome history) and the operator ceilings, then asks the
// policy. A nil policy uses policy.Default, which reproduces pre-F4 behavior.
func (e *Engine) planFor(ctx context.Context, j job.Job, p project.Project, t project.Task) policy.Plan {
	return e.decidePlan(e.planInputs(ctx, j, p, t))
}

// planInputs assembles the pure policy input from persisted state.
func (e *Engine) planInputs(ctx context.Context, j job.Job, p project.Project, t project.Task) policy.Inputs {
	in := policy.Inputs{
		Features: policy.Features{
			Archetype:     p.Archetype,
			CriteriaCount: len(t.Criteria),
			Difficulty:    e.difficultyHint(ctx, j.ID),
		},
		Limits: policy.Limits{
			CandidatesCeiling: 1,
			ReflectionCeiling: policy.DefaultReflectionLoops,
		},
	}
	if e.cfg != nil {
		in.Limits.CandidatesCeiling = e.cfg.Execution.DivergenceCandidates
		in.Limits.ReflectionCeiling = e.cfg.Execution.MaxReflectionLoopsValue()
	}
	if e.history != nil {
		if samples, rate, err := e.history.RecentStats(ctx, p.Archetype, 20); err == nil {
			in.Features.RecentSamples = samples
			in.Features.RecentAcceptRate = rate
		}
	}
	// F4-T7a: an active insight can bias the divergence persona. Proposed
	// insights are excluded by the lister, so they never reach here.
	if e.insights != nil {
		if lister, ok := e.insights.(StrategyInsightLister); ok {
			if pref := e.preferredDivergencePersona(ctx, lister, p.Archetype); pref != "" {
				in.Features.PreferredDivergencePersona = pref
			}
		}
	}
	return in
}

// decidePlan runs the configured policy and applies the operator-selected
// judge persona as a bound on the judgment phases. The default
// (execution.judge_persona: security) is a no-op.
func (e *Engine) decidePlan(in policy.Inputs) policy.Plan {
	var plan policy.Plan
	if e.policy == nil {
		plan = policy.Default{}.Decide(in)
	} else {
		plan = e.policy.Decide(in)
	}
	if e.cfg != nil {
		// F4-T4 quorum applies to the LLM judge in either mode; the
		// operator sets execution.policy.judge_count.
		plan.JudgeCount = e.cfg.Execution.JudgeCountValue()
		// The operator-selected judge mode is a bound on the plan (F4-T3).
		if e.cfg.Execution.JudgeModeSelection() == config.JudgeModeVerifier {
			plan.JudgeMode = policy.JudgeVerifier
		}
		if jp := e.cfg.Execution.JudgePersona; jp != "" {
			if plan.ModelRouting == nil {
				plan.ModelRouting = map[string]string{}
			}
			plan.ModelRouting[policy.PhaseEvaluating] = jp
			plan.ModelRouting[policy.PhaseComparing] = jp
		}
		// F4-T5 heterogeneous diversity: cycle candidates across the
		// generator and the `alternative` persona so a non-trivial task
		// draws from more than one source. A custom policy may set
		// DivergenceRoles explicitly; this is only the default.
		if e.cfg.Execution.HeterogeneousDiversityEnabled() && plan.Candidates >= 2 && len(plan.DivergenceRoles) == 0 {
			gen := plan.ModelRouting[policy.PhaseDiverging]
			if gen == "" {
				gen = llm.RoleMain
			}
			if gen == llm.RoleAlternative {
				plan.DivergenceRoles = []string{llm.RoleAlternative}
			} else {
				plan.DivergenceRoles = []string{gen, llm.RoleAlternative}
			}
		}
	}
	// F4-T7a: an active insight's preferred persona leads divergence. Applied
	// after the heterogeneity default so the insight always wins position 0.
	if pref := in.Features.PreferredDivergencePersona; pref != "" {
		alt := llm.RoleAlternative
		if pref == alt {
			alt = llm.RoleMain
		}
		plan.DivergenceRoles = []string{pref, alt}
		plan.InsightBias = pref
	}
	return plan
}

// roleFor resolves the persona serving a phase through the plan's
// ModelRouting, falling back to the engine's historical role.
func (e *Engine) roleFor(plan policy.Plan, phase, fallback string) string {
	if r, ok := plan.ModelRouting[phase]; ok && r != "" {
		return r
	}
	return fallback
}

// difficultyHint reads the planner's easy/hard hint for a job ("" when the
// job has not planned yet).
func (e *Engine) difficultyHint(ctx context.Context, jobID string) string {
	if e.db == nil {
		return ""
	}
	var v string
	if err := e.db.DB().QueryRowContext(ctx,
		`SELECT value FROM system_state WHERE key = ?`, difficultyStateKey(jobID)).Scan(&v); err != nil {
		return ""
	}
	return v
}

// setDifficultyHint persists the planner's hint for a job.
func (e *Engine) setDifficultyHint(ctx context.Context, jobID, hint string) {
	if e.db == nil || hint == "" {
		return
	}
	if _, err := e.db.DB().ExecContext(ctx,
		`INSERT INTO system_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		difficultyStateKey(jobID), hint); err != nil {
		slog.Warn("engine: persisting difficulty hint", "job", jobID, "err", err)
	}
}

// auditComputePlan records the resolved plan (ADR-0044: the decision is
// audited so outcomes can be attributed to it). `stage` distinguishes the
// pre-planning profile resolution from the post-planning divergence
// resolution (which can see the difficulty hint).
func (e *Engine) auditComputePlan(ctx context.Context, jobID, stage string, plan policy.Plan) {
	e.audit(ctx, jobID, map[string]any{
		"event":                "compute_planned",
		"stage":                stage,
		"candidates":           plan.Candidates,
		"max_reflection_loops": plan.MaxReflectionLoops,
		"judge_mode":           string(plan.JudgeMode),
		"judge_count":          plan.JudgeCount,
		"model_routing":        plan.ModelRouting,
		"divergence_roles":     plan.DivergenceRoles,
	})
	// F4-T7a: record the feedback→policy bias once, at the divergence stage
	// (the profile-stage plan runs before insights are relevant to the run).
	if stage == "divergence" && plan.InsightBias != "" {
		e.audit(ctx, jobID, map[string]any{
			"event": "policy_biased_from_insight", "persona": plan.InsightBias,
		})
	}
}

// isJudgmentPhase reports whether a phase adjudicates outputs and so must not
// spend its token budget on hidden reasoning (F4-T8).
func isJudgmentPhase(phase string) bool {
	return phase == llm.PhaseEvaluating || phase == llm.PhaseComparing
}

// thinkFlag renders a *bool Think for the audit log (nil when unset).
func thinkFlag(t *bool) any {
	if t == nil {
		return nil
	}
	return *t
}

// recordFailureCorrection captures a phase failure as a runtime_error
// CorrectionRecord (§18.1). Best-effort: a capture failure is logged, never
// fatal — the job has already failed.
func (e *Engine) recordFailureCorrection(ctx context.Context, jobID, projectID string, phase job.State, cause error) {
	if e.corrections == nil {
		return
	}
	if _, err := e.corrections.Capture(ctx, corrections.CaptureInput{
		Source: corrections.SourceRuntimeError, JobID: jobID, ProjectID: projectID,
		Detail: cause.Error() + " (phase " + string(phase) + ")",
	}); err != nil {
		slog.Error("engine: recording failure correction", "job", jobID, "err", err)
	}
}

// SetOnJobTerminal wires the terminal-transition observer the M6-T2
// scheduler uses to advance a decomposed task graph (ADR-0033). It fires
// once per terminal transition from Run (both the normal terminal branch
// and the phase-error path). A nil fn (the default) is a no-op.
func (e *Engine) SetOnJobTerminal(fn func(ctx context.Context, jobID string, state job.State)) {
	e.onTerminal = fn
}

// signalTerminal invokes the terminal observer, if any. The callback runs
// on the engine's job goroutine; the scheduler serializes its own work.
func (e *Engine) signalTerminal(ctx context.Context, jobID string, state job.State) {
	if e.onTerminal != nil {
		e.onTerminal(ctx, jobID, state)
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

// openFreezer is the fallback Freezer when none is wired: the engine is
// never frozen. Production always wires *control.KillSwitch (see New).
type openFreezer struct{}

func (openFreezer) Frozen() bool { return false }

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

// pauseReason returns the active stop reason ("kill_switch", "power", or "")
// so the engine's run loop has a single gate and a post-mortem can tell the
// two apart.
func (e *Engine) pauseReason() string {
	if e.freezer.Frozen() {
		return "kill_switch"
	}
	if e.pauseGate != nil && e.pauseGate.Paused() {
		return "power"
	}
	return ""
}

// ResumePaused re-drives every job parked in `paused` back to its
// paused_from state and enqueues it. It is the §24 wake / AC-restore path:
// the power Supervisor's OnResume calls it when the pause gate reopens. A job
// paused for another reason (e.g. a context-floor violation) simply re-pauses
// on the next run, so this is safe to call broadly.
func (e *Engine) ResumePaused(ctx context.Context) {
	active, err := e.jobs.Active(ctx)
	if err != nil {
		slog.Error("power resume: listing active jobs", "err", err)
		return
	}
	for _, j := range active {
		if j.State != job.StatePaused || j.PausedFrom == "" {
			continue
		}
		if _, err := e.jobs.Transition(ctx, j.ID, j.PausedFrom); err != nil {
			slog.Error("power resume: transition", "job", j.ID, "err", err)
			continue
		}
		e.audit(ctx, j.ID, map[string]any{
			"event": "job_resumed", "reason": "power", "to": string(j.PausedFrom),
		})
		slog.Info("power resume: re-driving paused job", "job", j.ID, "to", j.PausedFrom)
		e.Enqueue(j.ID)
	}
}

// audit appends an engine event to the append-only log under the `jobs`
// category (§28.1).
func (e *Engine) audit(ctx context.Context, jobID string, data map[string]any) {
	e.auditCat(ctx, jobID, "jobs", data)
}

// auditCat appends an engine event under an explicit §28.1 category.
// The category-aware variant exists for M5-T4's `inference` rows
// (context calculations, KV-cache pressure — §28.1) and is the seam
// future non-`jobs` engine events use; the plain audit above keeps
// every pre-M5-T4 call site unchanged.
func (e *Engine) auditCat(ctx context.Context, jobID, category string, data map[string]any) {
	if _, err := e.db.AppendEvent(ctx, store.Event{Category: category, JobID: jobID, Data: data}); err != nil {
		slog.Error("engine: appending event", "job", jobID, "category", category, "err", err)
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
		if j.State.Terminal() || j.State == job.StatePaused || j.State == job.StateAwaitingApproval {
			if j.State.Terminal() {
				// M5-T5 (ADR-0023 §5): a finished job's evictions are
				// history, not state. Clearing here (and only here)
				// keeps `system_state` bounded while a paused job keeps
				// its suppression for the resume.
				e.clearSuppressedTiers(ctx, jobID)
				// M6-T10 (§13.3): capture the immutable outcome before any
				// teardown that can block. stopPod can take seconds on a real
				// pod, and an offline collector must never observe a terminal
				// job with no strategy_outcomes row (the M3-T7 code-goal
				// capture gap F4 deferred).
				e.captureOutcome(ctx, jobID)
				// M6-T2 (ADR-0033): let the scheduler advance the graph.
				e.signalTerminal(ctx, jobID, j.State)
				// M2-T4b.5 (ADR-0024 §2): a terminal job's Job Pod has no
				// further use; stop it (idempotent, nil-safe).
				e.stopPod(ctx, jobID)
			}
			return
		}

		// §22.1 / §24: the kill switch or the power policy can stop work.
		// Active jobs pause (and are re-driven by Recover or ResumePaused);
		// queued jobs simply stay queued (nothing has started).
		if reason := e.pauseReason(); reason != "" {
			if job.CanTransition(j.State, job.StatePaused) {
				if _, err := e.jobs.Transition(ctx, jobID, job.StatePaused); err != nil {
					slog.Error("engine: pausing", "job", jobID, "reason", reason, "err", err)
				} else {
					e.audit(ctx, jobID, map[string]any{
						"event": "job_paused", "reason": reason, "from": string(j.State),
					})
				}
			}
			return
		}

		// M6-T10 (§13.3): capture the strategy profile once, at start.
		if j.State == job.StateQueued {
			e.captureProfile(ctx, j)
		}

		if err := e.step(ctx, j); err != nil {
			if errors.Is(err, ErrPaused) {
				return
			}
			slog.Error("engine: phase failed", "job", jobID, "state", j.State, "err", err)
			if _, terr := e.jobs.Transition(ctx, jobID, job.StateFailed); terr != nil {
				slog.Error("engine: marking job failed", "job", jobID, "err", terr)
			} else {
				// M6-T2 (ADR-0033): the failure path is a terminal
				// transition too; the scheduler learns of it here. M6-T6
				// records the failure as a CorrectionRecord (§18.1).
				e.signalTerminal(ctx, jobID, job.StateFailed)
				e.recordFailureCorrection(ctx, jobID, j.ProjectID, j.State, err)
				e.captureOutcome(ctx, jobID)
				// A failed job is terminal too; clear its §10.5 suppression
				// row so `system_state` does not accumulate one row per
				// failed job (the loop-top terminal branch clears the
				// success/compare paths).
				e.clearSuppressedTiers(ctx, jobID)
				// M2-T4b.5 (ADR-0024 §2): a failed job is terminal too; stop
				// its pod now. The failure path used to return without
				// teardown, leaving the pod for the next boot's sweep.
				e.stopPod(ctx, jobID)
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
