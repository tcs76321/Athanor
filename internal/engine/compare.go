package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/policy"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
	"github.com/tcs76321/athanor/internal/verify"
)

// comparisonVerdict is the §13.1 Phase 6 JSON the security persona
// produces. The schema mirrors the §13.1 example verbatim; the
// engine enforces the §19.3 rule against it.
type comparisonVerdict struct {
	Winner              string   `json:"winner"` // "new" | "previous" | "none"
	Confidence          float64  `json:"confidence"`
	Reasons             []string `json:"reasons"`
	MissingRequirements []string `json:"missing_requirements"`
}

// phaseCompare (§13.1 Phase 6, M3-T1; F4-T3): pick the winner of the
// comparison. Two paths share one §19.3 guard:
//
//   - JudgeMode "llm" (default, pre-F4): the security persona produces a
//     structured JSON verdict and DecideWinner enforces the §19.3 rule.
//   - JudgeMode "verifier" (F4-T3): deterministic per-archetype verifiers
//     run first. A decisive result decides acceptance and the LLM is
//     skipped; a clean pass with no previous artifact accepts; otherwise
//     the LLM is the tiebreaker, and only if its family differs from the
//     generator's.
//
// The §19.3 guard remains a safety belt in both paths: the LLM cannot flip
// a losing candidate into a winner without record-backed confidence.
//
// Artifact status flow per §9.3:
//   - winner "new"      → candidate → accepted; previous → superseded
//   - winner "previous" → candidate → rejected; previous stays accepted
//   - winner "none"     → candidate → rejected; job → failed
func (e *Engine) phaseCompare(ctx context.Context, j job.Job) error {
	if e.eval == nil {
		return errors.New("engine: evaluation repo is nil (M3-T1; tests must pass it)")
	}
	if e.cfg == nil {
		return errors.New("engine: cfg is nil (compare phase requires config)")
	}
	p, t, err := e.contexts(ctx, j)
	if err != nil {
		return err
	}

	final, err := e.artifacts.LatestForJob(ctx, j.ID, finalKindFor(p.Archetype))
	if err != nil {
		return fmt.Errorf("loading final artifact for comparison: %w", err)
	}
	if err := e.artifacts.SetStatus(ctx, final.ID, artifact.StatusCandidate); err != nil {
		// A duplicate flip (e.g. a previous attempt left the
		// artifact as `candidate`) is not a hard error here.
		var ise *artifact.IllegalStatusError
		if !errors.As(err, &ise) {
			return fmt.Errorf("promoting final to candidate: %w", err)
		}
	}

	records, err := e.eval.ListByJob(ctx, j.ID)
	if err != nil {
		return fmt.Errorf("listing evaluation records: %w", err)
	}
	if len(records) == 0 {
		// Defensive: the engine only enters this phase with ≥1
		// evaluation record, but if a crash erased them we fail
		// loudly rather than silently accept.
		return errors.New("phaseCompare: no evaluation records persisted for this job")
	}

	// Previous-side context: the project's last accepted artifact, plus its
	// evaluation history (the "Previous-record summary" the judge uses to
	// calibrate "better than previous").
	var (
		previousID       string
		previousJobID    string
		previousRecords  []evaluation.Record
		previousAvgScore float64
		previousAvgConf  float64
	)
	if prev, err := e.artifacts.LatestAcceptedByProject(ctx, p.ID); err == nil {
		previousID = prev.ID
		previousJobID = prev.JobID
		if prs, perr := e.eval.ListByArtifact(ctx, prev.ID); perr == nil {
			previousRecords = prs
			if n := len(prs); n > 0 {
				var sumScore, sumConf float64
				for _, r := range prs {
					sumScore += r.Score
					sumConf += r.Confidence
				}
				previousAvgScore = sumScore / float64(n)
				previousAvgConf = sumConf / float64(n)
			}
		} else {
			// Best-effort: a corrupt or partially-migrated DB should
			// not fail the comparison phase.
			e.audit(ctx, j.ID, map[string]any{
				"event":       "previous_records_load_failed",
				"previous_id": prev.ID,
				"error":       perr.Error(),
			})
		}
	} else if !errors.Is(err, artifact.ErrNotFound) {
		return fmt.Errorf("loading previous accepted artifact: %w", err)
	}

	// M5-T5: the candidate's bytes ride §11.2 §12 (tier 3, never evicted).
	// ADR-0013's 4 KB comparison limit is applied before assembly because
	// the assembler renders tier 3 verbatim and never truncates.
	candidateContent, _ := osReadFileLimited(final.StoragePath, comparisonContentLimit)
	plan := e.planFor(ctx, j, p, t)
	judgeRole := e.roleFor(plan, llm.PhaseComparing, llm.RoleSecurity)

	// F4-T3/T4: run the deterministic verifiers on the final artifact in
	// every mode. In verifier mode they decide; in llm mode they are the
	// reward-hacking guard (a failed verifier overrides an LLM "new").
	var ver verify.Result
	{
		var verr error
		ver, _, verr = e.verifyCandidate(ctx, j, p, t, candidateContent, 0)
		if verr != nil {
			return verr
		}
		e.auditVerification(ctx, j.ID, "final", ver)
	}
	genRole := e.roleFor(plan, policy.PhaseDiverging, llm.RoleMain)
	genFamily, judgeFamily, crossOK := e.crossFamily(genRole, judgeRole)

	var (
		verdict     comparisonVerdict
		judgeCalled bool
		decided     bool
	)
	if plan.JudgeMode == policy.JudgeVerifier && ver.Decisive() {
		switch {
		case !ver.Passed:
			// A failed deterministic verifier cannot be overridden.
			verdict = comparisonVerdict{Winner: loserWinner(previousID != ""), Confidence: 1,
				Reasons: append([]string{"deterministic verifier reject"}, ver.Reasons...)}
			decided = true
		case previousID == "":
			// Clean pass with nothing to compare against: accept.
			verdict = comparisonVerdict{Winner: "new", Confidence: 1,
				Reasons: []string{"deterministic verifier pass (no previous artifact)"}}
			decided = true
		}
	}
	if !decided && plan.JudgeMode == policy.JudgeVerifier &&
		!crossOK && e.cfg.Execution.RequireCrossFamilyValue() {
		// The judge shares the generator's family; a correlated judge adds
		// no signal, so do not consult it. Without decisive deterministic
		// evidence, decline the new artifact (conservative).
		e.audit(ctx, j.ID, map[string]any{
			"event": "judge_family_mismatch", "generator_family": genFamily,
			"judge_family": judgeFamily, "judge_role": judgeRole, "generator_role": genRole,
		})
		verdict = comparisonVerdict{Winner: loserWinner(previousID != ""), Confidence: 0,
			Reasons: []string{"no cross-family judge available; deterministic evidence not decisive"}}
		decided = true
	}
	if !decided {
		instructions := buildComparisonInstructions(final, records, previousID, previousRecords, previousAvgScore, previousAvgConf)
		v, err := e.runComparisonJudge(ctx, j, p, t, judgeRole, instructions, candidateContent, plan.JudgeCount)
		if err != nil {
			return err
		}
		verdict = v
		judgeCalled = true
	}

	// §19.3 deterministic guard (unchanged contract): applies to the LLM
	// verdict and to the deterministic ones alike. A "new" verdict with no
	// record backing is downgraded.
	threshold := e.cfg.Execution.MinJudge()
	verdict = DecideWinner(verdict, records, threshold, previousID != "")

	// F4-T4 reward-hacking guard: a decisive verifier failure overrides an
	// LLM "new". A judge that accepts a candidate the parser rejected is
	// either mistaken or gaming the rubric; the verifier wins.
	if resolved, overridden := resolveRewardHack(verdict, ver, previousID != ""); overridden {
		verdict = resolved
		e.audit(ctx, j.ID, map[string]any{
			"event": "judge_verifier_contradiction", "resolved_to": verdict.Winner,
			"verifiers": ver.Verifiers, "reasons": ver.Reasons,
		})
	}

	// F4-T6 cost-aware acceptance: a quality tie at materially lower token
	// cost wins. Applied only when the verdict declines the new artifact
	// (winner "previous") and the new artifact's best score is within the
	// tie margin of the previous's average score.
	if e.cfg.Execution.CostAwareEnabled() && previousID != "" && verdict.Winner == "previous" {
		newScore := e.maxEvaluationScore(ctx, j.ID)
		newTokens, _ := e.eventStats(ctx, j.ID)
		prevTokens := e.jobTokenCost(ctx, previousJobID)
		if costTieWins(newScore, previousAvgScore, e.cfg.Execution.QualityTieMarginValue(), newTokens, prevTokens) {
			verdict.Winner = "new"
			verdict.Confidence = 1
			verdict.Reasons = append(verdict.Reasons, fmt.Sprintf(
				"cost-aware: quality tie (%.2f vs %.2f) at lower token cost (%d < %d)",
				newScore, previousAvgScore, newTokens, prevTokens))
			e.audit(ctx, j.ID, map[string]any{
				"event": "cost_aware_accept", "new_tokens": newTokens, "previous_tokens": prevTokens,
				"new_score": newScore, "previous_score": previousAvgScore,
			})
		}
	}

	e.audit(ctx, j.ID, map[string]any{
		"event":            "verification_decision",
		"judge_mode":       string(plan.JudgeMode),
		"judge_called":     judgeCalled,
		"verifier_applied": ver.Applied,
		"verifier_passed":  ver.Passed,
		"verifiers":        ver.Verifiers,
		"generator_family": genFamily,
		"judge_family":     judgeFamily,
		"cross_family_ok":  crossOK,
	})
	tokenCost, _ := e.eventStats(ctx, j.ID)
	wallMS := int64(0)
	if j.StartedAt != nil {
		wallMS = time.Since(*j.StartedAt).Milliseconds()
	}
	e.audit(ctx, j.ID, map[string]any{
		"event":                   "comparison",
		"winner":                  verdict.Winner,
		"confidence":              verdict.Confidence,
		"reasons":                 verdict.Reasons,
		"missing_requirements":    verdict.MissingRequirements,
		"new_artifact_id":         final.ID,
		"previous_id":             previousID,
		"records":                 len(records),
		"judge_called":            judgeCalled,
		"token_cost":              tokenCost,
		"wall_time_ms":            wallMS,
		"previous_records_count":  len(previousRecords),
		"previous_avg_score":      previousAvgScore,
		"previous_avg_confidence": previousAvgConf,
	})

	// §9.3 status transitions.
	switch verdict.Winner {
	case "new":
		if previousID != "" {
			// M3-T3 commit 3.2: the supersede + accept pair is one atomic
			// operation; a crash between them cannot leave the project
			// with zero accepted artifacts.
			if err := e.artifacts.SupersedeAndAccept(ctx, previousID, final.ID); err != nil {
				return fmt.Errorf("supersede+accept: %w", err)
			}
		} else {
			if err := e.artifacts.SetStatus(ctx, final.ID, artifact.StatusAccepted); err != nil {
				return fmt.Errorf("accepting new: %w", err)
			}
		}
		// F3-T5 (§14, ADR-0030): record the accepted artifact to the
		// project repository on an agent branch. Best-effort.
		e.recordGitCommit(ctx, p, final)
		_, err = e.jobs.Transition(ctx, j.ID, job.StateCompleted)
		return err
	case "previous":
		if err := e.artifacts.SetStatus(ctx, final.ID, artifact.StatusRejected); err != nil {
			return fmt.Errorf("rejecting new: %w", err)
		}
		_, err = e.jobs.Transition(ctx, j.ID, job.StateCompleted)
		return err
	default: // "none"
		if err := e.artifacts.SetStatus(ctx, final.ID, artifact.StatusRejected); err != nil {
			return fmt.Errorf("rejecting new (none winner): %w", err)
		}
		_, err = e.jobs.Transition(ctx, j.ID, job.StateFailed)
		return err
	}
}

