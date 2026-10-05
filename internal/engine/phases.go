package engine

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
)

// tierNames renders tiers for audit rows, ascending, nil-safe.
func tierNames(tiers []prompt.Tier) []string {
	out := make([]string, 0, len(tiers))
	for _, t := range tiers {
		out = append(out, t.String())
	}
	return out
}

// tierWeights renders the per-tier price table for audit rows, keyed by
// tier name so a post-mortem reads without decoding integers.
func tierWeights(weights map[prompt.Tier]int) map[string]int {
	out := make(map[string]int, len(weights))
	for t, w := range weights {
		out[t.String()] = w
	}
	return out
}

// pauseForPressure pauses a job whose assembled prompt cannot fit (or
// cannot be relieved within) the persona's context window — §10.4's
// floor-breach arm (M5-T4, ADR-0022 §6). The request is never sent:
// Ollama silently truncates an oversized prompt at num_ctx, so sending
// would violate "Athanor never silently truncates context".
//
// The event reuses the `context_floor_violation` name (category
// `context`) so a post-mortem queries one event name for both the
// §12.6 pre-assembly gate and this per-call gate; the `trigger` field
// tells them apart.
func (e *Engine) pauseForPressure(ctx context.Context, j job.Job, phase, role string,
	activeTokens, maxContext int, a mce.Assessment) error {

	if _, err := e.jobs.Transition(ctx, j.ID, job.StatePaused); err != nil {
		return err
	}
	e.auditCat(ctx, j.ID, "context", map[string]any{
		"event": "context_floor_violation", "trigger": "kv_cache_monitor",
		"phase": phase, "persona": role,
		"active_tokens": activeTokens, "max_context": maxContext, "pressure": a.Pressure,
		"action": string(a.Action), "recommendation": a.Recommendation,
	})
	return ErrPaused
}

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

// phaseProducesJSON reports whether a phase expects a structured JSON
// verdict from the security persona (ADR-0012). Only the two judgment
// phases do; every other phase produces prose.
func phaseProducesJSON(phase string) bool {
	return phase == llm.PhaseEvaluating || phase == llm.PhaseComparing
}

// judgmentSeed derives the Ollama sampler seed for a Temperature-0
// judgment call (M3-T7.1). It is a deterministic function of the job,
// the phase, and the candidate bytes, so re-entering the same phase for
// the same job (e.g. crash recovery) samples the same point, and
// different candidates get independent seeds. The value is recorded in
// the llm_call audit row so a post-mortem can replay the call.
//
// It is deliberately NOT a cross-job determinism guarantee — a fresh job
// has a new ID, so a fresh run samples a different point. T-c measures
// that residual instability rather than assuming it away. FNV-1a is
// sufficient: this is a sampling key, not a security boundary.
func judgmentSeed(jobID, phase string, candidates []prompt.CandidateArtifact) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(jobID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(phase))
	for _, c := range candidates {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(c.Kind))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(c.Content))
	}
	return int64(h.Sum64() & (1<<63 - 1))
}

