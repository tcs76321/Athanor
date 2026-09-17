package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
)

// step performs the single phase named by j.State and transitions to
// the next state. M3-T1: every §8.1 state is handled explicitly
// (ADR-0001: "M3-T1 completes §8 exactly"). The phase bodies live
// across this package:
//
//	phasePlan        (below)        — §13.1 Phase 1
//	phaseDivergeN    (diverge.go)   — §13.1 Phase 2
//	phaseEvaluate    (evaluate.go)  — §13.1 Phase 3
//	phaseReflect     (reflect.go)   — §13.1 Phase 4
//	phaseSynthesize  (below)        — §13.1 Phase 5
//	phaseCompare     (compare.go)   — §13.1 Phase 6
func (e *Engine) step(ctx context.Context, j job.Job) error {
	switch j.State {
	case job.StateQueued:
		_, err := e.jobs.Transition(ctx, j.ID, job.StateContextBuilding)
		return err
	case job.StateContextBuilding:
		// M1: context is assembled per-phase inside call(); nothing to
		// build ahead of time (the MCE arrives in M5).
		_, err := e.jobs.Transition(ctx, j.ID, job.StatePlanning)
		return err
	case job.StatePlanning:
		return e.phasePlan(ctx, j)
	case job.StateDiverging:
		return e.phaseDivergeN(ctx, j)
	case job.StateEvaluating:
		return e.phaseEvaluate(ctx, j)
	case job.StateReflecting:
		return e.phaseReflect(ctx, j)
	case job.StateSynthesizing:
		return e.phaseSynthesize(ctx, j)
	case job.StateComparing:
		return e.phaseCompare(ctx, j)
	default:
		return fmt.Errorf("engine: no handler for state %q", j.State)
	}
}

// contexts loads the project and task a job executes for.
func (e *Engine) contexts(ctx context.Context, j job.Job) (project.Project, project.Task, error) {
	t, err := e.projects.Task(ctx, j.TaskID)
	if err != nil {
		return project.Project{}, project.Task{}, err
	}
	p, err := e.projects.Get(ctx, t.ProjectID)
	if err != nil {
		return project.Project{}, project.Task{}, err
	}
	return p, t, nil
}

// call performs one phase's LLM request with every guard: context
// feasibility (§12.6), deterministic prompt assembly (§11), phase
// temperature resolution (§13.1), the phase wall-time budget (§8.2), and
// token accounting to the EventLog (§28.2). An empty extraInstructions
// string adds nothing to the prompt.
func (e *Engine) call(ctx context.Context, j job.Job, p project.Project, t project.Task,
	phase, role string, extraInstructions string) (llm.Response, error) {

	if e.cfg == nil {
		return llm.Response{}, errors.New("engine: cfg is nil (call requires config)")
	}

	persona, ok := e.registry.Persona(role)
	if !ok {
		return llm.Response{}, fmt.Errorf("persona %q missing from registry", role)
	}

	// M1-T2: feasibility before every call — never silently reduce.
	verdict := llm.Check(persona, phase, p.Archetype, persona.ContextTarget, e.cfg.ContextEngine)
	if !verdict.Feasible {
		if _, err := e.jobs.Transition(ctx, j.ID, job.StatePaused); err != nil {
			return llm.Response{}, err
		}
		e.audit(ctx, j.ID, map[string]any{
			"event": "context_floor_violation", "phase": phase, "persona": role,
			"required": verdict.Required, "available": verdict.Available,
			"recommendation": verdict.Recommendation,
		})
		return llm.Response{}, ErrPaused
	}

	res, err := prompt.Assemble(prompt.Input{
		Phase:                  phase,
		Project:                prompt.Project{Name: p.Name, Archetype: p.Archetype, Goal: p.Goal},
		Task:                   prompt.Task{Title: t.Title, Description: t.Description},
		Criteria:               t.Criteria,
		EvaluationInstructions: extraInstructions,
	})
	if err != nil {
		return llm.Response{}, err
	}
	temperature := llm.ResolveTemperature(phase, persona.Temperature, nil)

	// Per-phase wall-time budget (§8.2); falls back to the default budget.
	budget, hasBudget := e.cfg.Execution.PhaseBudget(phase)
	if !hasBudget || budget <= 0 {
		budget = 10 * 60 * 1e9 // 10m guard for unbudgeted phases; every §13.1 phase has one
	}
	callCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	resp, err := e.client.Chat(callCtx, llm.Request{
		Model:         persona.Model,
		Messages:      res.Messages,
		Temperature:   temperature,
		ContextTarget: persona.ContextTarget,
	})
	if err != nil {
		// M3-T2 commit 2.4: when the per-phase wall-time budget
		// fires, the call's ctx is `context.DeadlineExceeded`.
		// Emit a `context_deadline_exceeded` audit row so the
		// failure is observable in the EventLog (the job still
		// transitions to `failed` via the step()/Run() path;
		// this row is the *why*).
		if errors.Is(err, context.DeadlineExceeded) {
			e.audit(ctx, j.ID, map[string]any{
				"event":      "context_deadline_exceeded",
				"phase":      phase,
				"persona":    role,
				"model":      persona.Model,
				"budget_sec": int64(budget / time.Second),
			})
		}
		return llm.Response{}, fmt.Errorf("phase %s: %w", phase, err)
	}

	// §13.1/§28.2: resolved temperature and per-section token counts are
	// written to the EventLog for every call.
	sections := make([]map[string]any, 0, len(res.Sections))
	for _, s := range res.Sections {
		sections = append(sections, map[string]any{"name": s.Name, "tokens": s.Tokens})
	}
	e.audit(ctx, j.ID, map[string]any{
		"event": "llm_call", "phase": phase, "persona": role, "model": persona.Model,
		"temperature": temperature, "prompt_tokens": resp.PromptTokens,
		"completion_tokens": resp.CompletionTokens, "estimated_prompt_tokens": res.TotalToken,
		"sections": sections,
	})
	return resp, nil
}

