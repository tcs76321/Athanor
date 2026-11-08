package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/analysis"
	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/engine/acceptance"
	"github.com/tcs76321/athanor/internal/engine/contextasm"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/notebook"
	"github.com/tcs76321/athanor/internal/policy"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
)

// tierNames renders tiers for audit rows, ascending, nil-safe.
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
		// Context is assembled per phase inside call() (the MCE tier ladder,
		// §10.5); there is nothing to build ahead of time.
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
func judgmentSeed(jobID, phase string, candidates []prompt.CandidateArtifact, salt int) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(jobID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(phase))
	// known-issues M7: `salt` decorrelates the votes of a judge quorum. With a
	// derived (pinned) seed, N votes at the same seed would be near-identical
	// and the quorum would add no independent signal; folding the vote index
	// in gives each vote its own reproducible seed.
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
// callOption tunes one engine LLM call. It exists so the many call sites stay
// readable; currently only the judge quorum needs it (a per-vote seed salt,
// known-issues M7).
type callOption func(*callParams)

type callParams struct{ seedSalt int }

// withSeedSalt folds a per-vote salt into the derived judgment seed.
func withSeedSalt(n int) callOption { return func(p *callParams) { p.seedSalt = n } }

func applyCallOptions(opts []callOption) callParams {
	var p callParams
	for _, o := range opts {
		o(&p)
	}
	return p
}

// planBudget applies an attempt's budget scale to a resolved phase budget
// (ADR-0074 §3). A scale <= 1 leaves the budget unchanged, so a plan can never
// shrink a call's wall-time.
func planBudget(budget time.Duration, plan policy.Plan) time.Duration {
	if plan.BudgetScale <= 1 {
		return budget
	}
	return time.Duration(float64(budget) * plan.BudgetScale)
}

// planTemperature applies an attempt's temperature delta to a resolved phase
// temperature. Judgment phases keep their resolved temperature — determinism is
// a security constraint, not a river. The result is clamped to [0, 1].
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
// instructions for generation phases (ADR-0074 §3). A judgment phase's
// instructions (the rubric) are left untouched.
func planInstructions(base string, plan policy.Plan, judgment bool) string {
	if judgment || plan.Directive == "" {
		return base
	}
	if base == "" {
		return plan.Directive
	}
	return base + "\n" + plan.Directive
}

