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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/budget"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/corrections"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/interruptions"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/notebook"
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

// OperatorGate is the household-mode surface. A stopped Athanor accepts no new
// work and preempts active work; Full/Lite/Deep allow work.
type OperatorGate interface {
	Allowed() bool
}

// ModelRequest asks the composition layer for a model. It carries only the
// grounded facts the selector needs; the pool owns the measured evidence.
type ModelRequest struct {
	Phase       string
	Capability  string
	Stance      string
	MinContext  int
	AvoidFamily string
	Scope       string
	ProjectID   string
	JobID       string
	Explore     bool
}

// ModelChoice is one selected model and the reason it won.
type ModelChoice struct {
	Model         string
	Family        string
	ContextTarget int
	Temperature   float64
	Score         float64
	Reason        string
}

// ModelSelector is the composition seam (ADR-0104). A nil selector preserves
// the legacy persona registry behavior.
type ModelSelector interface {
	Choose(ctx context.Context, req ModelRequest) (ModelChoice, bool)
	ChooseMany(ctx context.Context, req ModelRequest, n int) []ModelChoice
	Persona(model string) (ModelChoice, bool)
	Observe(ctx context.Context, jobID string, success bool)
	PreferPanel() bool
}

// PoolModelPrefix marks a synthetic role that names a model directly rather
// than a legacy persona. The registry never sees it.
const PoolModelPrefix = "pool:"

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
	// ReadFile reads one repository-relative file from the staged snapshot
	// (ADR-0113). It is observation-only: no write, no execute, no host FS.
	ReadFile(ctx context.Context, jobID string, req toolenvelope.ReadFileRequest) (toolenvelope.ReadFileResponse, error)
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
	// CommitTree writes a multi-file candidate into the repository at its
	// relative paths and commits once (ADR-0065 §5). Used for a code tree so
	// accepted source lands in the repo, not a managed blob.
	CommitTree(ctx context.Context, repoPath, branch string, files []toolenvelope.File, message string) (string, error)
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