// loserWinner is the winner to record when the new artifact cannot be
// accepted: the previous artifact if one exists, otherwise none.
func loserWinner(hasPrevious bool) string {
	if hasPrevious {
		return "previous"
	}
	return "none"
}

// resolveRewardHack applies the F4-T4 reward-hacking guard: when a
// deterministic verifier decisively failed but the verdict would accept the
// new artifact, the verifier wins. It returns the (possibly rewritten)
// verdict and whether an override occurred.
func resolveRewardHack(verdict comparisonVerdict, ver verify.Result, hasPrevious bool) (comparisonVerdict, bool) {
	if !ver.Decisive() || ver.Passed || verdict.Winner != "new" {
		return verdict, false
	}
	verdict.Winner = loserWinner(hasPrevious)
	verdict.Confidence = 1
	verdict.Reasons = append(verdict.Reasons,
		"reward-hacking guard: deterministic verifier failed; LLM 'new' overridden")
	return verdict, true
}

// costTieWins reports whether the new artifact wins a near-tie on quality by
// spending fewer tokens (F4-T6). A zero cost is unknown and never wins.
func costTieWins(newScore, prevScore, margin float64, newTokens, prevTokens int) bool {
	if newTokens <= 0 || prevTokens <= 0 {
		return false
	}
	return newScore >= prevScore-margin && newTokens < prevTokens
}

