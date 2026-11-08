package engine

import (
	"context"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/engine/acceptance"
	"github.com/tcs76321/athanor/internal/engine/phases"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/notebook"
	"github.com/tcs76321/athanor/internal/policy"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/regulator"
	"github.com/tcs76321/athanor/internal/verify"
)

// phaseHost is the unexported bridge between the engine and the phase package.
// Every method reads the current engine state; the type is unexported, so the
// phase port never becomes public API.
type phaseHost struct{ e *Engine }

var _ phases.Runtime = phaseHost{}

func (h phaseHost) Call(ctx context.Context, r phases.CallRequest) (llm.Response, error) {
	return h.e.call(ctx, r.Job, r.Project, r.Task, r.Phase, r.Role, r.Instructions, r.Candidates, r.SeedSalt)
}

func (h phaseHost) Plan(ctx context.Context, j job.Job, p project.Project, t project.Task) policy.Plan {
	return h.e.planFor(ctx, j, p, t)
}

func (h phaseHost) RoleFor(plan policy.Plan, phase, fallback string) string {
	return h.e.roleFor(plan, phase, fallback)
}

func (h phaseHost) Contexts(ctx context.Context, j job.Job) (project.Project, project.Task, error) {
	t, err := h.e.projects.Task(ctx, j.TaskID)
	if err != nil {
		return project.Project{}, project.Task{}, err
	}
	p, err := h.e.projects.Get(ctx, t.ProjectID)
	if err != nil {
		return project.Project{}, project.Task{}, err
	}
	return p, t, nil
}

func (h phaseHost) Audit(ctx context.Context, jobID string, data map[string]any) {
	h.e.audit(ctx, jobID, data)
}

func (h phaseHost) AuditCat(ctx context.Context, jobID, category string, data map[string]any) {
	h.e.auditCat(ctx, jobID, category, data)
}

func (h phaseHost) AuditComputePlan(ctx context.Context, jobID, stage string, plan policy.Plan) {
	h.e.auditComputePlan(ctx, jobID, stage, plan)
}

func (h phaseHost) AuditVerification(ctx context.Context, jobID, where string, r verify.Result) {
	h.e.audit(ctx, jobID, map[string]any{
		"event": "verification", "where": where,
		"applied": r.Applied, "passed": r.Passed, "score": r.Score,
		"verifiers": r.Verifiers, "reasons": r.Reasons,
	})
}

func (h phaseHost) Note(ctx context.Context, jobID, projectID string, kind notebook.Kind, phase, content string) {
	h.e.note(ctx, jobID, projectID, kind, phase, content)
}

func (h phaseHost) AskForClarification(ctx context.Context, jobID, projectID, phase, content string) bool {
	return h.e.askForClarification(ctx, jobID, projectID, phase, content)
}

func (h phaseHost) OneShotFirst(ctx context.Context, jobID string) bool {
	if h.e.cfg == nil || !h.e.cfg.Execution.OneShotFirstEnabled() {
		return false
	}
	if h.e.eval == nil {
		return true
	}
	records, err := h.e.eval.ListByJob(ctx, jobID)
	if err != nil {
		return false
	}
	return len(records) == 0
}

func (h phaseHost) MinContextFor(phase, capability, demand string) int {
	return h.e.minContextFor(phase, capability, demand)
}

func (h phaseHost) SelectDivergenceRoles(ctx context.Context, j job.Job, p project.Project, t project.Task, n int) []string {
	if h.e.selector == nil {
		return nil
	}
	capability := project.CapabilityFor(p, t)
	minContext := h.e.minContextFor(llm.PhaseDiverging, capability, project.ContextDemand(p, t))
	req := ModelRequest{
		Phase: llm.PhaseDiverging, Capability: capability, Stance: "generate",
		MinContext: minContext, ProjectID: p.ID, JobID: j.ID,
		Scope: "project:" + p.ID,
	}
	modelCount := n
	if modelCount > 3 {
		modelCount = 3
	}
	choices := h.e.selector.ChooseMany(ctx, req, modelCount)
	if len(choices) == 0 {
		return nil
	}
	roles := make([]string, 0, len(choices))
	for _, c := range choices {
		roles = append(roles, PoolModelPrefix+c.Model)
	}
	h.e.rememberGeneratorFamily(j.ID, choices[0].Family)
	return roles
}

func (h phaseHost) PreferPanel() bool {
	return h.e.selector != nil && h.e.selector.PreferPanel()
}

func (h phaseHost) UpdateProfileFeatures(ctx context.Context, j job.Job, t project.Task) {
	h.e.updateProfileFeatures(ctx, j, t)
}

func (h phaseHost) EnsurePod(ctx context.Context, j job.Job) error {
	return h.e.ensurePod(ctx, j)
}

func (h phaseHost) ToolRunner() phases.ToolRunner { return h.e.runner }

func (h phaseHost) Verifiers() *verify.Registry { return h.e.verifierRegistry() }

func (h phaseHost) RemainingHorizon(ctx context.Context, jobID string) (int64, bool, error) {
	if h.e.horizon == nil {
		return 0, false, nil
	}
	return h.e.horizon.Remaining(ctx, jobID)
}

func (h phaseHost) CaptureEvaluationCorrection(ctx context.Context, projectID, jobID, artifactID string, rec evaluation.Record, in verify.Input) {
	h.e.captureEvaluationCorrection(ctx, projectID, jobID, artifactID, rec, in)
}

func (h phaseHost) CaptureRegulatorCorrection(ctx context.Context, projectID, jobID string, r regulator.Result) {
	h.e.captureRegulatorCorrection(ctx, projectID, jobID, r)
}

func (h phaseHost) CaptureComparisonCorrection(ctx context.Context, projectID, jobID, artifactID string, v acceptance.Verdict) {
	h.e.captureComparisonCorrection(ctx, projectID, jobID, artifactID, v)
}

func (h phaseHost) RecordGitCommit(ctx context.Context, p project.Project, art artifact.Artifact) {
	h.e.recordGitCommit(ctx, p, art)
}

func (h phaseHost) EventStats(ctx context.Context, jobID string) (int, float64) {
	return h.e.eventStats(ctx, jobID)
}

func (h phaseHost) MaxEvaluationScore(ctx context.Context, jobID string) float64 {
	return h.e.maxEvaluationScore(ctx, jobID)
}

func (h phaseHost) JobTokenCost(ctx context.Context, jobID string) int {
	return h.e.jobTokenCost(ctx, jobID)
}

func (h phaseHost) ActualCrossFamily(ctx context.Context, j job.Job, p project.Project, t project.Task, genRole, judgeRole string) (string, string, bool) {
	return h.e.actualCrossFamily(ctx, j, p, t, genRole, judgeRole)
}