// call performs one phase's LLM request with every guard: context
// feasibility (§12.6), deterministic prompt assembly (§11), the §10.5 tier
// budget and eviction ladder (M5-T5), phase temperature resolution
// (§13.1), the phase wall-time budget (§8.2), and token accounting to the
// EventLog (§28.2).
//
// extraInstructions is the phase-specific evaluation/strategy text
// (§11.2 §13–14, tier 7). candidates are the artifacts the phase works on
// (§11.2 §12, tier 3): they are never concatenated into instructions, so a
// phase that must refine or judge a candidate cannot have it evicted out
// from under it (ADR-0023 §2).
func (e *Engine) call(ctx context.Context, j job.Job, p project.Project, t project.Task,
	phase, role, extraInstructions string, candidates []prompt.CandidateArtifact) (llm.Response, error) {

	if e.cfg == nil {
		return llm.Response{}, errors.New("engine: cfg is nil (call requires config)")
	}

	persona, ok := e.registry.Persona(role)
	if !ok {
		return llm.Response{}, fmt.Errorf("persona %q missing from registry", role)
	}

	// M1-T2: feasibility before every call — never silently reduce.
	// M5-T4: this is the §12.6 pre-assembly gate (what the *window*
	// must be); the per-call §10.4 pressure gate below is its
	// complement (what goes *in* the window).
	verdict := llm.Check(persona, phase, p.Archetype, persona.ContextTarget, e.cfg.ContextEngine)
	if !verdict.Feasible {
		if _, err := e.jobs.Transition(ctx, j.ID, job.StatePaused); err != nil {
			return llm.Response{}, err
		}
		e.auditCat(ctx, j.ID, "context", map[string]any{
			"event": "context_floor_violation", "phase": phase, "persona": role,
			"required": verdict.Required, "available": verdict.Available,
			"recommendation": verdict.Recommendation,
		})
		return llm.Response{}, ErrPaused
	}

	// M5-T5 (§10.5, ADR-0023): gather the tier payloads, the §25 tool
	// envelope, the persisted suppression set, and the assembly ceiling
	// before assembling. A nil ContextProvider leaves tiers 3 and 6
	// empty, which is the pre-T5 assembler exactly.
	tiers := e.promptTiersFor(ctx, j, t, candidates)
	suppressed := e.loadSuppressedTiers(ctx, j.ID)
	ceiling := ceilingFor(e.cfg.ContextEngine, persona.ContextTarget)
	// M6-T8c (§20.4): a queued interruption note is injected at this safe
	// point (context assembly) and marked injected after the call.
	notes, noteIDs := e.pendingInterruptions(ctx, j.ID)

	// M6-T11 (§13.4): only *active* insight statements reach the prompt; a
	// proposed insight is provably inert.
	var strategyNotes []string
	if e.insights != nil && e.cfg != nil && config.Val(e.cfg.StrategyAnalysis.StrategyNotesInPrompts, true) {
		stmts, err := e.insights.ActiveStatements(ctx)
		if err != nil {
			slog.Warn("engine: reading strategy insights", "job", j.ID, "err", err)
		} else {
			strategyNotes = stmts
		}
	}

	res, err := prompt.Assemble(prompt.Input{
		Phase:                  phase,
		Project:                prompt.Project{Name: p.Name, Archetype: p.Archetype, Goal: p.Goal},
		Task:                   prompt.Task{Title: t.Title, Description: t.Description},
		Criteria:               t.Criteria,
		EvaluationInstructions: extraInstructions,
		Tools:                  tiers.Tools,
		ActiveChunk:            tiers.ActiveChunk,
		Corrections:            tiers.Corrections,
		Candidates:             tiers.Candidates,
		DormantIndex:           tiers.DormantIndex,
		InterruptionNotes:      notes,
		StrategyNotes:          strategyNotes,
		Ceiling:                ceiling,
		Suppressed:             suppressed,
	})
	if err != nil {
		return llm.Response{}, err
	}

	// M5-T5: the assembly's own overflow evictions are state, not a
	// one-off — persist the union so the next call starts from the
	// reduced set (ADR-0023 §5, §23.6: a restart must not resurrect
	// evicted tiers).
	if len(res.Eviction.Evicted) > 0 {
		e.saveSuppressedTiers(ctx, j.ID, append(append([]prompt.Tier{}, suppressed...), res.Eviction.Evicted...))
	}
	e.auditCat(ctx, j.ID, "inference", map[string]any{
		"event": "kv_cache_assembled", "phase": phase, "persona": role,
		"ceiling": ceiling, "assembled_tokens": res.TotalToken,
		"fits": res.Eviction.Fits, "dropped_tokens": res.Eviction.DroppedTokens,
		"evicted_tiers":    tierNames(res.Eviction.Evicted),
		"suppressed_tiers": tierNames(res.Eviction.Suppressed),
		"tier_weights":     tierWeights(res.TierWeights),
	})

	// M5-T4 (§10.4, ADR-0022): per-call KV-cache pressure gate. The
	// assembled prompt's estimated size is checked against the
	// persona's window before the request is built, because Ollama
	// silently truncates an oversized prompt at num_ctx — and Athanor
	// never silently truncates context. Every call produces a
	// `kv_cache_pressure` row (`inference`, §28.1) so pressure is
	// observable even when no trigger fires; the row pairs with the
	// `llm_call` row that follows for estimate-vs-actual calibration
	// (ADR-0022 §3).
	assessment := mce.Assess(res.TotalToken, persona.ContextTarget, e.cfg.ContextEngine)
	e.auditCat(ctx, j.ID, "inference", map[string]any{
		"event": "kv_cache_pressure", "phase": phase, "persona": role,
		"active_tokens": res.TotalToken, "max_context": persona.ContextTarget,
		"pressure": assessment.Pressure, "action": string(assessment.Action),
	})
	switch assessment.Action {
	case mce.ActionNone:
		// Below the warning threshold: nothing to do.
	case mce.ActionWarn:
		// §10.4 85% arm. The audit row above is the M5-T4 action; the
		// context_swap suggestion in the prompt and the move of
		// oldest non-pinned chunks to Dormant arrive with M5-T5's
		// tier integration alongside the Dormant Index section.
	case mce.ActionCritical:
		// §10.4 95% arm: force-evict the lowest-priority tier through
		// the seam. The released budget materializes in the next
		// assembly (ADR-0023 §6); the current prompt still fits the
		// window (pressure < 100%), so proceeding cannot truncate. A
		// nil seam — or one with nothing left to evict — cannot
		// relieve the pressure, so the call falls through to the
		// floor-breach pause rather than sending a prompt that is over
		// the warning line with no remedy applied.
		eviction := Eviction{}
		if e.evictor != nil {
			var err error
			eviction, err = e.evictor.Evict(ctx, j.ID, res.TierWeights, res.Eviction.Suppressed)
			if err != nil {
				return llm.Response{}, fmt.Errorf("kv-cache eviction: %w", err)
			}
		}
		if len(eviction.Tiers) == 0 {
			return llm.Response{}, e.pauseForPressure(ctx, j, phase, role,
				res.TotalToken, persona.ContextTarget, assessment)
		}
		e.saveSuppressedTiers(ctx, j.ID, append(
			append([]prompt.Tier{}, res.Eviction.Suppressed...), eviction.Tiers...))
		e.auditCat(ctx, j.ID, "inference", map[string]any{
			"event": "kv_cache_evicted", "phase": phase, "persona": role,
			"tokens_freed": eviction.FreedTokens, "pressure": assessment.Pressure,
			"evicted_tiers": tierNames(eviction.Tiers),
		})
	default: // mce.ActionFloorBreach
		return llm.Response{}, e.pauseForPressure(ctx, j, phase, role,
			res.TotalToken, persona.ContextTarget, assessment)
	}

	temperature := llm.ResolveTemperature(phase, persona.Temperature, nil)

	// M3-T7.5b (ADR-0012 §D6 upgrade): bind the judgment phases to a JSON
	// schema, not just `format: "json"`. The schema grammar-constrains
	// field types, which `"json"` does not — the M3-T7 smoke saw a string
	// `confidence` and a string `style_issues` under `"json"`. The
	// tolerant parser (M3-T7.5a) remains the fallback for a model or
	// Ollama version that ignores the schema.
	var resolvedFormat any
	formatName := ""
	if e.cfg.Inference.JSONFormatEnabled() && phaseProducesJSON(phase) {
		if e.cfg.Inference.JSONSchema {
			resolvedFormat = verdictSchemaFor(phase)
			formatName = "json-schema"
		} else {
			resolvedFormat = "json"
			formatName = "json"
		}
	}
	var resolvedSeed *int64
	if e.cfg.Inference.JudgmentSeed == config.JudgmentSeedDerived && temperature == 0 {
		s := judgmentSeed(j.ID, phase, candidates)
		resolvedSeed = &s
	}

	// Per-phase wall-time budget (§8.2); falls back to the default budget.
	budget, hasBudget := e.cfg.Execution.PhaseBudget(phase)
	if !hasBudget || budget <= 0 {
		budget = 10 * 60 * 1e9 // 10m guard for unbudgeted phases; every §13.1 phase has one
	}
	callCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	req := llm.Request{
		Model:         persona.Model,
		Messages:      res.Messages,
		Temperature:   temperature,
		ContextTarget: persona.ContextTarget,
		Seed:          resolvedSeed,
		Format:        resolvedFormat,
	}
	var resp llm.Response
	if e.tokenSink != nil {
		// M6-T8c: stream for the live watch view; the aggregate is the same
		// as the non-streaming response.
		resp, err = e.client.Stream(callCtx, req, func(tok string) { e.tokenSink.Publish(j.ID, tok) })
	} else {
		resp, err = e.client.Chat(callCtx, req)
	}
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
		// M3-T7.1: generation provenance for reproducibility/audit.
		// `seed` is null when judgment_seed is off (Ollama draws a
		// random seed); `format` names the constrained format
		// ("json-schema" for the judgment phases).
		"format": formatName, "seed": resolvedSeed,
	})

	// M6-T7 (§18.3, ADR-0038): audit which corrections were injected, at
	// what token price, and whether the ladder dropped the tier. Only a
	// correction that actually survived into the prompt counts as applied.
	if len(tiers.CorrectionIDs) > 0 {
		suppressed := res.Eviction.Suppresses(prompt.TierCorrections)
		e.auditCat(ctx, j.ID, "feedback", map[string]any{
			"event": "corrections_injected", "phase": phase,
			"count": len(tiers.CorrectionIDs), "correction_ids": tiers.CorrectionIDs,
			"tokens": res.TierWeights[prompt.TierCorrections], "suppressed": suppressed,
		})
		if !suppressed && e.correctionSource != nil {
			for _, id := range tiers.CorrectionIDs {
				if err := e.correctionSource.MarkApplied(ctx, id); err != nil {
					slog.Error("engine: marking correction applied", "correction", id, "err", err)
				}
			}
		}
	}

	// M6-T8c (§20.4): the note rode the prompt; mark it injected and audit.
	if len(noteIDs) > 0 && e.interruptions != nil {
		if err := e.interruptions.MarkInjected(ctx, noteIDs); err != nil {
			slog.Error("engine: marking interruptions injected", "job", j.ID, "err", err)
		}
		e.audit(ctx, j.ID, map[string]any{
			"event": "interruption_injected", "phase": phase, "count": len(noteIDs), "note_ids": noteIDs,
		})
	}

	// M6-T11 (§13.4): audit the strategy-note injection channel.
	if len(strategyNotes) > 0 {
		e.auditCat(ctx, j.ID, "strategy", map[string]any{
			"event": "strategy_notes_injected", "phase": phase, "count": len(strategyNotes),
		})
	}
	return resp, nil
}

