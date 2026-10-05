package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/policy"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/strategy"
)

// StrategySink is the §13.3 capture seam (M6-T10): a job's profile at start
// and its immutable outcome at end. Satisfied by *strategy.Repo; nil disables
// capture.
type StrategySink interface {
	CreateProfile(ctx context.Context, p strategy.Profile) (strategy.Profile, error)
	CreateOutcome(ctx context.Context, o strategy.Outcome) (strategy.Outcome, error)
}

// StrategyInsightSource is the §13.4 prompt channel (M6-T11): the statements
// of *active* insights. Proposed insights are excluded by the source itself,
// so they can never affect a prompt.
type StrategyInsightSource interface {
	ActiveStatements(ctx context.Context) ([]string, error)
}

// phaseStrategies is the engine's fixed phase → persona mapping (the persona
// plan §13.1). Capture derives the profile from it with zero inference.
var phaseStrategies = []struct{ phase, role string }{
	{llm.PhasePlanning, llm.RoleTall},
	{llm.PhaseDiverging, llm.RoleMain},
	{llm.PhaseEvaluating, llm.RoleSecurity},
	{llm.PhaseReflecting, llm.RoleMain},
	{llm.PhaseSynthesizing, llm.RoleMain},
	{llm.PhaseComparing, llm.RoleSecurity},
}

// strategySignature builds the §13.3 signature: one entry per executed phase
// with its resolved persona and temperature. The persona is resolved through
// the plan's ModelRouting (F4-T1c), so a routed judge is recorded.
func (e *Engine) strategySignature(archetype string, plan policy.Plan) []strategy.SignatureEntry {
	out := make([]strategy.SignatureEntry, 0, len(phaseStrategies))
	for _, s := range phaseStrategies {
		role := e.roleFor(plan, s.phase, s.role)
		persona, ok := e.registry.Persona(role)
		if !ok {
			continue
		}
		entry := strategy.SignatureEntry{
			Phase:       s.phase,
			Persona:     role,
			Temperature: llm.ResolveTemperature(s.phase, persona.Temperature, nil),
		}
		if s.phase == llm.PhaseDiverging {
			entry.Candidates = plan.Candidates
		}
		out = append(out, entry)
	}
	return out
}

// captureProfile records the job's strategy profile at start (idempotent).
func (e *Engine) captureProfile(ctx context.Context, j job.Job) {
	if e.strategy == nil {
		return
	}
	p, t, err := e.contexts(ctx, j)
	if err != nil {
		slog.Error("engine: loading project for strategy profile", "job", j.ID, "err", err)
		return
	}
	plan := e.planFor(ctx, j, p, t)
	e.auditComputePlan(ctx, j.ID, "profile", plan)
	if _, err := e.strategy.CreateProfile(ctx, strategy.Profile{
		JobID: j.ID, ProjectID: p.ID, Archetype: p.Archetype,
		Signature: e.strategySignature(p.Archetype, plan),
	}); err != nil {
		slog.Error("engine: capturing strategy profile", "job", j.ID, "err", err)
	}
}

// captureOutcome records the immutable strategy outcome at terminal state.
func (e *Engine) captureOutcome(ctx context.Context, jobID string) {
	if e.strategy == nil {
		return
	}
	j, err := e.jobs.Get(ctx, jobID)
	if err != nil {
		slog.Error("engine: loading job for strategy outcome", "job", jobID, "err", err)
		return
	}
	if !j.State.Terminal() {
		return
	}
	tokens, confidence := e.eventStats(ctx, jobID)
	o := strategy.Outcome{
		JobID:               jobID,
		Result:              e.outcomeResult(ctx, j),
		Score:               e.maxEvaluationScore(ctx, jobID),
		EvaluatorConfidence: confidence,
		ReflectionLoops:     e.reflectionLoops(ctx, jobID),
		TokenCost:           tokens,
	}
	if j.StartedAt != nil && j.FinishedAt != nil {
		o.WallTime = j.FinishedAt.Sub(*j.StartedAt)
	}
	if _, err := e.strategy.CreateOutcome(ctx, o); err != nil {
		slog.Error("engine: capturing strategy outcome", "job", jobID, "err", err)
	}
}

// outcomeResult maps the terminal job state and the comparison winner to a
// §13.3 result.
func (e *Engine) outcomeResult(ctx context.Context, j job.Job) string {
	switch j.State {
	case job.StateFailed:
		return strategy.ResultFailed
	case job.StateCancelled:
		return strategy.ResultCancelled
	}
	winner := ""
	if events, err := e.db.QueryEvents(ctx, store.EventFilter{JobID: j.ID, Category: "jobs"}); err == nil {
		for _, ev := range events {
			var d struct {
				Event  string `json:"event"`
				Winner string `json:"winner"`
			}
			if json.Unmarshal([]byte(ev.DataJSON), &d) == nil && d.Event == "comparison" {
				winner = d.Winner
			}
		}
	}
	switch winner {
	case "new":
		return strategy.ResultAcceptedNew
	case "previous":
		return strategy.ResultAcceptedPrevious
	default:
		return strategy.ResultRejected
	}
}

// eventStats sums token usage across the job's `llm_call` rows and returns
// the last comparison confidence.
func (e *Engine) eventStats(ctx context.Context, jobID string) (tokens int, confidence float64) {
	events, err := e.db.QueryEvents(ctx, store.EventFilter{JobID: jobID, Category: "jobs"})
	if err != nil {
		return 0, 0
	}
	for _, ev := range events {
		var d struct {
			Event            string  `json:"event"`
			PromptTokens     int     `json:"prompt_tokens"`
			CompletionTokens int     `json:"completion_tokens"`
			Confidence       float64 `json:"confidence"`
		}
		if json.Unmarshal([]byte(ev.DataJSON), &d) != nil {
			continue
		}
		switch d.Event {
		case "llm_call":
			tokens += d.PromptTokens + d.CompletionTokens
		case "comparison":
			confidence = d.Confidence
		}
	}
	return tokens, confidence
}

// maxEvaluationScore returns the highest score among the job's evaluation
// records (0 when the evaluation repo is not wired).
func (e *Engine) maxEvaluationScore(ctx context.Context, jobID string) float64 {
	if e.eval == nil {
		return 0
	}
	recs, err := e.eval.ListByJob(ctx, jobID)
	if err != nil {
		return 0
	}
	var max float64
	for _, r := range recs {
		if r.Score > max {
			max = r.Score
		}
	}
	return max
}

// reflectionLoops reads the job's typed reflection counter (0 when unset).
func (e *Engine) reflectionLoops(ctx context.Context, jobID string) int {
	var raw string
	if err := e.db.DB().QueryRowContext(ctx,
		`SELECT value FROM system_state WHERE key = ?`, reflectCounterPrefix+jobID).Scan(&raw); err != nil {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return n
}
