package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"time"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/engine/acceptance"
	"github.com/tcs76321/athanor/internal/engine/contextasm"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/policy"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
)

// pauseForContext applies a context-assembly pause request: transition to
// paused, audit the context event, optionally stop the pod (the pressure path
// did; the pre-assembly gates did not), and report ErrPaused so Run stops the
// phase without failing the job.
func (e *Engine) pauseForContext(ctx context.Context, j job.Job, np *contextasm.NeedsPause) error {
	if _, err := e.jobs.Transition(ctx, j.ID, job.StatePaused); err != nil {
		return err
	}
	data := map[string]any{"event": np.Event}
	for k, v := range np.Fields {
		data[k] = v
	}
	e.auditCat(ctx, j.ID, "context", data)
	if np.StopPod {
		e.stopPod(ctx, j.ID)
	}
	return ErrPaused
}

// phaseProducesJSON reports whether a phase expects a structured JSON verdict
// from the security persona (ADR-0012). Only the two judgment phases do.
func phaseProducesJSON(phase string) bool {
	return phase == llm.PhaseEvaluating || phase == llm.PhaseComparing
}

// judgmentSeed derives the Ollama sampler seed for a Temperature-0 judgment
// call (M3-T7.1). It is deterministic per job/phase/candidate so a resumed job
// samples the same point; the salt decorrelates a judge quorum's votes.
func judgmentSeed(jobID, phase string, candidates []prompt.CandidateArtifact, salt int) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(jobID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(phase))
	if salt != 0 {
		_, _ = h.Write([]byte{0})
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(salt))
		_, _ = h.Write(b[:])
	}
	for _, c := range candidates {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(c.Kind))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(c.Content))
	}
	return int64(h.Sum64() & (1<<63 - 1))
}

// planBudget applies an attempt's budget scale to a resolved phase budget
// (ADR-0074 §3). A scale <= 1 leaves the budget unchanged.
func planBudget(budget time.Duration, plan policy.Plan) time.Duration {
	if plan.BudgetScale <= 1 {
		return budget
	}
	return time.Duration(float64(budget) * plan.BudgetScale)
}

// planTemperature applies an attempt's temperature delta to a resolved phase
// temperature. Judgment phases keep their resolved temperature.
func planTemperature(resolved float64, plan policy.Plan, judgment bool) float64 {
	if judgment || plan.TemperatureDelta == 0 {
		return resolved
	}
	t := resolved + plan.TemperatureDelta
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return t
}

// planInstructions appends an attempt's retry directive to a phase's
// instructions for generation phases.
func planInstructions(base string, plan policy.Plan, judgment bool) string {
	if judgment || plan.Directive == "" {
		return base
	}
	if base == "" {
		return plan.Directive
	}
	return base + "\n" + plan.Directive
}

