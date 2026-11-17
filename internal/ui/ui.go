// Package ui serves Athanor's local web UI (ROADMAP M6-T8; ARCHITECTURE §27)
// on the Core's loopback listener. It uses no third-party UI framework:
// html/template, SSE, and net/http, with vendored HTMX as a static asset.
//
// Pages: Dashboard (jobs + pending approvals + power/freeze), Projects
// (artifact history + diffs + corrections), Corrections, Job Watch (live
// phase events + streamed tokens + interruption notes + controls), Reviews,
// and the read-only inspector (events, artifacts, memory, daydream, runtime,
// and one job's full trace). Live updates use Server-Sent Events:
// /ui/events/stream for the dashboard and /ui/jobs/{id}/stream for the watch
// view.
package ui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tcs76321/athanor/internal/architect"
	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/assist"
	"github.com/tcs76321/athanor/internal/composition"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/corrections"
	"github.com/tcs76321/athanor/internal/cycle"
	"github.com/tcs76321/athanor/internal/doctor"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/firstuse"
	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/intake"
	"github.com/tcs76321/athanor/internal/interruptions"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/operator"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/review"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/strategy"
	"github.com/tcs76321/athanor/internal/trigger"
)

// Freezer reports the §22 kill-switch state.
type Freezer interface{ Frozen() bool }

// Operator is the household-mode surface on the dashboard.
type Operator interface {
	Status() operator.Status
	Set(ctx context.Context, mode operator.Mode, hold time.Duration, reason string) error
}

// Assist refines a user-authored field for its downstream purpose (ADR-0105
// authoring assist). It returns a proposal; the UI applies it only on request.
type Assist interface {
	Refine(ctx context.Context, field, text, mode string) (assist.Result, error)
}

// Control is the optional kill-switch write surface (§22). Satisfied by
// *control.KillSwitch; nil leaves the Settings control read-only.
type Control interface {
	Frozen() bool
	Freeze(ctx context.Context) error
	Unfreeze(ctx context.Context, reason string) error
}

// Exporter writes one accepted artifact through the egress pipeline.
// Satisfied by *egress.Exporter.
type Exporter interface {
	ExportOne(ctx context.Context, artifactID string) (path string, exported bool, err error)
}

// Hardware is the host capacity reported to the resources step.
type Hardware struct {
	// TotalMemoryBytes is the memory available to the container runtime (the
	// Podman VM on macOS). Job Pod limits are derived from it.
	TotalMemoryBytes uint64
	// HostMemoryBytes is the physical host's memory. Ollama runs on the host,
	// so model-fit budgets use this when it is known. It falls back to
	// TotalMemoryBytes in host mode and on hosts where the Core can see the
	// full machine directly.
	HostMemoryBytes uint64
	FreeDiskBytes   uint64
	CPUs            int
	// Accelerator is the host GPU/NPU probe result. The Core Pod cannot see
	// the host device, so `athanor start` passes it in. Nil means "not
	// probed"; a zero-value GPUInfo with only NPU set is inventory-only.
	Accelerator *doctor.GPUInfo
}

// TaskResetter is the operator recovery surface for blocked tasks.
type TaskResetter interface {
	Reset(ctx context.Context, taskID string) ([]string, error)
}

// Enqueuer starts a job (the engine). Used by retry/resume.
type Enqueuer interface{ Enqueue(jobID string) }

// RepoPreindexer runs one bounded repository index pass. It is optional: a UI
// without one still creates projects, but a repo-backed first job starts with
// an empty Dormant Index.
type RepoPreindexer interface {
	Preindex(ctx context.Context, projectID, path string) error
}

// AlarmResolver clears an active alarm from the operator surface. Satisfied by
// *alarms.Service; nil hides the resolve control.
type AlarmResolver interface {
	Resolve(ctx context.Context, id string) error
}

// Pusher files a HITL request for a git push. It never pushes directly; the
// approved side effect runs through the HITL approver (ADR-0036).
type Pusher interface {
	RequestPush(ctx context.Context, projectID, remote string) (hitl.Request, error)
}

// uiPreindexer adapts the UI's error-only pre-index seam to firstuse's
// counter-returning one. The UI does not render the counters; the service still
// audits them.
type uiPreindexer struct{ inner RepoPreindexer }

