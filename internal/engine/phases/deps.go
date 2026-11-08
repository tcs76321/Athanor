// Package phases owns the seven §13.1 phase orchestrators: planning,
// divergence, evaluation, reflection, synthesis, and comparison, plus the
// phase-local operations they need (verification, pod wiring, research,
// language, fixtures, and the repair loop).
//
// The package reads runtime services through a consumer-defined port (Runtime)
// and holds only fixed-at-construction repositories. It never imports the
// engine root and never opens a database transaction itself (ADR-0131).
package phases

import (
	"context"
	"errors"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/engine/acceptance"
	"github.com/tcs76321/athanor/internal/engine/jobstate"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/notebook"
	"github.com/tcs76321/athanor/internal/policy"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
	"github.com/tcs76321/athanor/internal/regulator"
	"github.com/tcs76321/athanor/internal/toolenvelope"
	"github.com/tcs76321/athanor/internal/verify"
)

// CallRequest is one phase's LLM call. SeedSalt decorrelates a judge quorum's
// votes (known-issues M7).
type CallRequest struct {
	Job          job.Job
	Project      project.Project
	Task         project.Task
	Phase        string
	Role         string
	Instructions string
	Candidates   []prompt.CandidateArtifact
	SeedSalt     int
}

// callOption tunes one call without changing every call site.
type callOption func(*CallRequest)

// withSeedSalt folds a per-vote salt into the derived judgment seed.
func withSeedSalt(n int) callOption { return func(r *CallRequest) { r.SeedSalt = n } }

// Runtime is the phase package's port onto the engine. The root implements it
// on an unexported adapter; the type never appears in a public signature.
type Runtime interface {
	Call(ctx context.Context, r CallRequest) (llm.Response, error)
	Plan(ctx context.Context, j job.Job, p project.Project, t project.Task) policy.Plan
	RoleFor(plan policy.Plan, phase, fallback string) string
	Contexts(ctx context.Context, j job.Job) (project.Project, project.Task, error)
	Audit(ctx context.Context, jobID string, data map[string]any)
	AuditComputePlan(ctx context.Context, jobID, stage string, plan policy.Plan)
	AuditVerification(ctx context.Context, jobID, where string, r verify.Result)
	Note(ctx context.Context, jobID, projectID string, kind notebook.Kind, phase, content string)
	AskForClarification(ctx context.Context, jobID, projectID, phase, content string) bool
	OneShotFirst(ctx context.Context, jobID string) bool
	MinContextFor(phase, capability, demand string) int
	SelectDivergenceRoles(ctx context.Context, j job.Job, p project.Project, t project.Task, n int) []string
	PreferPanel() bool
	UpdateProfileFeatures(ctx context.Context, j job.Job, t project.Task)
	EnsurePod(ctx context.Context, j job.Job) error
	ToolRunner() ToolRunner
	Verifiers() *verify.Registry
	RemainingHorizon(ctx context.Context, jobID string) (int64, bool, error)
	CaptureEvaluationCorrection(ctx context.Context, projectID, jobID, artifactID string, rec evaluation.Record, in verify.Input)
	CaptureRegulatorCorrection(ctx context.Context, projectID, jobID string, r regulator.Result)
	CaptureComparisonCorrection(ctx context.Context, projectID, jobID, artifactID string, v acceptance.Verdict)
	RecordGitCommit(ctx context.Context, p project.Project, art artifact.Artifact)
	EventStats(ctx context.Context, jobID string) (tokens int, confidence float64)
	MaxEvaluationScore(ctx context.Context, jobID string) float64
	JobTokenCost(ctx context.Context, jobID string) int
	ActualCrossFamily(ctx context.Context, j job.Job, p project.Project, t project.Task, generatorRole, judgeRole string) (gen, judge string, ok bool)
}