// MemorySource is the §10.2 retrieval seam (M5-T7): project-scoped compacted
// memory and dormant chunk hits from the hybrid FTS5/vector retriever. The
// engine renders compacted memos into tier 5; chunk hits remain the Dormant
// Index / working-set producers. A nil source leaves tier 5 empty.
type MemorySource interface {
	RelevantMemory(ctx context.Context, projectID, query string, limit int) ([]mce.MemoryHit, error)
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
	// operatorGate is the user-facing Deep/Full/Lite/Stopped gate. nil allows
	// work (pre-operator behavior).
	operatorGate OperatorGate
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
	// memorySource is the §10.2 retrieval seam (M5-T7): compacted project
	// memory is rendered into tier 5. nil leaves tier 5 empty.
	memorySource MemorySource
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
	// selector is the ADR-0104 composition seam. When a pool is present it
	// chooses the model and context for each phase, overriding the fixed
	// persona mapping; nil keeps the legacy behavior.
	selector ModelSelector
	// horizon is the ADR-0091 budget ledger. nil disables accounting, which
	// is the pre-ledger behavior.
	horizon *budget.Ledger
	// history is the F4-T2 outcome-history seam: recent per-archetype
	// outcome statistics feed the policy's familiarity signal. nil leaves
	// the signal absent (the criteria heuristic still applies).
	history StrategyHistory
	// notebook is the ADR-0109 persistent working-memory seam. nil keeps
	// the pre-notebook prompt and behavior.
	notebook Notebook
	// clarifier is the ADR-0112 mid-job question seam. nil ignores the
	// QUESTION marker.
	clarifier Clarifier
	// memoryWriter is the ADR-0114 completed-job summary seam. nil disables
	// the write (the M1 walk still runs).
	memoryWriter MemoryWriter
	// verifiers is the F4-T3 deterministic verification registry. nil uses
	// verify.Default(), so the engine always has a verifier set.
	verifiers *verify.Registry
	// inFlight is the count of running job goroutines. The cap is
	// read from cap.MaxConcurrentJobs() on every Enqueue; the atomic
	// counter is the only source of truth for the running count.
	// We use a counter (not a channel) so the cap can change without
	// reallocating the semaphore mid-flight.
	inFlight int64
	// draining is set by StopPods at graceful shutdown. Once true, ensurePod
	// is a no-op so a phase still in flight cannot start a new pod that
	// outlives the process (M9 soak). It is never reset.
	draining atomic.Bool
	mu       sync.Mutex // guards running set to avoid double-running a job
	running  map[string]bool
	// cancelMu guards per-job cancel funcs and the user-cancel set. A job is
	// cancellable only while its Run goroutine is alive.
	cancelMu  sync.Mutex
	cancels   map[string]context.CancelFunc
	cancelled map[string]bool
	pauseReq  map[string]bool
	// castMu guards the per-job generator family, used to prefer an
	// independent verifier family when the pool offers one.
	castMu    sync.Mutex
	jobFamily map[string]string
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
		cancels:      map[string]context.CancelFunc{},
		cancelled:    map[string]bool{},
		pauseReq:     map[string]bool{},
		jobFamily:    map[string]string{},
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

// SetMemorySource wires the §10.2 retrieval seam (M5-T7). A nil source leaves
// the episodic tier empty.
func (e *Engine) SetMemorySource(s MemorySource) { e.memorySource = s }

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

// SetBudgetLedger wires the ADR-0091 horizon ledger. A nil ledger, or disabled
// budget settings, leaves admission unchanged.
func (e *Engine) SetBudgetLedger(l *budget.Ledger) { e.horizon = l }

// StrategyHistory is the F4-T2 history seam: recent per-archetype outcome
// statistics so the policy can treat a familiar task class as easy. A nil
// seam leaves the familiarity signal absent.
type StrategyHistory interface {
	RecentStats(ctx context.Context, archetype string, limit int) (samples int, acceptRate float64, err error)
}

// StrategyOperationHistory is the M8-T10 optional seam: per-operation execution
// counts for a task class, feeding the exploration floor so an unproven
// operation stays eligible. It is type-asserted, so a source that implements
// only StrategyHistory keeps working.
type StrategyOperationHistory interface {
	OperationSamples(ctx context.Context, archetype string, limit int) (map[string]int, error)
}

// Notebook is the persistent per-job working-memory seam (ADR-0109). It is
// nil-safe: a nil notebook leaves the prompt unchanged.
type Notebook interface {
	Append(ctx context.Context, entry notebook.Entry) (notebook.Entry, error)
	Recent(ctx context.Context, jobID string, limit int) ([]notebook.Entry, error)
}

// SetStrategyHistory wires the F4-T2 outcome-history seam. A nil seam
// disables the familiarity signal (difficulty and criteria still apply).
func (e *Engine) SetStrategyHistory(h StrategyHistory) { e.history = h }

// SetNotebook wires the ADR-0109 working-memory store.
func (e *Engine) SetNotebook(n Notebook) { e.notebook = n }

// Clarifier parks a job on a model-initiated question (ADR-0112). A nil
// clarifier ignores the QUESTION marker.
type Clarifier interface {
	Ask(ctx context.Context, jobID, projectID, phase, question string) error
}

// SetClarifier wires the ADR-0112 clarification queue.
func (e *Engine) SetClarifier(c Clarifier) { e.clarifier = c }

// MemoryWriter records a completed job's deterministic summary for later
// retrieval (ADR-0114). A nil writer leaves memory writes disabled.
type MemoryWriter interface {
	RememberJobSummary(ctx context.Context, projectID, jobID, summary string) (bool, error)
}

// SetMemoryWriter wires the ADR-0114 job-summary writer.
func (e *Engine) SetMemoryWriter(w MemoryWriter) { e.memoryWriter = w }

// SetPauseGate wires the §24 power pause signal (M7-T1). A nil gate means
// the engine never pauses for power.
func (e *Engine) SetPauseGate(g PauseGate) { e.pauseGate = g }

// SetOperatorGate wires the Deep/Full/Lite/Stopped household mode. A nil gate
// allows work (pre-operator behavior).
func (e *Engine) SetOperatorGate(g OperatorGate) { e.operatorGate = g }

// SetModelSelector wires the composition layer (ADR-0104). A nil selector
// keeps the legacy fixed-persona behavior.
func (e *Engine) SetModelSelector(s ModelSelector) { e.selector = s }

// stanceForPhase maps a job phase to a composition stance.
func stanceForPhase(phase string) string {
	switch phase {
	case llm.PhasePlanning:
		return "plan"
	case llm.PhaseDiverging:
		return "generate"
	case llm.PhaseEvaluating, llm.PhaseComparing:
		return "verify"
	case llm.PhaseReflecting:
		return "critique"
	case llm.PhaseSynthesizing:
		return "synthesize"
	default:
		return "generate"
	}
}

func personaFromChoice(c ModelChoice) llm.Persona {
	return llm.Persona{
		Role:          "pool",
		Model:         c.Model,
		Family:        c.Family,
		ContextTarget: c.ContextTarget,
		Temperature:   c.Temperature,
	}
}

// personaFor resolves a phase's model. A synthetic `pool:<model>` role names
// a candidate directly; otherwise the composition selector chooses from the
// measured pool. The legacy registry is the fallback when no pool is wired.
func (e *Engine) personaFor(ctx context.Context, phase, role, capability, projectID, jobID string) (llm.Persona, bool) {
	if e.selector != nil && strings.HasPrefix(role, PoolModelPrefix) {
		if c, ok := e.selector.Persona(strings.TrimPrefix(role, PoolModelPrefix)); ok {
			return personaFromChoice(c), true
		}
	}
	if e.selector != nil && capability != "" && e.cfg != nil {
		minContext := 0
		if floor, ok := llm.FloorFor(capability, e.cfg.ContextEngine); ok {
			minContext = floor
		}
		req := ModelRequest{
			Phase:      phase,
			Capability: capability,
			Stance:     stanceForPhase(phase),
			MinContext: minContext,
			ProjectID:  projectID,
			JobID:      jobID,
		}
		if phase == llm.PhaseEvaluating || phase == llm.PhaseComparing {
			req.AvoidFamily = e.generatorFamily(jobID)
		}
		if phase == llm.PhaseDiverging && jobID != "" &&
			(capability == project.ArchetypeText || capability == project.ArchetypeDocument) &&
			exploreBucket(jobID, 8) == 0 {
			req.Explore = true
		}
		if projectID != "" {
			req.Scope = "project:" + projectID
		}
		if c, ok := e.selector.Choose(ctx, req); ok {
			if req.Explore {
				e.audit(ctx, jobID, map[string]any{
					"event": "cast_exploration", "phase": phase, "model": c.Model,
				})
			}
			if phase == llm.PhaseDiverging && jobID != "" {
				e.rememberGeneratorFamily(jobID, c.Family)
			}
			return personaFromChoice(c), true
		}
	}
	if e.registry == nil {
		return llm.Persona{}, false
	}
	return e.registry.Persona(role)
}

func (e *Engine) generatorFamily(jobID string) string {
	if jobID == "" {
		return ""
	}
	e.castMu.Lock()
	defer e.castMu.Unlock()
	return e.jobFamily[jobID]
}

func (e *Engine) rememberGeneratorFamily(jobID, family string) {
	if jobID == "" || family == "" {
		return
	}
	e.castMu.Lock()
	e.jobFamily[jobID] = family
	e.castMu.Unlock()
}

// exploreBucket is a stable, deterministic 1-in-n bucket for budgeted
// exploration. It never uses time or randomness, so a resumed job makes the
// same choice.
func exploreBucket(s string, n int) int {
	if n < 2 {
		return 0
	}
	sum := 0
	for _, r := range s {
		sum = (sum + int(r)) % n
	}
	return sum
}

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
	in := e.planInputs(ctx, j, p, t)
	plan := e.decidePlan(in)
	e.applyLearnedSelection(ctx, in, &plan)
	return plan
}