// jobTokenCost reads a job's recorded strategy token cost (0 when absent or
// the outcome has not been captured).
func (e *Engine) jobTokenCost(ctx context.Context, jobID string) int {
	if e.db == nil || jobID == "" {
		return 0
	}
	var cost int
	if err := e.db.DB().QueryRowContext(ctx,
		`SELECT token_cost FROM strategy_outcomes WHERE job_id = ?`, jobID).Scan(&cost); err != nil {
		return 0
	}
	return cost
}

// runComparisonJudge calls the LLM comparison judge. When count > 1 it
// requires a majority (quorum, F4-T4) and audits the vote; a call that fails
// to parse abstains rather than failing the job. When every call fails it
// returns an error (fail loud — a judge that cannot answer is not a
// verdict).
func (e *Engine) runComparisonJudge(ctx context.Context, j job.Job, p project.Project, t project.Task,
	judgeRole, instructions, content string, count int) (comparisonVerdict, error) {

	if count <= 1 {
		return e.comparisonCall(ctx, j, p, t, judgeRole, instructions, content)
	}
	var (
		best    comparisonVerdict
		gotBest bool
		votes   []string
	)
	for i := 0; i < count; i++ {
		v, err := e.comparisonCall(ctx, j, p, t, judgeRole, instructions, content)
		if err != nil {
			continue // abstention
		}
		votes = append(votes, v.Winner)
		if !gotBest {
			best, gotBest = v, true
		}
	}
	if !gotBest {
		return comparisonVerdict{}, fmt.Errorf("judge quorum: all %d comparison calls failed", count)
	}
	majority := majorityWinner(votes)
	e.audit(ctx, j.ID, map[string]any{
		"event": "judge_quorum", "count": count, "votes": votes, "majority": majority,
	})
	if majority != "" {
		best.Winner = majority
	}
	return best, nil
}

