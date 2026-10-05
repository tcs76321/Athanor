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
	"github.com/tcs76321/athanor/internal/toolenvelope"
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
		if a.JobID == j.ID && a.Kind == artifact.KindProposal {
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

	var (
		testsPassed  bool
		failedTests  []string
		testExitCode int
	)
	// F3-T4 (§6.2, ADR-0031): the test command is the project's, not a
	// hard-coded `pytest -q`. A code project with no override resolves to
	// the built-in default.
	testCommand := p.TestCommand()
	// M3-T2 (ADR-0014): for `code` archetype, execute the candidate's
	// code in the Job Pod before the security-persona verdict. The
	// `code_executed` audit event + `KindCode` artifact persistence
	// move here from the former M2-T4 sub-step in `phaseSynthesize`.
	if p.Archetype == project.ArchetypeCode && e.runner != nil {
		// M2-T4b.5 (ADR-0024 §2): make the job's Job Pod exist before the
		// pod sub-steps, so the per-job token the runner presents
		// (TokenFor) is available. Best-effort: a nil seam is a no-op and
		// a start failure is audited, leaving the sub-step to surface its
		// own error if the pod is genuinely required.
		e.ensurePod(ctx, j)
		if err := e.runCodeInPod(ctx, j, p, t); err != nil {
			return evaluation.Record{}, err
		}
	}
	if p.Archetype == project.ArchetypeCode && e.runner != nil {
		// §8.1 "tracked sub-state": running_tests is an event, not a
		// column. We enter and exit the substate around the
		// `runner.RunTests` call so the audit log shows the duration.
		e.audit(ctx, j.ID, map[string]any{
			"event":     "substate_entered",
			"phase":     string(llm.PhaseEvaluating),
			"substate":  "running_tests",
			"candidate": idx,
		})
		req := toolenvelope.ExecuteRequest{Command: testCommand}
		res, runErr := e.runner.RunTests(ctx, j.ID, req)
		if runErr == nil {
			testsPassed = res.ExitCode == 0
			testExitCode = res.ExitCode
			if !testsPassed {
				// The runner reports an exit code, not per-test names;
				// record the command that failed so the record is
				// actionable (the per-test-name surface is future work).
				failedTests = []string{testCommand}
			}
		} else if !errors.Is(runErr, toolenvelope.ErrToolDisallowed) {
			e.audit(ctx, j.ID, map[string]any{
				"event":     "tests_run_failed",
				"candidate": idx,
				"error":     runErr.Error(),
			})
		}
		e.audit(ctx, j.ID, map[string]any{
			"event":     "substate_exited",
			"phase":     string(llm.PhaseEvaluating),
			"substate":  "running_tests",
			"candidate": idx,
			"exit_code": testExitCode,
		})
	} else if p.Archetype == project.ArchetypeCode {
		// No runner wired (unit test mode): leave testsPassed = false
		// and let the LLM persona make the call via MissingCriteria.
		testsPassed = false
	}

	content, err := e.artifacts.ReadContent(ctx, cand.ID)
	if err != nil {
		return evaluation.Record{}, fmt.Errorf("reading candidate content: %w", err)
	}

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

	instructions := rubricBlock + fmt.Sprintf(
		"EVALUATE CANDIDATE %d of %d (artifact_id=%s). "+
			"Tests already ran in the Job Pod: passed=%v, failed_tests=%v. "+
			"Apply the §19 acceptance-criteria check to the candidate content in the "+
			"CANDIDATE ARTIFACT section above. "+
			"Output JSON only: {passed, score (0.0-1.0), failed_tests, missing_criteria, "+
			"security_issues, style_issues, better_than_previous, confidence (0.0-1.0), summary}.",
		idx, total, cand.ID, testsPassed, failedTests)

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

	// Reconcile the LLM verdict with the deterministic test result:
	// the LLM's `passed` field is overridden by the pod's verdict
	// when the test command actually ran.
	if p.Archetype == project.ArchetypeCode && e.runner != nil {
		verdict.Passed = testsPassed && len(verdict.MissingCriteria) == 0 && len(verdict.SecurityIssues) == 0
		if testsPassed {
			// Clear test-only failures if the run was actually green.
			verdict.FailedTests = nil
		} else {
			verdict.FailedTests = failedTests
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