func (e *Engine) call(ctx context.Context, j job.Job, p project.Project, t project.Task,
	phase, role, extraInstructions string, candidates []prompt.CandidateArtifact,
	opts ...callOption) (llm.Response, error) {

	params := applyCallOptions(opts)

	if e.cfg == nil {
		return llm.Response{}, errors.New("engine: cfg is nil (call requires config)")
	}
	// known-issues L26: guard the registry like the family/difficulty paths
	// do, so a mis-wired Engine fails loudly instead of panicking.
	if e.registry == nil {
		return llm.Response{}, errors.New("engine: registry is nil (call requires personas)")
	}

	persona, ok := e.personaFor(ctx, phase, role, project.CapabilityFor(p, t), project.ContextDemand(p, t), p.ID, j.ID)
	if !ok {
		return llm.Response{}, fmt.Errorf("persona %q missing from registry", role)
	}
	// ADR-0074 §3: an attempt's escalation knobs (budget scale, temperature
	// delta, retry directive) apply to every call the job makes. They come from
	// the same pure plan the phase used, so they are consistent across phases.
	plan := e.planFor(ctx, j, p, t)

	// Context policy (window planning, tiers, suppression, pressure) lives in
	// the contextasm package. It returns the adjusted persona and the
	// assembled messages, or a pause request the root translates into the
	// lifecycle transition.
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
			resolvedFormat = acceptance.VerdictSchema(phase)
			formatName = "json-schema"
		} else {
			resolvedFormat = "json"
			formatName = "json"
		}
	}
	var resolvedSeed *int64
	if e.cfg.Inference.JudgmentSeed == config.JudgmentSeedDerived && temperature == 0 {
		s := judgmentSeed(j.ID, phase, candidates, params.seedSalt)
		resolvedSeed = &s
	}

	// Per-phase wall-time budget (§8.2); falls back to the default budget.
	budget, hasBudget := e.cfg.Execution.PhaseBudget(phase)
	if !hasBudget || budget <= 0 {
		budget = 10 * 60 * 1e9 // 10m guard for unbudgeted phases; every §13.1 phase has one
	}
	budget = planBudget(budget, plan)
	callCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	// F4-T8: `think` is a per-phase decision. Judgment phases (evaluating,
	// comparing) must not spend the token budget on hidden reasoning — a
	// thinking-capable model can burn num_predict on reasoning and emit
	// nothing usable (the M3-T7 runaway). Generation follows config; a
	// configured level is passed as a level, not a boolean (ADR-0090 S0).
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
	// ADR-0091: account the call immediately so the live grant's Remaining is
	// meaningful before terminal settlement. The measured wall is the call's
	// own latency; settlement later replaces the totals with the whole-job
	// token spend and elapsed wall.
	e.spendHorizon(ctx, j.ID, resp.PromptTokens+resp.CompletionTokens, time.Since(callStart))

	// §13.1/§28.2: resolved temperature and per-section token counts are
	// written to the EventLog for every call.
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
		// M3-T7.1: generation provenance for reproducibility/audit.
		// `seed` is null when judgment_seed is off (Ollama draws a
		// random seed); `format` names the constrained format
		// ("json-schema" for the judgment phases).
		"format": formatName, "seed": resolvedSeed,
		// F4-T8: the explicit bounds every call runs under.
		"max_output_tokens": req.MaxTokens,
		"think":             thinkFlag(think),
		"budget_sec":        int64(budget / time.Second),
		// F4 follow-up D3: the residency bound (§12.5).
		"keep_alive": req.KeepAlive,
	}
	// Opt-in full payload capture (logging.capture_llm_payloads): the trace
	// inspector can then show the exact tokens, not only counts.
	if e.cfg.Logging.CaptureLLMPayloads {
		callAudit["prompt_messages"] = prep.Messages
		callAudit["response"] = resp.Content
		if resp.Thinking != "" {
			callAudit["thinking"] = resp.Thinking
		}
	}
	e.audit(ctx, j.ID, callAudit)

	// M6-T7 (§18.3, ADR-0038): audit which corrections were injected, at
	// what token price, and whether the ladder dropped the tier. Only a
	// correction that actually survived into the prompt counts as applied.
	if len(prep.CorrectionIDs) > 0 {
		suppressed := prep.Eviction.Suppresses(prompt.TierCorrections)
		e.auditCat(ctx, j.ID, "feedback", map[string]any{
			"event": "corrections_injected", "phase": phase,
			"count": len(prep.CorrectionIDs), "correction_ids": prep.CorrectionIDs,
			"tokens": prep.TierWeights[prompt.TierCorrections], "suppressed": suppressed,
		})
		if !suppressed && e.correctionSource != nil {
			for _, id := range prep.CorrectionIDs {
				if err := e.correctionSource.MarkApplied(ctx, id); err != nil {
					slog.Error("engine: marking correction applied", "correction", id, "err", err)
				}
			}
		}
	}

	// M6-T8c (§20.4): the note rode the prompt; mark it injected and audit.
	if len(prep.NoteIDs) > 0 && e.interruptions != nil {
		if err := e.interruptions.MarkInjected(ctx, prep.NoteIDs); err != nil {
			slog.Error("engine: marking interruptions injected", "job", j.ID, "err", err)
		}
		e.audit(ctx, j.ID, map[string]any{
			"event": "interruption_injected", "phase": phase, "count": len(prep.NoteIDs), "note_ids": prep.NoteIDs,
		})
	}

	// M6-T11 (§13.4): audit the strategy-note injection channel.
	if prep.StrategyNotes > 0 {
		e.auditCat(ctx, j.ID, "strategy", map[string]any{
			"event": "strategy_notes_injected", "phase": phase, "count": prep.StrategyNotes,
		})
	}
	return resp, nil
}

// finalKindFor maps a task capability to the artifact kind its final output
// takes (§9.1).
func finalKindFor(capability string) artifact.Kind {
	switch capability {
	case project.CapabilityCode:
		return artifact.KindCode
	case project.CapabilityData:
		return artifact.KindDataset
	case project.CapabilityMedia:
		return artifact.KindMedia
	default: // text, document
		return artifact.KindDocument
	}
}