func (u uiPreindexer) IndexOnceSummary(ctx context.Context, projectID, path string) (firstuse.Summary, error) {
	return firstuse.Summary{}, u.inner.Preindex(ctx, projectID, path)
}

// Deps wires the UI to the Core's stores and services.
type Deps struct {
	Store         *store.Store
	Projects      *project.Repo
	Jobs          *job.Repository
	Artifacts     *artifact.Store
	Corrections   *corrections.Repo
	Reviews       *review.Repo
	Eval          *evaluation.Repo
	HITL          *hitl.Service
	HITLRepo      *hitl.Repo
	Interruptions *interruptions.Repo
	Strategy      *strategy.Repo
	Freezer       Freezer
	Control       Control
	Operator      Operator
	Assist        Assist
	Pool          *composition.Pool
	Engine        Enqueuer
	// Config is read-only. The inspector shows personas, limits, and the
	// budget shape; it never writes configuration.
	Config *config.Config
	// Runtime and Version identify the process serving the UI: "Core Pod" in
	// the supported runtime, "host (development)" under `serve`. They are
	// display-only.
	Runtime string
	Version string
	// Indexer runs the bounded pre-index on the first-use project create.
	// nil is valid: the job starts without a populated Dormant Index.
	Indexer RepoPreindexer
	// Intake is the guided project-intake service (ADR-0132). It is the
	// canonical state machine behind the onboarding pages.
	Intake *intake.Service
	// Cycles is the project timeline repository (ADR-0134). The canvas reads
	// and edits a cycle's draft graph; Starter compiles and starts it.
	Cycles    *cycle.Repo
	Starter   firstuse.Starter
	Architect *architect.Service
	// Triggers records a project's cycle schedules (ADR-0134). nil disables
	// schedule persistence; the cycle still works as a manual run.
	Triggers *trigger.Repo
	// ModelAudit is the measured model-fit JSON passed by `athanor start`.
	// Empty means no audit was available.
	ModelAudit string
	// Doctor runs the §30.2 host/environment checks for the first-run wizard.
	// nil falls back to the UI-native checks.
	Doctor func(ctx context.Context) doctor.Report
	// ConfigPath is the file the onboarding may update when applying an
	// automatic persona assignment. Empty disables write-back.
	ConfigPath string
	// Hardware reports host capacity for the resources step. nil leaves the
	// step with zero/unknown values and recommended defaults.
	Hardware func(ctx context.Context) Hardware
	// Restart asks the supervisor to restart the Core with the newly written
	// configuration. nil leaves the change marked restart-required (host
	// development mode has no supervisor).
	Restart func()
	// TaskResetter returns a blocked task and its blocked dependents to
	// pending. nil hides the reset control.
	TaskResetter TaskResetter
	// Alarms clears an active alarm. nil hides the resolve control.
	Alarms AlarmResolver
	// Pusher files a HITL request for a git push. nil hides the push control.
	Pusher Pusher
	// DefaultTTL is applied to defer decisions made without an explicit
	// duration.
	DefaultTTL time.Duration
}

