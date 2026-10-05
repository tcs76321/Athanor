package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
)

// evalVerdict is the §13.1 Phase 6 JSON the security persona produces.
// The schema is the LLM's output shape; the engine enforces the
// §19.3 deterministic guard against it, not the LLM's text.
type evalVerdict struct {
	Passed             bool     `json:"passed"`
	Score              float64  `json:"score"`
	FailedTests        []string `json:"failed_tests"`
	MissingCriteria    []string `json:"missing_criteria"`
	SecurityIssues     []string `json:"security_issues"`
	StyleIssues        []string `json:"style_issues"`
	BetterThanPrevious bool     `json:"better_than_previous"`
	Confidence         float64  `json:"confidence"`
	Summary            string   `json:"summary"`
}

// phaseEvaluate (§13.1 Phase 3, M3-T1): for every candidate artifact
// produced by the diverging phase, run the §19 acceptance-criteria
// checks, exercise the test command in a Job Pod (code archetype
// only), and persist one EvaluationRecord per candidate.
//
// Temperature: 0.0 (pinned, "maximally deterministic"). Persona:
// `security` (the LLM is told to judge, not generate).
//
// Sub-state: for the code archetype, `running_tests` is recorded as
// an event row (`substate_entered` / `substate_exited`) so the
// timeline is queryable without a jobs.state column (per §8.1's
// "tracked sub-state" note).
//
// Branch (§8.2):
//   - ≥1 candidate passed (passed == true) → `evaluating → synthesizing`
//   - 0 candidates passed                  → `evaluating → reflecting`
func (e *Engine) phaseEvaluate(ctx context.Context, j job.Job) error {
	if e.eval == nil {
		return errors.New("engine: evaluation repo is nil (M3-T1; tests must pass it)")
	}
	p, t, err := e.contexts(ctx, j)
	if err != nil {
		return err
	}

	allCandidates, err := e.listCandidateArtifacts(ctx, j)
	if err != nil {
		return fmt.Errorf("listing candidates: %w", err)
	}
	// Idempotency: a candidate that already has an EvaluationRecord
	// for this job was evaluated on a previous divergence cycle. The
	// reflection loop re-enters `diverging` and produces a new
	// generation of proposals, so on the second `phaseEvaluate` we
	// must not re-evaluate the old set — doing so would (a) waste
	// LLM calls, (b) re-bump the existing EvaluationRecord audit
	// rows, and (c) make the test-time verdict queue impossible to
	// size. We filter to "unevaluated" proposals and treat
	// "all-already-evaluated" as a 0-pass cycle (which routes
	// correctly to `reflecting` via the passCount == 0 branch
	// below, so a re-entry where divergence produced no new
	// proposals still hits the reflection path instead of silently
	// re-completing).
	existing, err := e.eval.ListByJob(ctx, j.ID)
	if err != nil {
		return fmt.Errorf("listing existing evaluation records: %w", err)
	}
	evaluated := make(map[string]struct{}, len(existing))
	for _, r := range existing {
		evaluated[r.ArtifactID] = struct{}{}
	}
	var candidates []artifact.Artifact
	for _, c := range allCandidates {
		if _, seen := evaluated[c.ID]; !seen {
			candidates = append(candidates, c)
		}
	}
	// `candidates` is the *unevaluated* subset. If it is empty AND
	// `allCandidates` is also empty, divergence truly produced
	// nothing — hard fail. If `candidates` is empty but
	// `allCandidates` is not, every proposal was already evaluated
	// in a prior cycle; the reflection path will re-check the
	// budget and either re-diverge (within budget) or fail (over
	// budget). This branch replaces the M3-T1 simplification that
	// would otherwise fail the job on the second `phaseEvaluate`
	// after a reflection loop.
	if len(candidates) == 0 && len(allCandidates) == 0 {
		// No candidates landed: divergence produced nothing. This is
		// a hard failure (the engine cannot evaluate a void).
		e.audit(ctx, j.ID, map[string]any{
			"event": "evaluating_no_candidates",
		})
		_, err = e.jobs.Transition(ctx, j.ID, job.StateFailed)
		return err
	}

	// The previous accepted artifact (§19.2 `compared_against`) is
	// the same for every candidate in this job.
	previousID := ""
	if prev, err := e.artifacts.LatestAcceptedByProject(ctx, p.ID); err == nil {
		previousID = prev.ID
	} else if !errors.Is(err, artifact.ErrNotFound) {
		return fmt.Errorf("loading previous accepted artifact: %w", err)
	}

	passCount := 0
	for i, cand := range candidates {
		rec, err := e.evaluateCandidate(ctx, j, p, t, cand, previousID, i+1, len(candidates))
		if err != nil {
			return fmt.Errorf("evaluating candidate %d: %w", i+1, err)
		}
		if rec.PassedTests && len(rec.MissingCriteria) == 0 && len(rec.SecurityIssues) == 0 {
			passCount++
		}
	}

	e.audit(ctx, j.ID, map[string]any{
		"event":      "evaluating_done",
		"candidates": len(candidates),
		"passed":     passCount,
	})

	if passCount > 0 {
		_, err = e.jobs.Transition(ctx, j.ID, job.StateSynthesizing)
	} else {
		_, err = e.jobs.Transition(ctx, j.ID, job.StateReflecting)
	}
	return err
}