// bestPassingCandidate selects the job's evaluated candidate to synthesize
// from: the highest-scoring candidate that passed deterministic verification
// (real tests, no missing criteria, no security issues). When no candidate
// passed — or the evaluation repo is not wired — it falls back to the latest
// proposal and reports ok=false. This prevents synthesis from refining an
// arbitrary (possibly failing) proposal and discarding a candidate that
// already passed the real tests (M8-T26b).
func (e *Engine) bestPassingCandidate(ctx context.Context, j job.Job) (artifact.Artifact, bool, error) {
	candidates, err := e.listCandidateArtifacts(ctx, j)
	if err != nil {
		return artifact.Artifact{}, false, fmt.Errorf("listing candidates: %w", err)
	}
	if len(candidates) == 0 {
		return artifact.Artifact{}, false, fmt.Errorf("no divergence candidates for job %s", j.ID)
	}
	if e.eval == nil {
		return candidates[len(candidates)-1], false, nil
	}
	records, err := e.eval.ListByJob(ctx, j.ID)
	if err != nil {
		return artifact.Artifact{}, false, fmt.Errorf("listing evaluation records: %w", err)
	}
	passing := map[string]bool{}
	score := map[string]float64{}
	for _, r := range records {
		if r.PassedTests && len(r.MissingCriteria) == 0 && len(r.SecurityIssues) == 0 {
			passing[r.ArtifactID] = true
		}
		if cur, seen := score[r.ArtifactID]; !seen || r.Score > cur {
			score[r.ArtifactID] = r.Score
		}
	}
	var chosen *artifact.Artifact
	var chosenScore float64
	for i := range candidates {
		if !passing[candidates[i].ID] {
			continue
		}
		if chosen == nil || score[candidates[i].ID] > chosenScore {
			c := candidates[i]
			chosen = &c
			chosenScore = score[candidates[i].ID]
		}
	}
	if chosen != nil {
		return *chosen, true, nil
	}
	// No candidate passed. Start synthesis from the BEST-scoring candidate,
	// not the last one: with multiple candidates the last is arbitrary, and
	// letting it seed synthesis can make the full loop deliver a worse
	// artifact than a one-shot arm (ADR-0117).
	var best *artifact.Artifact
	bestScore := 0.0
	for i := range candidates {
		candidateScore, ok := score[candidates[i].ID]
		if !ok {
			continue
		}
		if best == nil || candidateScore > bestScore {
			c := candidates[i]
			best = &c
			bestScore = candidateScore
		}
	}
	if best != nil {
		return *best, false, nil
	}
	return candidates[len(candidates)-1], false, nil
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
	candidate, passed, err := e.bestPassingCandidate(ctx, j)
	if err != nil {
		return err
	}
	candidateContent, err := e.artifacts.ReadContent(ctx, candidate.ID)
	if err != nil {
		return fmt.Errorf("reading divergence candidate: %w", err)
	}

	// M5-T5: the proposal bytes ride §11.2 §12 (tier 3, never evicted)
	// instead of being concatenated into the §13 instructions.
	plan := e.planFor(ctx, j, p, t)
	capability := project.CapabilityFor(p, t)
	role := e.roleFor(plan, llm.PhaseSynthesizing, llm.RoleMain)
	instructions := "Refine the divergence proposal below into the final artifact for this task."
	if capability == project.CapabilityCode {
		instructions += " " + codeInstruction(e.languageFor(ctx, j, p, t))
	}
	resp, err := e.call(ctx, j, p, t, llm.PhaseSynthesizing, role, instructions,
		[]prompt.CandidateArtifact{{Kind: "proposal", Content: string(candidateContent)}})
	if err != nil {
		return err
	}

	// F4 follow-up (A1): the synthesized final must also be raw source for a
	// code archetype, so the accepted artifact and its git commit are clean.
	content := resp.Content
	if capability == project.ArchetypeCode {
		content = normalizeCode(content)
	}
	// M8-T26b / P0: never discard a verified candidate. If the rewrite fails
	// the deterministic verifier while the source candidate passed it, keep
	// the candidate's bytes as the final artifact. This applies to every
	// archetype with a hard verifier (code tests, declared text/document
	// checks, data schema), not only code: the v2 trace showed a text job
	// whose passing candidate was rewritten into a final that failed a
	// declared check, ending `failed` while the external check passed.
	if ver, _, verr := e.verifyCandidate(ctx, j, p, t, content, 0); verr == nil {
		e.auditVerification(ctx, j.ID, "final", ver)
		if ver.HardDecisive() && !ver.HardPassed {
			fallback := passed
			reason := "source candidate passed the verifier"
			if !fallback {
				if candVer, _, cerr := e.verifyCandidate(ctx, j, p, t, string(candidateContent), 0); cerr == nil {
					fallback = acceptance.ShouldFallbackToCandidate(false, ver, candVer)
					reason = "source candidate scored better than the synthesis"
				}
			}
			if fallback {
				content = string(candidateContent)
				e.audit(ctx, j.ID, map[string]any{
					"event": "synthesis_fallback_to_candidate", "candidate_id": candidate.ID,
					"archetype": capability, "reason": reason,
				})
			}
		}
	}

	e.note(ctx, j.ID, p.ID, notebook.KindDecision, "synthesizing",
		fmt.Sprintf("final %s artifact synthesized (%d bytes)", capability, len(content)))
	kind := finalKindFor(capability)
	if prev, err := e.artifacts.LatestForJob(ctx, j.ID, kind); err == nil {
		if _, err := e.artifacts.NewVersion(ctx, prev.ID, []byte(content)); err != nil {
			return fmt.Errorf("versioning final artifact: %w", err)
		}
	} else {
		if _, err := e.artifacts.CreateDraftFor(ctx, p.ID, t.ID, j.ID, kind, []byte(content)); err != nil {
			return fmt.Errorf("persisting final artifact: %w", err)
		}
	}

	_, err = e.jobs.Transition(ctx, j.ID, job.StateComparing)
	return err
}