// ToolRunner is the phase package's window onto the Job Pod internal API.
type ToolRunner interface {
	RunCode(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error)
	RunTests(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error)
	RunLint(ctx context.Context, jobID string, req toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error)
	ReadFile(ctx context.Context, jobID string, req toolenvelope.ReadFileRequest) (toolenvelope.ReadFileResponse, error)
	FetchURL(ctx context.Context, jobID string, req toolenvelope.FetchURLRequest) (toolenvelope.FetchURLResponse, error)
}

// Deps is everything a phase needs. Config, repositories, and the runner are
// fixed at construction; Runtime is live so post-construction seam wiring still
// takes effect.
type Deps struct {
	Cfg       *config.Config
	Runtime   Runtime
	State     *jobstate.Store
	Jobs      *job.Repository
	Projects  *project.Repo
	Artifacts *artifact.Store
	Eval      *evaluation.Repo
}

// Step runs the phase named by the job's current state. The root handles
// queued → context_building → planning so lifecycle transitions stay there.
func Step(ctx context.Context, d *Deps, j job.Job) error {
	switch j.State {
	case job.StatePlanning:
		return d.phasePlan(ctx, j)
	case job.StateDiverging:
		return d.phaseDivergeN(ctx, j)
	case job.StateEvaluating:
		return d.phaseEvaluate(ctx, j)
	case job.StateReflecting:
		return d.phaseReflect(ctx, j)
	case job.StateSynthesizing:
		return d.phaseSynthesize(ctx, j)
	case job.StateComparing:
		return d.phaseCompare(ctx, j)
	default:
		return &UnhandledStateError{State: j.State}
	}
}

// ErrPaused reports that the job was paused instead of failed — a context
// floor violation (recommend-or-escalate, §12.3), a clarification question, or
// the kill switch freezing the daemon mid-run. The root re-exports it so
// errors.Is(engine.ErrPaused) keeps working.
var ErrPaused = errors.New("job paused")

// ErrRepositorySnapshot reports that a repository-backed code job could not
// stage the repository it must test against.
var ErrRepositorySnapshot = errors.New("repository snapshot unavailable")

// UnhandledStateError reports a state the phase package does not own.
type UnhandledStateError struct{ State job.State }

func (e *UnhandledStateError) Error() string {
	return "phases: no handler for state " + string(e.State)
}

// Wrappers keep the phase bodies readable while routing through the port.

func (d *Deps) call(ctx context.Context, j job.Job, p project.Project, t project.Task,
	phase, role, instructions string, candidates []prompt.CandidateArtifact, opts ...callOption) (llm.Response, error) {
	req := CallRequest{Job: j, Project: p, Task: t, Phase: phase, Role: role, Instructions: instructions, Candidates: candidates}
	for _, o := range opts {
		o(&req)
	}
	return d.Runtime.Call(ctx, req)
}

func (d *Deps) planFor(ctx context.Context, j job.Job, p project.Project, t project.Task) policy.Plan {
	return d.Runtime.Plan(ctx, j, p, t)
}

func (d *Deps) roleFor(plan policy.Plan, phase, fallback string) string {
	return d.Runtime.RoleFor(plan, phase, fallback)
}

func (d *Deps) contexts(ctx context.Context, j job.Job) (project.Project, project.Task, error) {
	return d.Runtime.Contexts(ctx, j)
}

func (d *Deps) audit(ctx context.Context, jobID string, data map[string]any) {
	if d.Runtime != nil {
		d.Runtime.Audit(ctx, jobID, data)
	}
}

func (d *Deps) auditComputePlan(ctx context.Context, jobID, stage string, plan policy.Plan) {
	d.Runtime.AuditComputePlan(ctx, jobID, stage, plan)
}

func (d *Deps) auditVerification(ctx context.Context, jobID, where string, r verify.Result) {
	d.Runtime.AuditVerification(ctx, jobID, where, r)
}

func (d *Deps) note(ctx context.Context, jobID, projectID string, kind notebook.Kind, phase, content string) {
	d.Runtime.Note(ctx, jobID, projectID, kind, phase, content)
}