// finalKindFor maps a project archetype to the artifact kind its final
// output takes (§6.2 → §9.1).
func finalKindFor(archetype string) artifact.Kind {
	switch archetype {
	case project.ArchetypeCode:
		return artifact.KindCode
	case project.ArchetypeData:
		return artifact.KindDataset
	case project.ArchetypeMedia:
		return artifact.KindMedia
	default: // text, document
		return artifact.KindDocument
	}
}

// phaseSynthesize (§13.1 Phase 5): main persona refines the persisted
// proposal into the final draft artifact. Re-runs after a crash version
// the existing artifact instead of piling up drafts.
//
// M3-T2 (ADR-0014): the M2-T4 sub-steps (`runCodeInPod` and
// `runTestsInPod`) used to live here. They now live in
// `phaseEvaluate.evaluateCandidate` (per candidate, before the
// security-persona verdict) so the pod's exit code is part of the
// evidence the LLM judge sees. This function is archetype-agnostic;
// the per-archetype artifact kind is the only divergence
// (`finalKindFor` below).
func (e *Engine) phaseSynthesize(ctx context.Context, j job.Job) error {
	p, t, err := e.contexts(ctx, j)
	if err != nil {
		return err
	}
	candidate, err := e.artifacts.LatestForJob(ctx, j.ID, artifact.KindProposal)
	if err != nil {
		return fmt.Errorf("loading divergence candidate: %w", err)
	}
	candidateContent, err := e.artifacts.ReadContent(ctx, candidate.ID)
	if err != nil {
		return fmt.Errorf("reading divergence candidate: %w", err)
	}

	instructions := "CANDIDATE PROPOSAL (from the divergence phase — refine into the final artifact):\n" + string(candidateContent)
	resp, err := e.call(ctx, j, p, t, llm.PhaseSynthesizing, llm.RoleMain, instructions)
	if err != nil {
		return err
	}

	kind := finalKindFor(p.Archetype)
	if prev, err := e.artifacts.LatestForJob(ctx, j.ID, kind); err == nil {
		if _, err := e.artifacts.NewVersion(ctx, prev.ID, []byte(resp.Content)); err != nil {
			return fmt.Errorf("versioning final artifact: %w", err)
		}
	} else {
		if _, err := e.artifacts.CreateDraftFor(ctx, p.ID, t.ID, j.ID, kind, []byte(resp.Content)); err != nil {
			return fmt.Errorf("persisting final artifact: %w", err)
		}
	}

	_, err = e.jobs.Transition(ctx, j.ID, job.StateComparing)
	return err
}

// phaseCompare (§13.1 Phase 6, M1 form): with a single draft, no previous
// accepted artifact, and no evaluation machinery (M3), the comparison is
// deterministic: the draft wins by default. The decision and its rationale
// are audited; the artifact stays a draft until real evaluation exists.
// phasePlan (§13.1 Phase 1): tall persona, low temperature. The plan
// guides divergence; in M1 it is advisory context, not persisted.
func (e *Engine) phasePlan(ctx context.Context, j job.Job) error {
	p, t, err := e.contexts(ctx, j)
	if err != nil {
		return err
	}
	if _, err := e.call(ctx, j, p, t, llm.PhasePlanning, llm.RoleTall, ""); err != nil {
		return err
	}
	_, err = e.jobs.Transition(ctx, j.ID, job.StateDiverging)
	return err
}