// pendingInterruptions returns a job's queued notes as (texts, ids). A store
// error degrades to no notes with a warning rather than failing the call.
func (e *Engine) pendingInterruptions(ctx context.Context, jobID string) ([]string, []string) {
	if e.interruptions == nil {
		return nil, nil
	}
	notes, err := e.interruptions.Pending(ctx, jobID)
	if err != nil {
		slog.Warn("engine: reading interruption notes", "job", jobID, "err", err)
		return nil, nil
	}
	texts := make([]string, 0, len(notes))
	ids := make([]string, 0, len(notes))
	for _, n := range notes {
		texts = append(texts, n.Text)
		ids = append(ids, n.ID)
	}
	return texts, ids
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

	// M5-T5: the proposal bytes ride §11.2 §12 (tier 3, never evicted)
	// instead of being concatenated into the §13 instructions.
	instructions := "Refine the divergence proposal below into the final artifact for this task."
	resp, err := e.call(ctx, j, p, t, llm.PhaseSynthesizing, llm.RoleMain, instructions,
		[]prompt.CandidateArtifact{{Kind: "proposal", Content: string(candidateContent)}})
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

// phasePlan (§13.1 Phase 1): tall persona, low temperature. The plan
// guides divergence; in M1 it is advisory context, not persisted.
func (e *Engine) phasePlan(ctx context.Context, j job.Job) error {
	p, t, err := e.contexts(ctx, j)
	if err != nil {
		return err
	}
	if _, err := e.call(ctx, j, p, t, llm.PhasePlanning, llm.RoleTall, "", nil); err != nil {
		return err
	}
	_, err = e.jobs.Transition(ctx, j.ID, job.StateDiverging)
	return err
}