func (d *Deps) askForClarification(ctx context.Context, jobID, projectID, phase, content string) bool {
	return d.Runtime.AskForClarification(ctx, jobID, projectID, phase, content)
}

func (d *Deps) ensurePod(ctx context.Context, j job.Job) error {
	return d.Runtime.EnsurePod(ctx, j)
}

func (d *Deps) runner() ToolRunner {
	if d.Runtime == nil {
		return nil
	}
	return d.Runtime.ToolRunner()
}

func (d *Deps) verifierRegistry() *verify.Registry {
	if d.Runtime == nil {
		return verify.Default()
	}
	if d.Runtime.Verifiers() == nil {
		return verify.Default()
	}
	return d.Runtime.Verifiers()
}

func (d *Deps) actualCrossFamily(ctx context.Context, j job.Job, p project.Project, t project.Task, genRole, judgeRole string) (string, string, bool) {
	return d.Runtime.ActualCrossFamily(ctx, j, p, t, genRole, judgeRole)
}

func (d *Deps) eventStats(ctx context.Context, jobID string) (int, float64) {
	return d.Runtime.EventStats(ctx, jobID)
}

func (d *Deps) maxEvaluationScore(ctx context.Context, jobID string) float64 {
	return d.Runtime.MaxEvaluationScore(ctx, jobID)
}

func (d *Deps) jobTokenCost(ctx context.Context, jobID string) int {
	return d.Runtime.JobTokenCost(ctx, jobID)
}

func (d *Deps) captureEvaluationCorrection(ctx context.Context, projectID, jobID, artifactID string, rec evaluation.Record, in verify.Input) {
	d.Runtime.CaptureEvaluationCorrection(ctx, projectID, jobID, artifactID, rec, in)
}

func (d *Deps) captureRegulatorCorrection(ctx context.Context, projectID, jobID string, r regulator.Result) {
	d.Runtime.CaptureRegulatorCorrection(ctx, projectID, jobID, r)
}

func (d *Deps) captureComparisonCorrection(ctx context.Context, projectID, jobID, artifactID string, v acceptance.Verdict) {
	d.Runtime.CaptureComparisonCorrection(ctx, projectID, jobID, artifactID, v)
}

func (d *Deps) recordGitCommit(ctx context.Context, p project.Project, art artifact.Artifact) {
	d.Runtime.RecordGitCommit(ctx, p, art)
}

// Exported seams for the root's behavioral tests. They are internal to the
// module and exist so the integration suite can exercise one phase without
// re-implementing the harness in this package.
func (d *Deps) VerifyCandidate(ctx context.Context, j job.Job, p project.Project, t project.Task, content string, idx int) (verify.Result, verify.Input, error) {
	return d.verifyCandidate(ctx, j, p, t, content, idx)
}

func (d *Deps) BestPassingCandidate(ctx context.Context, j job.Job) (artifact.Artifact, bool, error) {
	return d.bestPassingCandidate(ctx, j)
}

func (d *Deps) ResearchContext(ctx context.Context, j job.Job, p project.Project, t project.Task) (string, error) {
	return d.researchContext(ctx, j, p, t)
}

func (d *Deps) PhaseCompare(ctx context.Context, j job.Job) error {
	return d.phaseCompare(ctx, j)
}

func (d *Deps) OneShotFirst(ctx context.Context, jobID string) bool {
	return d.oneShotFirst(ctx, jobID)
}

func (d *Deps) ObserveForRepair(ctx context.Context, j job.Job, p project.Project, t project.Task, role string) {
	d.observeForRepair(ctx, j, p, t, role)
}

func (d *Deps) RunPairwiseJudge(ctx context.Context, j job.Job, p project.Project, t project.Task, judgeRole, current, previous string) (acceptance.Verdict, bool, error) {
	return d.runPairwiseJudge(ctx, j, p, t, judgeRole, current, previous)
}