// phasePlan (§13.1 Phase 1): tall persona, low temperature. The plan
// guides divergence; in M1 it is advisory context, not persisted. F4-T2
// adds one thing: the planner is asked to end with a `DIFFICULTY:` hint,
// which is parsed and persisted so the compute policy can scale to the
// task (an unparseable or absent hint leaves the policy on its other
// signals).
//
// The request rides the task description (a pinned, tier-2 §11.2 section)
// rather than the phase's tier-7 instructions, so the planning call stays
// pinned-only — the property the §10.4 critical-pressure tests key on.
func (e *Engine) phasePlan(ctx context.Context, j job.Job) error {
	p, t, err := e.contexts(ctx, j)
	if err != nil {
		return err
	}
	plan := e.planFor(ctx, j, p, t)
	role := e.roleFor(plan, llm.PhasePlanning, llm.RoleTall)
	planTask := t
	planTask.Description = strings.TrimRight(t.Description, "\n") + "\n\n" + difficultyRequest + "\n\n" + analysisRequest + "\n\n" + languageRequest
	resp, err := e.call(ctx, j, p, planTask, llm.PhasePlanning, role, "", nil)
	if err != nil {
		return err
	}
	if e.askForClarification(ctx, j.ID, p.ID, "planning", resp.Content) {
		return ErrPaused
	}
	e.note(ctx, j.ID, p.ID, notebook.KindPlan, "planning", resp.Content)
	if d := parseDifficultyHint(resp.Content); d != "" {
		if err := e.state.SetDifficulty(ctx, j.ID, d); err != nil {
			slog.Warn("engine: persisting difficulty hint", "job", j.ID, "err", err)
		}
		e.audit(ctx, j.ID, map[string]any{"event": "difficulty_hint", "difficulty": d})
	}
	if l := parseLanguageHint(resp.Content); l != "" {
		if err := e.state.SetLanguage(ctx, j.ID, l); err != nil {
			slog.Warn("engine: persisting language hint", "job", j.ID, "err", err)
		}
		e.audit(ctx, j.ID, map[string]any{"event": "language_hint", "language": l})
	}
	a := analysis.Parse(resp.Content)
	if err := e.state.SetAnalysis(ctx, j.ID, a); err != nil {
		slog.Error("engine: storing task analysis", "job", j.ID, "err", err)
	}
	e.audit(ctx, j.ID, map[string]any{
		"event": "task_analysis", "parsed": a.Parsed,
		"unit_count": len(a.Units), "deliberate": len(a.Deliberate()),
		"units_detail": a.Units,
	})
	_, err = e.jobs.Transition(ctx, j.ID, job.StateDiverging)
	return err
}

// difficultyRequest is the F4-T2 planning instruction. It is a constant so
// the parser and the prompt cannot drift.
const difficultyRequest = "End your plan with a final line exactly of the form `DIFFICULTY: easy` or `DIFFICULTY: hard`, judging whether this task is routine or genuinely hard. Keep it to one line."

// analysisRequest is the ADR-0090 planning instruction. It is a constant so
// the parser and the prompt cannot drift. The model is asked for a bounded
// decomposition; a malformed or absent block degrades to one direct unit.
const analysisRequest = "Then emit exactly one fenced ```analysis block containing JSON of the form " +
	`{"units":[{"goal":"...","mode":"direct|deliberate","reason":"...","falsifier":"..."}]}. ` +
	"Use at most five units. Default every unit to \"direct\"; use \"deliberate\" only for a part " +
	"you expect to be wrong or that has hidden constraints. The falsifier is the cheapest check " +
	"that would reveal that unit is wrong. No prose inside the block."

// parseDifficultyHint extracts the F4-T2 `DIFFICULTY: easy|hard` marker from
// the planning output. It scans lines case-insensitively and returns the
// last valid marker (a model that revises its estimate should win). An
// absent or unrecognized marker returns "" — the policy then falls back to
// history and criteria rather than guessing.
func parseDifficultyHint(content string) string {
	hint := ""
	for _, line := range strings.Split(content, "\n") {
		l := strings.ToUpper(strings.TrimSpace(line))
		const prefix = "DIFFICULTY:"
		if !strings.HasPrefix(l, prefix) {
			continue
		}
		v := strings.TrimSpace(l[len(prefix):])
		switch {
		case strings.HasPrefix(v, "EASY"):
			hint = "easy"
		case strings.HasPrefix(v, "HARD"):
			hint = "hard"
		}
	}
	return hint
}