// planInputs assembles the pure policy input from persisted state.
func (e *Engine) planInputs(ctx context.Context, j job.Job, p project.Project, t project.Task) policy.Inputs {
	capability := capabilityFor(p, t)
	in := policy.Inputs{
		Features: policy.Features{
			Archetype:     capability,
			CriteriaCount: len(t.Criteria),
			Difficulty:    e.difficultyHint(ctx, j.ID),
			JobKey:        j.ID,
			Attempt:       e.taskAttemptIndex(ctx, t.ID, j.ID),
		},
		Limits: policy.Limits{
			CandidatesCeiling: 1,
			ReflectionCeiling: policy.DefaultReflectionLoops,
		},
	}
	if e.cfg != nil {
		in.Limits.CandidatesCeiling = e.cfg.Execution.DivergenceCandidates
		in.Limits.ReflectionCeiling = e.cfg.Execution.MaxReflectionLoopsValue()
		// §13.1: a hard task may raise candidates up to this ceiling (M9-T4).
		if h := e.cfg.Execution.MaxHardTaskVariations; h > 0 {
			in.Limits.HardCandidatesCeiling = h
		}
	}
	if e.history != nil {
		if samples, rate, err := e.history.RecentStats(ctx, capability, 20); err == nil {
			in.Features.RecentSamples = samples
			in.Features.RecentAcceptRate = rate
		}
		// M8-T10: per-operation counts feed the exploration floor.
		if oph, ok := e.history.(StrategyOperationHistory); ok {
			if m, err := oph.OperationSamples(ctx, capability, 20); err == nil {
				in.Features.OperationSamples = m
			}
		}
	}
	// F4-T7a: an active insight can bias the divergence persona. Proposed
	// insights are excluded by the lister, so they never reach here.
	if e.insights != nil {
		if lister, ok := e.insights.(StrategyInsightLister); ok {
			if pref := e.preferredDivergencePersona(ctx, lister, capability); pref != "" {
				in.Features.PreferredDivergencePersona = pref
			}
		}
	}
	return in
}