// listCandidateArtifacts returns the divergence proposals in version
// order (oldest first) so the evaluator's indices match the
// divergence indices and the audit trail is monotonic.
func (e *Engine) listCandidateArtifacts(ctx context.Context, j job.Job) ([]artifact.Artifact, error) {
	rows, err := e.artifacts.ListByProject(ctx, j.ProjectID)
	if err != nil {
		return nil, err
	}
	var out []artifact.Artifact
	for _, a := range rows {
		if a.JobID == j.ID && a.Kind == artifact.KindProposal && a.Status == artifact.StatusDraft {
			out = append(out, a)
		}
	}
	// ListByProject returns newest first; reverse for evaluation order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// evaluateCandidate runs the §19 checks for one candidate and
// persists the resulting EvaluationRecord. The candidate is scored by
// the security persona at temp 0.0; for the code archetype, the
// existing test command is run in the Job Pod first and its exit
// code is folded into the verdict (the LLM doesn't grade tests; the
// pod does).
func (e *Engine) evaluateCandidate(ctx context.Context, j job.Job, p project.Project, t project.Task,
	cand artifact.Artifact, previousID string, idx, total int) (evaluation.Record, error) {

	content, err := e.artifacts.ReadContent(ctx, cand.ID)
	if err != nil {
		return evaluation.Record{}, fmt.Errorf("reading candidate content: %w", err)
	}

	// F4-T3: deterministic verification first. For code this runs the real
	// test command on *this* candidate; for text/document it applies the
	// structural checks parsed from the criteria. A non-decisive result
	// falls through to the LLM judge below.
	ver, vin, err := e.verifyCandidate(ctx, j, p, t, string(content), idx)
	if err != nil {
		return evaluation.Record{}, err
	}
	if vin.TestRan {
		e.audit(ctx, j.ID, map[string]any{
			"event": "tests_ran", "candidate": idx,
			"passed": vin.TestsPassed, "command": vin.TestCommand,
		})
	}
	e.auditVerification(ctx, j.ID, "candidate", ver)

	// M3-T2 (commit 2.2): the per-archetype rubric goes to the
	// persona first so the verdict's `missing_criteria` /
	// `security_issues` / `style_issues` arrays map 1:1 to
	// rubric items. Empty for data/media (deferred) and for
	// unknown archetypes.
	rubric := rubricFor(p.Archetype)
	var rubricBlock string
	if rubric != "" {
		rubricBlock = "## RUBRIC (apply every item; echo unmet items into missing_criteria or security_issues or style_issues)\n" + rubric + "\n\n"
	}

	deterministic := "Deterministic checks: not applicable (no parser could decide)."
	if ver.Decisive() {
		deterministic = fmt.Sprintf(
			"Deterministic checks: passed=%v score=%.2f verifiers=%v reasons=%v. These are authoritative; do not contradict them.",
			ver.Passed, ver.Score, ver.Verifiers, ver.Reasons)
	}
	instructions := rubricBlock + fmt.Sprintf(
		"EVALUATE CANDIDATE %d of %d (artifact_id=%s). %s "+
			"Apply the §19 acceptance-criteria check to the candidate content in the "+
			"CANDIDATE ARTIFACT section above. "+
			"Output JSON only: {passed, score (0.0-1.0), failed_tests, missing_criteria, "+
			"security_issues, style_issues, better_than_previous, confidence (0.0-1.0), summary}.",
		idx, total, cand.ID, deterministic)

	// M5-T5: the candidate bytes are §11.2 §12 (tier 3, never evicted)
	// rather than a concatenation inside the §13 instructions.
	ctxCandidates := []prompt.CandidateArtifact{{Kind: "candidate", Content: string(content)}}
	plan := e.planFor(ctx, j, p, t)
	judgeRole := e.roleFor(plan, llm.PhaseEvaluating, llm.RoleSecurity)
	resp, err := e.call(ctx, j, p, t, llm.PhaseEvaluating, judgeRole, instructions, ctxCandidates)
	if err != nil {
		return evaluation.Record{}, err
	}

	verdict, coercions, err := parseEvalVerdict(resp.Content)
	if err != nil {
		return evaluation.Record{}, fmt.Errorf("parsing security verdict: %w", err)
	}
	if len(coercions) > 0 {
		// M3-T7.5: the model emitted valid JSON with drifted types. The
		// verdict was recovered by coercion; record it so the raw
		// conformance rate is measurable rather than hidden.
		e.audit(ctx, j.ID, map[string]any{
			"event": "verdict_coerced", "phase": string(llm.PhaseEvaluating),
			"artifact_id": cand.ID, "fields": coercions,
		})
	}

	// F4-T3: deterministic evidence is authoritative. When a verifier
	// applied, its pass/fail and reasons override the LLM's prose; the
	// model may still add missing_criteria/security_issues the parser
	// cannot see.
	if ver.Decisive() {
		verdict.Passed = ver.Passed && len(verdict.MissingCriteria) == 0 && len(verdict.SecurityIssues) == 0
		if ver.Passed {
			verdict.FailedTests = nil
		} else {
			verdict.FailedTests = nil
			verdict.MissingCriteria = append(verdict.MissingCriteria, ver.Reasons...)
		}
	}

	rec := evaluation.NewRecord(j.ID, cand.ID)
	rec.ComparedAgainst = previousID
	// M3-T7.5b: the prompt asks for 0.0-1.0, but a model can still return a
	// percentage (the M3-T7 smoke saw 96 next to 0.96). Normalize
	// defensively so the §19.2 score and the strategy outcome stay on one
	// scale regardless of the model's unit choice.
	score := verdict.Score
	if score > 1 {
		score /= 100
		if score > 1 {
			score = 1
		}
	}
	rec.Score = score
	rec.PassedTests = verdict.Passed
	rec.FailedTests = verdict.FailedTests
	rec.MissingCriteria = verdict.MissingCriteria
	rec.SecurityIssues = verdict.SecurityIssues
	rec.StyleIssues = verdict.StyleIssues
	rec.BetterThanPrevious = verdict.BetterThanPrevious
	rec.Confidence = verdict.Confidence
	rec.Summary = verdict.Summary
	return e.eval.Create(ctx, rec)
}

// parseEvalVerdict extracts the JSON the security persona produced.
// A non-JSON response is a hard error — the §13.1 contract is
// "structured JSON output," and a wandering verdict is not a verdict
// at all. The brace-scan logic and the tolerant type-drift coercion
// live in `parseVerdictJSONCoerced` (ADR-0012 follow-up, M3-T7.5);
// this function pins the destination type and returns the coercion
// report so the caller can audit it.
func parseEvalVerdict(content string) (evalVerdict, []string, error) {
	return parseVerdictJSONCoerced[evalVerdict](content)
}