// comparisonCall is one LLM comparison invocation plus its parse and
// coercion audit.
func (e *Engine) comparisonCall(ctx context.Context, j job.Job, p project.Project, t project.Task,
	judgeRole, instructions, content string) (comparisonVerdict, error) {

	resp, err := e.call(ctx, j, p, t, llm.PhaseComparing, judgeRole, instructions,
		[]prompt.CandidateArtifact{{Kind: "candidate", Content: content}})
	if err != nil {
		return comparisonVerdict{}, err
	}
	v, coercions, err := parseComparisonVerdict(resp.Content)
	if err != nil {
		// M3-T3 commit 3.3: the unknown-winner case is a typed error. Audit
		// the downgrade and proceed with Winner "none" (the safe default).
		if isUnknownWinnerErr(err) {
			e.audit(ctx, j.ID, map[string]any{
				"event": "comparison_unknown_winner_downgraded", "raw_winner": v.Winner,
				"downgraded_to": "none",
			})
			v.Winner = "none"
		} else {
			return comparisonVerdict{}, fmt.Errorf("parsing comparison verdict: %w", err)
		}
	}
	if len(coercions) > 0 {
		e.audit(ctx, j.ID, map[string]any{
			"event": "verdict_coerced", "phase": string(llm.PhaseComparing), "fields": coercions,
		})
	}
	return v, nil
}

// majorityWinner returns the winner with a strict majority of votes, or ""
// when there is no majority.
func majorityWinner(votes []string) string {
	if len(votes) == 0 {
		return ""
	}
	counts := map[string]int{}
	for _, v := range votes {
		counts[v]++
	}
	for _, w := range []string{"new", "previous", "none"} {
		if counts[w]*2 > len(votes) {
			return w
		}
	}
	return ""
}

// buildComparisonInstructions is the prompt for the security persona.
// It includes the final artifact content (truncated to 4 KB) and
// the EvaluationRecord set; the persona must produce the §13.1
// structured JSON. M3-T3 commit 3.1 adds a "Previous-record
// summary" section that lists the previous accepted artifact's
// own evaluation history, so the judge can reason about how the
// previous scored when it was a candidate (not just that it
// exists).
// comparisonContentLimit is ADR-0013's comparison content bound: the judge
// sees the head of the candidate, and the truncation is disclosed in the
// instructions rather than hidden (M5-T5 moved the bytes into §11.2 §12,
// so the limit is applied here — before assembly — because the assembler
// renders tier 3 verbatim).
const comparisonContentLimit = 4096