// taskAttemptIndex returns the 1-based position of jobID among its task's jobs
// in creation order — the attempt number (ADR-0074). It returns 0 when it
// cannot be determined, which the escalation treats as a first attempt (no
// escalation). The task's job set is stable for the life of a job (the
// scheduler creates the next job only after the current one is terminal), so
// the value is consistent across the phases that call planFor.
func (e *Engine) taskAttemptIndex(ctx context.Context, taskID, jobID string) int {
	if e.jobs == nil || taskID == "" {
		return 0
	}
	js, err := e.jobs.ByTask(ctx, taskID)
	if err != nil {
		return 0
	}
	for i, j := range js {
		if j.ID == jobID {
			return i + 1
		}
	}
	return 0
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
	// ADR-0074 §3: a retry escalates compute up the fixed ladder, bounded by
	// the ceilings. The first attempt (Attempt <= 1) is never escalated, so a
	// first-pass benchmark is unaffected.
	plan = policy.Escalate(plan, in.Features.Attempt, in)
	if e.cfg != nil {
		// F4-T4 quorum: the operator's execution.policy.judge_count is
		// authoritative over any policy-supplied value and applies to the
		// COMPARISON phase's LLM judge, in either judge_mode (llm or
		// verifier). The evaluation phase makes a single advisory judge call
		// (verifier-first selection decides there), so count > 1 decorrelates
		// the accept/reject decision only (known-issues M8).
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
	// M8-T9: the anti-rut reset (ADR-0064 §5). On a novel task class, drop
	// the learned persona bias so specialisation does not steer a genuinely
	// new problem; the plan stays on the shipped default path. Controlled
	// by execution.policy.novelty_reset (auto|reset|keep).
	// M8-T9 / M8-T29: the anti-rut reset and the exploration blend share one
	// novelty_reset mode. `keep` never drops learned bias; `reset` always
	// does; `auto` drops it when the class is novel (the reset) and, even on a
	// familiar class, occasionally (the exploration blend — deterministic per
	// job) so evidence keeps accruing. This is G8's "exploration randomness".
	switch e.noveltyMode() {
	case config.NoveltyResetKeep:
		// keep the learned bias; no reset, no exploration
	case config.NoveltyResetReset:
		plan.NoveltyReset = true
		return plan
	default: // auto
		if plan.Novelty >= policy.NoveltyThreshold {
			plan.NoveltyReset = true
			return plan
		}
		if policy.Explore(in.Features.JobKey, plan.Novelty) {
			plan.Exploration = true
			return plan
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

// noveltyMode resolves the novelty_reset selection ("auto" | "reset" |
// "keep"), defaulting to auto. It gates both the anti-rut reset (M8-T9) and
// the exploration blend (M8-T29).
func (e *Engine) noveltyMode() string {
	if e.cfg != nil {
		return e.cfg.Execution.NoveltyResetSelection()
	}
	return config.NoveltyResetAuto
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

// jobScratchKeys are the per-job `system_state` keys the engine writes during a
// run and must clear at terminal state: the planning difficulty hint, the
// reflection and self-refine counters, and the §10.5 suppression row. They are
// per-job scratch, not durable state; leaving them behind grows `system_state`
// without bound (one or more rows per job).
func jobScratchKeys(jobID string) []string {
	return []string{
		difficultyStateKey(jobID),
		languageStateKey(jobID),
		reflectCounterPrefix + jobID,
		reflectProposalPrefix + jobID,
		refineCounterPrefix + jobID,
		evictionStateKey(jobID),
		analysisPrefix + jobID,
		regulatorSigPrefix + jobID,
		regulatorRefinedPrefix + jobID,
	}
}

// clearJobScratch deletes a terminal job's per-job `system_state` rows so the
// table does not accumulate one set per finished job.
func (e *Engine) clearJobScratch(ctx context.Context, jobID string) {
	if e.db == nil {
		return
	}
	for _, k := range jobScratchKeys(jobID) {
		if _, err := e.db.DB().ExecContext(ctx, `DELETE FROM system_state WHERE key = ?`, k); err != nil {
			slog.Error("engine: clearing job state", "job", jobID, "key", k, "err", err)
		}
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
		"operations":           plan.Operations, // M8-T8: the eligible set
	})
	// F4-T7a: record the feedback→policy bias once, at the divergence stage
	// (the profile-stage plan runs before insights are relevant to the run).
	if stage == "divergence" && plan.InsightBias != "" {
		e.audit(ctx, jobID, map[string]any{
			"event": "policy_biased_from_insight", "persona": plan.InsightBias,
		})
	}
	// M8-T9: record that the anti-rut reset dropped the learned bias.
	if stage == "divergence" && plan.NoveltyReset {
		e.audit(ctx, jobID, map[string]any{
			"event": "novelty_reset", "novelty": plan.Novelty,
		})
	}
	// M8-T29: record that the exploration blend dropped the learned bias.
	if stage == "divergence" && plan.Exploration {
		e.audit(ctx, jobID, map[string]any{
			"event": "exploration", "novelty": plan.Novelty,
		})
	}
}

// isJudgmentPhase reports whether a phase adjudicates outputs and so must not
// spend its token budget on hidden reasoning (F4-T8).
func isJudgmentPhase(phase string) bool {
	return phase == llm.PhaseEvaluating || phase == llm.PhaseComparing
}

// thinkFlag renders a *ThinkMode for the audit log: a level string, a boolean,
// or nil when unset.
func thinkFlag(t *llm.ThinkMode) any {
	if t == nil {
		return nil
	}
	if t.Level != "" {
		return t.Level
	}
	return t.Enabled
}

// resolveThink maps configuration and phase to the wire control. Judgment
// phases are always off (F4-T8); a configured level applies to generation
// only. An empty level keeps the boolean Think setting.
func resolveThink(in config.Inference, phase string) *llm.ThinkMode {
	if isJudgmentPhase(phase) {
		return llm.Off()
	}
	switch in.ThinkLevel {
	case "off":
		return llm.Off()
	case "low", "medium", "high":
		return llm.At(in.ThinkLevel)
	}
	if in.Think != nil && *in.Think {
		return llm.At("")
	}
	return llm.Off()
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
	e.EnqueueContext(context.Background(), jobID)
}

// EnqueueContext is Enqueue with a caller-owned lifetime. Cancelling ctx while
// the job is waiting for a slot abandons the wait cleanly; cancelling it while
// the job is running cancels the phase context (use Cancel to mark the job
// cancelled rather than failed).
func (e *Engine) EnqueueContext(ctx context.Context, jobID string) {
	go func() {
		// A stopped Athanor accepts no new work. The job stays queued, and
		// ResumeAll re-enqueues it once the household mode allows work again.
		if e.operatorGate != nil && !e.operatorGate.Allowed() {
			return
		}
		jobCtx, cancel := context.WithCancel(ctx)
		e.cancelMu.Lock()
		e.cancels[jobID] = cancel
		e.cancelMu.Unlock()
		defer func() {
			cancel()
			e.cancelMu.Lock()
			delete(e.cancels, jobID)
			delete(e.cancelled, jobID)
			e.cancelMu.Unlock()
		}()
		// ADR-0091: reserve the horizon grant before competing for a slot, so
		// a deferred job waits without holding the single execution slot.
		if j, err := e.jobs.Get(jobCtx, jobID); err == nil && j.State == job.StateQueued {
			for {
				ok, wait := e.admitHorizon(jobCtx, jobID)
				if ok {
					break
				}
				timer := time.NewTimer(wait)
				select {
				case <-jobCtx.Done():
					timer.Stop()
					e.finishQueuedCancel(jobID)
					return
				case <-timer.C:
				}
			}
		}
		for {
			if jobCtx.Err() != nil {
				e.finishQueuedCancel(jobID)
				return
			}
			max := e.cap.MaxConcurrentJobs()
			if max < 1 {
				max = 1
			}
			// Try to reserve a slot with a single CAS. If the cap is
			// exceeded, wait cancellably; if the CAS lost a race, retry
			// immediately so we do not burn a wait cycle.
			cur := atomic.LoadInt64(&e.inFlight)
			if cur >= int64(max) {
				timer := time.NewTimer(50 * time.Millisecond)
				select {
				case <-jobCtx.Done():
					timer.Stop()
					e.finishQueuedCancel(jobID)
					return
				case <-timer.C:
				}
				continue
			}
			if !atomic.CompareAndSwapInt64(&e.inFlight, cur, cur+1) {
				continue
			}
			// The slot is reserved: only now is this goroutine allowed to
			// release it. A deferred decrement at goroutine entry would
			// decrement a slot that was never acquired whenever the wait was
			// abandoned, driving inFlight negative and silently raising the
			// cap for the rest of the process lifetime.
			defer atomic.AddInt64(&e.inFlight, -1)
			e.Run(jobCtx, jobID)
			return
		}
	}()
}

// finishQueuedCancel commits `cancelled` for a job that was cancelled while it
// was still waiting for a slot. Cancel marks the user-cancel set before it
// cancels the context, so a caller-owned context cancellation (not a user
// cancel) leaves the job queued for a later Enqueue instead of cancelling it.
func (e *Engine) finishQueuedCancel(jobID string) {
	if !e.wasUserCancelled(jobID) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	j, err := e.jobs.Get(ctx, jobID)
	if err != nil || j.State != job.StateQueued {
		return
	}
	if _, err := e.jobs.Transition(ctx, jobID, job.StateCancelled); err != nil {
		slog.Error("engine: cancelling queued job", "job", jobID, "err", err)
		return
	}
	e.audit(ctx, jobID, map[string]any{
		"event": "job_cancelled", "state": string(j.State), "reason": "queued",
	})
	e.FinalizeTerminal(ctx, jobID)
}

// Cancel asks a job to stop. A job with a live goroutine is marked and its
// context cancelled, so Run commits `cancelled` and finalizes it. A job with
// no goroutine — paused, awaiting approval, or still queued — is cancelled
// directly and finalized here; otherwise those states could not be cancelled
// from the operator surface at all.
func (e *Engine) Cancel(jobID string) bool {
	e.cancelMu.Lock()
	cancel := e.cancels[jobID]
	if cancel != nil {
		e.cancelled[jobID] = true
		e.cancelMu.Unlock()
		cancel()
		return true
	}
	e.cancelMu.Unlock()

	ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	j, err := e.jobs.Get(ctx, jobID)
	if err != nil || j.State.Terminal() {
		return false
	}
	if !job.CanTransition(j.State, job.StateCancelled) {
		return false
	}
	if _, err := e.jobs.Transition(ctx, jobID, job.StateCancelled); err != nil {
		slog.Error("engine: cancelling parked job", "job", jobID, "state", j.State, "err", err)
		return false
	}
	e.audit(ctx, jobID, map[string]any{
		"event": "job_cancelled", "state": string(j.State), "reason": "operator",
	})
	e.FinalizeTerminal(ctx, jobID)
	return true
}

// wasUserCancelled reports whether Cancel marked this job.
func (e *Engine) wasUserCancelled(jobID string) bool {
	e.cancelMu.Lock()
	defer e.cancelMu.Unlock()
	return e.cancelled[jobID]
}

// Pause asks a running job to stop at the phase boundary without becoming
// terminal. It is used by the operator Stopped mode and by "use this
// computer": the phase context is cancelled, Run commits `paused` with the
// current state as paused_from, and a later ResumeAll re-drives it.
func (e *Engine) Pause(jobID string) bool {
	e.cancelMu.Lock()
	cancel := e.cancels[jobID]
	if cancel == nil {
		e.cancelMu.Unlock()
		return false
	}
	e.pauseReq[jobID] = true
	e.cancelMu.Unlock()
	cancel()
	return true
}

// wasPauseRequested reports whether Pause was used for this job.
func (e *Engine) wasPauseRequested(jobID string) bool {
	e.cancelMu.Lock()
	defer e.cancelMu.Unlock()
	return e.pauseReq[jobID]
}

func (e *Engine) clearPauseRequested(jobID string) {
	e.cancelMu.Lock()
	delete(e.pauseReq, jobID)
	e.cancelMu.Unlock()
}

// PauseAll preempts every active job. Queued jobs have no goroutine; the
// operator gate prevents them from starting, and ResumeAll re-enqueues them.
func (e *Engine) PauseAll(ctx context.Context) {
	active, err := e.jobs.Active(ctx)
	if err != nil {
		slog.Error("operator: listing active jobs to pause", "err", err)
		return
	}
	for _, j := range active {
		if j.State.Terminal() || j.State == job.StateQueued {
			continue
		}
		e.Pause(j.ID)
	}
}

// ResumeAll re-drives paused jobs and re-enqueues queued jobs after a stop or
// temporary hold is lifted.
func (e *Engine) ResumeAll(ctx context.Context) {
	e.resumePaused(ctx, "operator")
	active, err := e.jobs.Active(ctx)
	if err != nil {
		slog.Error("operator: listing active jobs to resume", "err", err)
		return
	}
	for _, j := range active {
		if j.State == job.StateQueued {
			e.Enqueue(j.ID)
		}
	}
}

// Recover resumes every active job after a daemon restart or an unfreeze
// (§23.6): running and queued jobs continue from their last committed state.
// A paused job is resumed only when its pause reason is transient (power,
// operator stop, or kill switch reopened) and the gate is currently open;
// manual and context-floor pauses stay parked for the operator. Every parked
// job's pod is stopped so a restart cannot leave resident sandboxes behind.
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
		if j.State == job.StateAwaitingApproval {
			e.stopPod(ctx, j.ID)
			continue
		}
		if j.State == job.StatePaused {
			e.stopPod(ctx, j.ID)
			if !e.resumableOnBoot(ctx, j) {
				continue
			}
			if _, err := e.jobs.Transition(ctx, j.ID, j.PausedFrom); err != nil {
				slog.Error("recovery: resuming paused job", "job", j.ID, "to", j.PausedFrom, "err", err)
				continue
			}
			e.audit(ctx, j.ID, map[string]any{
				"event": "job_resumed", "reason": "recover", "to": string(j.PausedFrom),
			})
		}
		if err := e.jobs.SetRecoveryFlag(ctx, j.ID, "interrupted"); err != nil {
			slog.Error("recovery: marking interrupted", "job", j.ID, "err", err)
		}
		slog.Info("resuming job after restart", "job", j.ID, "state", j.State)
		e.Enqueue(j.ID)
	}
}

// resumableOnBoot reports whether a paused job's reason is transient and its
// gate is open. A manual pause or a context-floor pause is deliberately not
// resumed; the operator must act.
func (e *Engine) resumableOnBoot(ctx context.Context, j job.Job) bool {
	if j.PausedFrom == "" {
		return false
	}
	switch e.lastPauseReason(ctx, j.ID) {
	case "power":
		return e.pauseGate == nil || !e.pauseGate.Paused()
	case "operator_stopped":
		return e.operatorGate == nil || e.operatorGate.Allowed()
	case "kill_switch":
		return e.freezer == nil || !e.freezer.Frozen()
	default:
		return false
	}
}

// lastPauseReason reads the most recent job_paused audit reason for a job.
// A missing event is the manual/context-floor case and is not resumable.
func (e *Engine) lastPauseReason(ctx context.Context, jobID string) string {
	if e.db == nil {
		return ""
	}
	events, err := e.db.QueryEvents(ctx, store.EventFilter{JobID: jobID, Category: "jobs"})
	if err != nil {
		return ""
	}
	for i := len(events) - 1; i >= 0; i-- {
		var d struct {
			Event  string `json:"event"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal([]byte(events[i].DataJSON), &d) != nil {
			continue
		}
		if d.Event == "job_paused" {
			return d.Reason
		}
	}
	return ""
}

// ReapIdlePods stops the Job Pods of parked jobs. It is safe to call
// periodically: a paused or awaiting job does not need a resident sandbox,
// and resuming re-stages it on demand.
func (e *Engine) ReapIdlePods(ctx context.Context) {
	active, err := e.jobs.Active(ctx)
	if err != nil {
		slog.Warn("reaper: listing active jobs", "err", err)
		return
	}
	for _, j := range active {
		if j.State == job.StatePaused || j.State == job.StateAwaitingApproval {
			e.stopPod(ctx, j.ID)
		}
	}
}

// pauseReason returns the active stop reason ("kill_switch", "power", or "")
// so the engine's run loop has a single gate and a post-mortem can tell the
// two apart.
func (e *Engine) pauseReason() string {
	if e.freezer.Frozen() {
		return "kill_switch"
	}
	if e.operatorGate != nil && !e.operatorGate.Allowed() {
		return "operator_stopped"
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
	e.resumePaused(ctx, "power")
}

// resumePaused re-drives paused jobs with a caller-supplied reason, so the
// event log distinguishes a power resume from an operator resume.
func (e *Engine) resumePaused(ctx context.Context, reason string) {
	active, err := e.jobs.Active(ctx)
	if err != nil {
		slog.Error("resume: listing active jobs", "err", err)
		return
	}
	for _, j := range active {
		if j.State != job.StatePaused || j.PausedFrom == "" {
			continue
		}
		if _, err := e.jobs.Transition(ctx, j.ID, j.PausedFrom); err != nil {
			slog.Error("resume: transition", "job", j.ID, "err", err)
			continue
		}
		e.audit(ctx, j.ID, map[string]any{
			"event": "job_resumed", "reason": reason, "to": string(j.PausedFrom),
		})
		slog.Info("resume: re-driving paused job", "job", j.ID, "reason", reason, "to", j.PausedFrom)
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
	// known-issues L26: nil-safe like the other seams. A mis-wired Engine
	// drops the audit row rather than panicking; audit is best-effort.
	if e.db == nil {
		return
	}
	// A phase deadline cancels ctx while the phase is unwinding; an audit row
	// written during that unwind is still important (it explains why the
	// phase failed). Detach cancellation but keep the context values, and
	// bound the write so a stuck DB cannot hold the job goroutine.
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := e.db.AppendEvent(actx, store.Event{Category: category, JobID: jobID, Data: data}); err != nil {
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
		// A pause request that raced a naturally completed phase must not
		// poison a later user cancel.
		e.clearPauseRequested(jobID)
	}()
	// A panic in a phase is contained to this job. Without this, a nil map,
	// a bad index, or a parser panic in any phase would kill the daemon —
	// the opposite of the §23 endurance posture. The handler commits the
	// failure, records a correction, and tears the pod down; the process
	// keeps running and the job is diagnosable from the event log.
	defer func() {
		if r := recover(); r != nil {
			e.handleJobPanic(jobID, r)
		}
	}()

	for {
		j, err := e.jobs.Get(ctx, jobID)
		if err != nil {
			slog.Error("engine: loading job", "job", jobID, "err", err)
			return
		}
		if j.State.Terminal() {
			e.FinalizeTerminal(ctx, jobID)
			return
		}
		if j.State == job.StatePaused || j.State == job.StateAwaitingApproval {
			// A parked job is resumable, not running. Its Job Pod must not
			// stay resident while it waits for a human, a power event, or a
			// budget window; resume re-stages it.
			e.stopPod(ctx, jobID)
			return
		}

		// ADR-0091: a direct Run (Recover, tests) may not have gone through
		// Enqueue's admission. Reserve before the first phase; a deferred job
		// stays queued.
		if j.State == job.StateQueued {
			if ok, _ := e.admitHorizon(ctx, jobID); !ok {
				return
			}
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
			e.stopPod(ctx, jobID)
			return
		}

		// M6-T10 (§13.3): capture the strategy profile once, at start.
		if j.State == job.StateQueued {
			e.captureProfile(ctx, j)
		}

		if err := e.step(ctx, j); err != nil {
			if errors.Is(err, ErrPaused) {
				e.stopPod(ctx, jobID)
				return
			}
			// An external state write can land while a phase is running:
			// the UI's pause control, a HITL resume, or a concurrent cancel.
			// The phase's next transition then fails its compare-and-swap or
			// the paused-resume invariant. Re-read before treating the error
			// as a job failure — the job is not broken, it was moved. Without
			// this, pausing a running job from the UI reliably failed it.
			if cur, gerr := e.jobs.Get(ctx, jobID); gerr == nil {
				switch {
				case cur.State == job.StatePaused || cur.State == job.StateAwaitingApproval:
					e.audit(ctx, jobID, map[string]any{
						"event": "phase_aborted_by_external_state",
						"phase": string(j.State), "to": string(cur.State),
						"error": err.Error(),
					})
					e.stopPod(ctx, jobID)
					return
				case cur.State.Terminal():
					e.FinalizeTerminal(ctx, jobID)
					return
				}
			}
			// An operator stop or a temporary hold cancels the phase and parks
			// the job in `paused`; it is resumable, not cancelled.
			if errors.Is(err, context.Canceled) && e.wasPauseRequested(jobID) {
				cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				cur, gerr := e.jobs.Get(cctx, jobID)
				if gerr == nil && job.CanTransition(cur.State, job.StatePaused) {
					if _, terr := e.jobs.Transition(cctx, jobID, job.StatePaused); terr == nil {
						e.audit(cctx, jobID, map[string]any{
							"event": "job_paused", "reason": "operator", "from": string(cur.State),
						})
					}
				}
				e.stopPod(cctx, jobID)
				e.clearPauseRequested(jobID)
				cancel()
				return
			}
			// A user Cancel is a terminal `cancelled`, not a failure. The
			// phase context is already canceled, so commit on a detached,
			// bounded context; the pod teardown follows below.
			if errors.Is(err, context.Canceled) && e.wasUserCancelled(jobID) {
				cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				if _, terr := e.jobs.Transition(cctx, jobID, job.StateCancelled); terr != nil {
					slog.Error("engine: marking job cancelled", "job", jobID, "err", terr)
				} else {
					e.audit(cctx, jobID, map[string]any{
						"event": "job_cancelled", "state": string(j.State),
					})
					e.FinalizeTerminal(cctx, jobID)
				}
				cancel()
				return
			}
			slog.Error("engine: phase failed", "job", jobID, "state", j.State, "err", err)
			if _, terr := e.jobs.Transition(ctx, jobID, job.StateFailed); terr != nil {
				slog.Error("engine: marking job failed", "job", jobID, "err", terr)
			} else {
				// M6-T6 records the failure as a CorrectionRecord (§18.1).
				e.recordFailureCorrection(ctx, jobID, j.ProjectID, j.State, err)
				// One terminal path owns outcome/process capture, scratch
				// cleanup, budget settlement, scheduler notification, and pod
				// teardown. A failure is terminal too.
				e.FinalizeTerminal(ctx, jobID)
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

// FinalizeTerminal runs the one-time teardown for any terminal job. It is the
// public seam external writers (HITL rejection/expiry, the UI) use after they
// commit a terminal transition; it is idempotent enough to run more than once.
func (e *Engine) FinalizeTerminal(ctx context.Context, jobID string) {
	j, err := e.jobs.Get(ctx, jobID)
	if err != nil {
		slog.Error("engine: loading terminal job", "job", jobID, "err", err)
		return
	}
	if !j.State.Terminal() {
		return
	}
	e.finalizeTerminal(ctx, jobID, j)
}

// finalizeTerminal performs the one-time teardown for a job that has reached a
// terminal state. Order matters: captureOutcome reads the per-job reflection
// counter from system_state, so scratch must not be cleared until after the
// outcome is written.
func (e *Engine) finalizeTerminal(ctx context.Context, jobID string, j job.Job) {
	// ADR-0114: leave a bounded deterministic summary for later projects/jobs.
	e.rememberCompletedJob(ctx, j)
	// ADR-0119: evaluate the trajectory itself, not only the final artifact.
	e.evaluateProcess(ctx, j)
	// M6-T10 (§13.3): capture the immutable outcome before any teardown that
	// can block and before clearJobScratch removes the reflection counter.
	e.captureOutcome(ctx, jobID)
	// M5-T5 (ADR-0023 §5): a finished job's evictions are history, not state.
	// Clearing here (and only here) keeps `system_state` bounded while a
	// paused job keeps its suppression for the resume.
	e.clearJobScratch(ctx, jobID)
	// ADR-0091: close the horizon grant with actual spend before any teardown
	// that can block.
	e.settleHorizon(ctx, jobID)
	// M6-T2 (ADR-0033): let the scheduler advance the graph.
	e.signalTerminal(ctx, jobID, j.State)
	// M2-T4b.5 (ADR-0024 §2): a terminal job's Job Pod has no further use;
	// stop it (idempotent, nil-safe).
	e.stopPod(ctx, jobID)
	// The per-job generator family is only needed for the verifier cast.
	e.castMu.Lock()
	delete(e.jobFamily, jobID)
	e.castMu.Unlock()
}

// handleJobPanic contains a panic from one job's goroutine. It records the
// panic and the phase it happened in, fails the job if it is still active,
// captures the runtime-error correction, and stops the job's pod. The daemon
// itself keeps running: a single bad phase must not take down work that is
// already in flight or queued.
//
// The context is detached from the (possibly canceled) phase context so the
// post-mortem rows are still written, and bounded so a wedged database cannot
// hold the goroutine indefinitely. A second panic in this handler is logged
// by the deferred guard rather than escaping.
func (e *Engine) handleJobPanic(jobID string, recovered any) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stack := debug.Stack()
	if len(stack) > 8<<10 {
		stack = stack[:8<<10]
	}
	slog.Error("engine: job panicked",
		"job", jobID, "panic", fmt.Sprint(recovered), "stack", string(stack))

	defer func() {
		if r := recover(); r != nil {
			slog.Error("engine: panic handler itself failed", "job", jobID, "panic", fmt.Sprint(r))
		}
	}()

	j, err := e.jobs.Get(ctx, jobID)
	if err != nil {
		slog.Error("engine: loading panicked job", "job", jobID, "err", err)
		e.stopPod(ctx, jobID)
		return
	}
	e.audit(ctx, jobID, map[string]any{
		"event": "job_panicked", "phase": string(j.State), "panic": fmt.Sprint(recovered),
	})
	if !j.State.Terminal() {
		if _, terr := e.jobs.Transition(ctx, jobID, job.StateFailed); terr != nil {
			slog.Error("engine: failing panicked job", "job", jobID, "err", terr)
		} else {
			e.recordFailureCorrection(ctx, jobID, j.ProjectID, j.State,
				fmt.Errorf("panic: %v", recovered))
			e.FinalizeTerminal(ctx, jobID)
		}
	} else {
		e.FinalizeTerminal(ctx, jobID)
	}
	e.stopPod(ctx, jobID)
}