// call performs one phase's LLM request: model/persona resolution, context
// assembly through contextasm, the phase budget, the request, and the post-call
// accounting. Every lifecycle transition stays in this package.
func (e *Engine) call(ctx context.Context, j job.Job, p project.Project, t project.Task,
	phase, role, extraInstructions string, candidates []prompt.CandidateArtifact,
	salt int) (llm.Response, error) {

	if e.cfg == nil {
		return llm.Response{}, errors.New("engine: cfg is nil (call requires config)")
	}
	if e.registry == nil {
		return llm.Response{}, errors.New("engine: registry is nil (call requires personas)")
	}

	persona, ok := e.personaFor(ctx, phase, role, project.CapabilityFor(p, t), project.ContextDemand(p, t), p.ID, j.ID)
	if !ok {
		return llm.Response{}, fmt.Errorf("persona %q missing from registry", role)
	}
	plan := e.planFor(ctx, j, p, t)

	prep, err := e.asm.Prepare(ctx, contextasm.PrepareRequest{
		Job: j, Project: p, Task: t,
		Phase: phase, Role: role, Persona: persona,
		Instructions:   planInstructions(extraInstructions, plan, isJudgmentPhase(phase)),
		Candidates:     candidates,
		AllowQuestions: e.cfg.Execution.ClarificationsEnabled(),
	})
	if err != nil {
		var np *contextasm.NeedsPause
		if errors.As(err, &np) {
			return llm.Response{}, e.pauseForContext(ctx, j, np)
		}
		return llm.Response{}, err
	}
	persona = prep.Persona

	temperature := planTemperature(llm.ResolveTemperature(phase, persona.Temperature, nil), plan, isJudgmentPhase(phase))

	var resolvedFormat any
	formatName := ""
	if e.cfg.Inference.JSONFormatEnabled() && phaseProducesJSON(phase) {
		if e.cfg.Inference.JSONSchema {
			resolvedFormat = acceptance.VerdictSchema(phase)
			formatName = "json-schema"
		} else {
			resolvedFormat = "json"
			formatName = "json"
		}
	}
	var resolvedSeed *int64
	if e.cfg.Inference.JudgmentSeed == config.JudgmentSeedDerived && temperature == 0 {
		s := judgmentSeed(j.ID, phase, candidates, salt)
		resolvedSeed = &s
	}

	budget, hasBudget := e.cfg.Execution.PhaseBudget(phase)
	if !hasBudget || budget <= 0 {
		budget = 10 * 60 * 1e9
	}
	budget = planBudget(budget, plan)
	callCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	think := resolveThink(e.cfg.Inference, phase)

	req := llm.Request{
		Model:         persona.Model,
		Messages:      prep.Messages,
		Temperature:   temperature,
		ContextTarget: persona.ContextTarget,
		Seed:          resolvedSeed,
		Format:        resolvedFormat,
		MaxTokens:     e.cfg.Inference.MaxOutputTokens,
		Think:         think,
		KeepAlive:     e.cfg.Inference.KeepAlive,
	}
	var resp llm.Response
	callStart := time.Now()
	if e.tokenSink != nil {
		resp, err = e.client.Stream(callCtx, req, func(tok string) { e.tokenSink.Publish(j.ID, tok) })
	} else {
		resp, err = e.client.Chat(callCtx, req)
	}
	if err != nil {
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
	e.spendHorizon(ctx, j.ID, resp.PromptTokens+resp.CompletionTokens, time.Since(callStart))

	sections := make([]map[string]any, 0, len(prep.Sections))
	for _, s := range prep.Sections {
		sections = append(sections, map[string]any{"name": s.Name, "tokens": s.Tokens})
	}
	callAudit := map[string]any{
		"event": "llm_call", "phase": phase, "persona": role, "model": persona.Model,
		"temperature": temperature, "prompt_tokens": resp.PromptTokens,
		"completion_tokens":       resp.CompletionTokens,
		"reasoning_tokens":        resp.ReasoningTokens,
		"thinking_chars":          len(resp.Thinking),
		"estimated_prompt_tokens": prep.TotalToken,
		"sections":                sections,
		"format":                  formatName, "seed": resolvedSeed,
		"max_output_tokens": req.MaxTokens,
		"think":             thinkFlag(think),
		"budget_sec":        int64(budget / time.Second),
		"keep_alive":        req.KeepAlive,
	}
	if e.cfg.Logging.CaptureLLMPayloads {
		callAudit["prompt_messages"] = prep.Messages
		callAudit["response"] = resp.Content
		if resp.Thinking != "" {
			callAudit["thinking"] = resp.Thinking
		}
	}
	e.audit(ctx, j.ID, callAudit)

	if len(prep.CorrectionIDs) > 0 {
		e.auditCat(ctx, j.ID, "feedback", map[string]any{
			"event": "corrections_injected", "phase": phase,
			"count": len(prep.CorrectionIDs), "correction_ids": prep.CorrectionIDs,
			"tokens": prep.TierWeights[prompt.TierCorrections], "suppressed": prep.CorrectionsSuppressed,
		})
		if !prep.CorrectionsSuppressed && e.correctionSource != nil {
			for _, id := range prep.CorrectionIDs {
				if err := e.correctionSource.MarkApplied(ctx, id); err != nil {
					slog.Error("engine: marking correction applied", "correction", id, "err", err)
				}
			}
		}
	}

	if len(prep.NoteIDs) > 0 && e.interruptions != nil {
		if err := e.interruptions.MarkInjected(ctx, prep.NoteIDs); err != nil {
			slog.Error("engine: marking interruptions injected", "job", j.ID, "err", err)
		}
		e.audit(ctx, j.ID, map[string]any{
			"event": "interruption_injected", "phase": phase, "count": len(prep.NoteIDs), "note_ids": prep.NoteIDs,
		})
	}

	if prep.StrategyNotes > 0 {
		e.auditCat(ctx, j.ID, "strategy", map[string]any{
			"event": "strategy_notes_injected", "phase": phase, "count": prep.StrategyNotes,
		})
	}
	return resp, nil
}