func buildComparisonInstructions(
	final artifact.Artifact,
	records []evaluation.Record,
	previousID string,
	previousRecords []evaluation.Record,
	previousAvgScore float64,
	previousAvgConf float64,
) string {
	var b strings.Builder
	fmt.Fprintf(&b, "COMPARISON. Decide whether to accept the new artifact.\n"+
		"new artifact_id: %s\nprevious artifact_id: %s\n", final.ID, previousID)
	b.WriteString("\nEvaluationRecords for the new artifact:\n")
	for i, r := range records {
		fmt.Fprintf(&b, "  record %d: artifact=%s, passed_tests=%v, missing_criteria=%v, security_issues=%v, better_than_previous=%v, confidence=%.2f, summary=%q\n",
			i+1, r.ArtifactID, r.PassedTests, r.MissingCriteria, r.SecurityIssues, r.BetterThanPrevious, r.Confidence, r.Summary)
	}
	if len(previousRecords) > 0 {
		// §3.1: surface the previous's own evaluation
		// history. The judge uses this to calibrate
		// "better than previous" (a non-trivial claim
		// when the previous scored 0.95 vs 0.30).
		b.WriteString("\nPrevious-record summary (the previous's own evaluation history):\n")
		fmt.Fprintf(&b, "  count: %d\n", len(previousRecords))
		fmt.Fprintf(&b, "  avg_score: %.2f\n", previousAvgScore)
		fmt.Fprintf(&b, "  avg_confidence: %.2f\n", previousAvgConf)
		for i, r := range previousRecords {
			fmt.Fprintf(&b, "  record %d: artifact=%s, passed_tests=%v, better_than_previous=%v, confidence=%.2f, summary=%q\n",
				i+1, r.ArtifactID, r.PassedTests, r.BetterThanPrevious, r.Confidence, r.Summary)
		}
	}
	fmt.Fprintf(&b, "\nThe new artifact's content is the CANDIDATE ARTIFACT section above "+
		"(truncated to %d bytes per §19 disclosure practice).\n", comparisonContentLimit)
	b.WriteString("\nOutput JSON only: {winner: \"new\"|\"previous\"|\"none\", confidence: 0.0-1.0, reasons: [...], missing_requirements: [...]}")
	return b.String()
}

// osReadFileLimited reads up to `limit` bytes from path. Failures
// are non-fatal: the comparison proceeds with an empty content
// section rather than aborting the whole job.
func osReadFileLimited(path string, limit int) (string, error) {
	if path == "" {
		return "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, limit)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return "", err
	}
	return string(buf[:n]), nil
}

// errUnknownWinner is the sentinel parseComparisonVerdict
// returns when the security persona's verdict has a `winner`
// field whose value is outside the closed set
// `{"new","previous","none"}`. M3-T3 commit 3.3 changed this
// from a silent downgrade (v.Winner = "none") to a typed
// error so the engine can audit the downgrade explicitly
// via the `comparison_unknown_winner_downgraded` event.
//
// The parseComparisonVerdict function still trims
// whitespace (the M3-T1 carry-over polish item from
// `docs/m3-t1-plan.md:128–136`); a value that becomes a
// known string after trimming is honored.
var errUnknownWinner = errors.New("compare: unknown winner value")

// isUnknownWinnerErr is the errors.Is-compatible check.
// Errors are wrapped via fmt.Errorf("...%w", errUnknownWinner)
// so the caller can branch on the type without a type
// assertion.
func isUnknownWinnerErr(err error) bool {
	return errors.Is(err, errUnknownWinner)
}

// parseComparisonVerdict is the comparison-phase twin of
// parseEvalVerdict. Same lenient-wrapping tolerance.
//
// M3-T3 commit 3.3 added two refinements on top of the
// M3-T1 version: (a) the parsed Winner field is TrimSpace'd
// before the closed-set check, so a model that emits
// `  "new"\n` (whitespace around the string) is honored; (b)
// an unknown winner is reported via errUnknownWinner
// rather than silently downgraded to "none", so the engine
// can audit the downgrade.
//
// The brace-scan logic lives in `parseVerdictJSON`
// (ADR-0012 follow-up); this function is a thin wrapper
// that pins the destination type, applies the §3.3
// refinements, and returns the typed errUnknownWinner.
func parseComparisonVerdict(content string) (comparisonVerdict, []string, error) {
	v, coercions, err := parseVerdictJSONCoerced[comparisonVerdict](content)
	if err != nil {
		return v, coercions, err
	}
	// M3-T3 commit 3.3: trim whitespace before the
	// closed-set check. The M3-T1 carry-over polish item
	// in `docs/m3-t1-plan.md:128–136` was that `  "new"\n`
	// or `"new\n"` would normalize to "none" — this trim
	// fixes that.
	v.Winner = strings.TrimSpace(v.Winner)
	switch v.Winner {
	case "new", "previous", "none":
		// known; honor as-is
	default:
		// Unknown: return the typed error so the caller
		// can audit the downgrade. The engine's phaseCompare
		// catches this, emits a
		// `comparison_unknown_winner_downgraded` audit
		// event, and proceeds with the verdict's Winner
		// set to "none" (the safe default).
		return v, coercions, fmt.Errorf("winner %q: %w", v.Winner, errUnknownWinner)
	}
	return v, coercions, nil
}