// UI serves the web interface.
type UI struct {
	deps Deps
	hub  *Hub
	// qualMu/qualRunning guard the one in-process qualification worker.
	qualMu      sync.Mutex
	qualRunning bool
	// qualStopping is true after Stop until the worker has actually exited.
	// A depth choice during that window waits for the old worker instead of
	// silently doing nothing.
	qualStopping bool
	qualCancel   context.CancelFunc
	// qualDone closes when the current worker returns. It is the handoff that
	// lets a start wait out a cancelled worker.
	qualDone chan struct{}
	// qualTier is the live target depth. The worker re-reads it between
	// cases, so choosing Balanced or Complete mid-run takes effect without
	// waiting for the Minimal pass to finish.
	qualTier string
	// qualLastSave throttles per-case state writes. The state includes the
	// full case evidence, so writing it on every case would make a long screen
	// spend much of its time serializing JSON.
	qualLastSave time.Time
	// themeMu guards the process-wide UI theme. The Core is a single-user
	// loopback service, so one theme is the right scope; it is persisted in
	// system_state and applied server-side so first paint is correct without
	// an inline script (CSP-safe).
	themeMu sync.RWMutex
	theme   string
	density string
	// onboardMu guards the in-memory model-pull progress shown by the
	// first-run wizard. The durable onboarding step lives in system_state.
	onboardMu   sync.Mutex
	pulls       map[string]*pullProgress
	pullCancels map[string]context.CancelFunc
	// catalogMu caches the installed-model catalog. The onboarding and models
	// pages render it on every load, and the qualification poll reloads the
	// page every 2s; without a TTL every render performed one /api/tags call
	// plus one /api/show call per installed model, contending with the very
	// inference the page was measuring.
	catalogMu   sync.Mutex
	catalog     []setupModel
	catalogErr  string
	catalogAt   time.Time
	catalogBase string
	// indexMu/indexRuns track the bounded repository index pass started from
	// project create or the project page. Progress is in-memory (the durable
	// manifest is the indexer's own state); a Core restart simply shows no
	// run and offers the button again.
	indexMu   sync.Mutex
	indexRuns map[string]*indexRun
	// statusMu caches the shell's status strip (freeze, active runs, waiting
	// approvals). Rendering it from live queries on every page load would add
	// three queries to every navigation; a few seconds of staleness is the
	// right tradeoff for a status hint.
	statusMu    sync.Mutex
	statusAt    time.Time
	statusFroze bool
	statusRun   int
	statusWait  int
	// exporter is optional: the egress pipeline is wired after the UI is
	// constructed, so it is a setter rather than a New() field.
	exporter Exporter
	// done is closed by Close at daemon shutdown. SSE handlers select on it
	// so http.Server.Shutdown does not wait for a browser tab to disconnect.
	done      chan struct{}
	closeOnce sync.Once
}

// New builds a UI.
func New(deps Deps) *UI {
	u := &UI{
		deps: deps, hub: NewHub(), theme: "monokai", density: "comfortable",
		pulls: map[string]*pullProgress{}, pullCancels: map[string]context.CancelFunc{},
		indexRuns: map[string]*indexRun{}, done: make(chan struct{}),
	}
	if saved, ok := u.loadSystemState(context.Background(), "ui:theme"); ok && validTheme(saved) {
		u.theme = saved
	}
	if saved, ok := u.loadSystemState(context.Background(), "ui:density"); ok && validDensity(saved) {
		u.density = saved
	}
	u.reconcileInterruptedQualification()
	return u
}

// Close stops the UI's long-lived SSE handlers. It is idempotent and must be
// called before http.Server.Shutdown: Shutdown waits for active handlers and
// does not cancel their request contexts, so an open watch/dashboard stream
// would otherwise hit the shutdown deadline.
func (u *UI) Close() {
	u.closeOnce.Do(func() { close(u.done) })
}

// Theme returns the active theme id.
func (u *UI) Theme() string {
	u.themeMu.RLock()
	defer u.themeMu.RUnlock()
	return u.theme
}

// Density is the active reading density ("comfortable" or "compact").
func (u *UI) Density() string {
	u.themeMu.RLock()
	defer u.themeMu.RUnlock()
	if u.density == "" {
		return "comfortable"
	}
	return u.density
}

func validDensity(id string) bool { return id == "comfortable" || id == "compact" }

// setDensity validates, stores, and persists the reading density.
func (u *UI) setDensity(id string) error {
	if !validDensity(id) {
		return fmt.Errorf("unknown density %q", id)
	}
	u.themeMu.Lock()
	u.density = id
	u.themeMu.Unlock()
	if err := u.saveSystemState(context.Background(), "ui:density", id); err != nil {
		return fmt.Errorf("persisting density: %w", err)
	}
	return nil
}

// setTheme validates, stores, and persists a theme.
func (u *UI) setTheme(id string) error {
	if !validTheme(id) {
		return fmt.Errorf("unknown theme %q", id)
	}
	u.themeMu.Lock()
	u.theme = id
	u.themeMu.Unlock()
	if err := u.saveSystemState(context.Background(), "ui:theme", id); err != nil {
		return fmt.Errorf("persisting theme: %w", err)
	}
	return nil
}

// Hub implements the engine's token sink and fans chunks out to watch-view
// subscribers. Slow subscribers drop chunks rather than block the engine.
func (u *UI) Hub() *Hub { return u.hub }

// SetExporter wires the egress pipeline after construction. A nil exporter
// leaves the artifact Export action hidden/disabled.
func (u *UI) SetExporter(e Exporter) { u.exporter = e }
