package phases

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/engine/acceptance"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/notebook"
	"github.com/tcs76321/athanor/internal/policy"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
	"github.com/tcs76321/athanor/internal/verify"
)

// The §13.1 Phase 6 comparison logic. The verdict shape and every acceptance
// guard live in internal/engine/acceptance; this file gathers evidence, calls
// the judge, and applies the artifact status transitions.

// phaseCompare (§13.1 Phase 6, M3-T1; F4-T3): pick the winner of the
// comparison. Two paths share one §19.3 guard:
//
//   - JudgeMode "llm" (default, pre-F4): the security persona produces a
//     structured JSON verdict and acceptance.Decide enforces the §19.3 rule.
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
func (d *Deps) phaseCompare(ctx context.Context, j job.Job) error {
	if d.Eval == nil {
		return errors.New("engine: evaluation repo is nil (M3-T1; tests must pass it)")
	}
	if d.Cfg == nil {
		return errors.New("engine: cfg is nil (compare phase requires config)")
	}
	p, t, err := d.contexts(ctx, j)
	if err != nil {
		return err
	}

	final, err := d.Artifacts.LatestForJob(ctx, j.ID, finalKindFor(project.CapabilityFor(p, t)))
	if err != nil {
		return fmt.Errorf("loading final artifact for comparison: %w", err)
	}
	// Crash window: acceptance and the job's terminal transition are separate
	// writes. If the process died after the artifact was accepted but before
	// the job moved to completed, recovery re-enters this phase with the final
	// artifact already `accepted`. Comparing it against itself would try to
	// supersede and re-accept the same row and fail. Finish the transition
	// idempotently instead.
	if final.Status == artifact.StatusAccepted {
		if final.GitCommit == "" {
			d.recordGitCommit(ctx, p, final)
		}
		d.audit(ctx, j.ID, map[string]any{
			"event": "comparison_recovered_accepted", "artifact_id": final.ID,
		})
		_, err = d.Jobs.Transition(ctx, j.ID, job.StateCompleted)
		return err
	}
	if err := d.Artifacts.SetStatus(ctx, final.ID, artifact.StatusCandidate); err != nil {
		// A duplicate candidate promotion (d.g. a previous attempt left the
		// artifact as `candidate`) is not a hard error here; any other status
		// mismatch is.
		var ise *artifact.IllegalStatusError
		if !errors.As(err, &ise) || ise.From != artifact.StatusCandidate || ise.To != artifact.StatusCandidate {
			return fmt.Errorf("promoting final to candidate: %w", err)
		}
	}

	records, err := d.Eval.ListByJob(ctx, j.ID)
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
	if prev, err := d.Artifacts.LatestAcceptedByProject(ctx, p.ID); err == nil {
		previousID = prev.ID
		previousJobID = prev.JobID
		if prs, perr := d.Eval.ListByArtifact(ctx, prev.ID); perr == nil {
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
			d.audit(ctx, j.ID, map[string]any{
				"event":       "previous_records_load_failed",
				"previous_id": prev.ID,
				"error":       perr.Error(),
			})
		}
	} else if !errors.Is(err, artifact.ErrNotFound) {
		return fmt.Errorf("loading previous accepted artifact: %w", err)
	}

	// Read the final artifact's full content once, hash-verified. The
	// deterministic verifier must run against the complete bytes — for a
	// code archetype it writes them to the Job Pod and runs the real test
	// command, so it must never see a truncated copy. The LLM judge gets a
	// separately truncated head (ADR-0013's 4 KB limit) below.
	fullContent, err := d.Artifacts.ReadContent(ctx, final.ID)
	if err != nil {
		return fmt.Errorf("reading final artifact for comparison: %w", err)
	}
	plan := d.planFor(ctx, j, p, t)
	judgeRole := d.roleFor(plan, llm.PhaseComparing, llm.RoleSecurity)

	// F4-T3/T4: run the deterministic verifiers on the final artifact in
	// every mode. In verifier mode they decide; in llm mode they are the
	// reward-hacking guard (a failed verifier overrides an LLM "new").
	var ver verify.Result
	{
		var verr error
		ver, _, verr = d.verifyCandidate(ctx, j, p, t, string(fullContent), 0)
		if verr != nil {
			return verr
		}
		d.auditVerification(ctx, j.ID, "final", ver)
	}
	genRole := d.roleFor(plan, policy.PhaseDiverging, llm.RoleMain)
	genFamily, judgeFamily, crossOK := d.actualCrossFamily(ctx, j, p, t, genRole, judgeRole)
	// known-issues D1: the final artifact's record describes the bytes actually
	// being compared. Build it before the decision so the verifier path can
	// use its deterministic better-than-previous signal.
	finalRecord := acceptance.FinalRecord(j, final, previousID, previousAvgScore, ver)

	var (
		verdict     acceptance.Verdict
		judgeCalled bool
		decided     bool
	)
	if plan.JudgeMode == policy.JudgeVerifier && ver.HardDecisive() {
		switch {
		case !ver.HardPassed:
			// A failed objective verifier cannot be overridden.
			verdict = acceptance.Verdict{Winner: acceptance.LoserWinner(previousID != ""), Confidence: 1,
				Reasons: append([]string{"deterministic verifier reject"}, ver.Reasons...)}
			decided = true
		case previousID == "":
			// Clean pass with nothing to compare against: accept.
			verdict = acceptance.Verdict{Winner: "new", Confidence: 1,
				Reasons: []string{"deterministic verifier pass (no previous artifact)"}}
			decided = true
		case finalRecord.BetterThanPrevious:
			// A clean pass at or above the previous accepted artifact's score
			// is decisive. The §19.3 guard still runs below and can only
			// confirm this record-backed verdict.
			verdict = acceptance.Verdict{Winner: "new", Confidence: 1,
				Reasons: []string{"deterministic verifier pass at or above the previous accepted score"}}
			decided = true
		}
	}
	if !decided && plan.JudgeMode == policy.JudgeVerifier &&
		!crossOK && d.Cfg.Execution.RequireCrossFamilyValue() && ver.HardDecisive() && ver.HardPassed {
		// No cross-family judge is available. The deterministic verifier
		// passed, so refusing every follow-up artifact would make a
		// single-model-family install unable to accept any code task after the
		// first. Accept the verified artifact and record why, instead of
		// silently turning every later task into a rejected artifact under a
		// "completed" job.
		d.audit(ctx, j.ID, map[string]any{
			"event": "judge_family_mismatch", "generator_family": genFamily,
			"judge_family": judgeFamily, "judge_role": judgeRole, "generator_role": genRole,
			"resolution": "deterministic verifier pass; cross-family judge unavailable",
		})
		verdict = acceptance.Verdict{Winner: "new", Confidence: 1,
			Reasons: []string{"deterministic verifier pass; no cross-family judge available"}}
		decided = true
	}
	if !decided && plan.JudgeMode == policy.JudgeVerifier &&
		!crossOK && d.Cfg.Execution.RequireCrossFamilyValue() {
		// The judge shares the generator's family; a correlated judge adds
		// no signal, so do not consult it. Without decisive deterministic
		// evidence, decline the new artifact (conservative).
		d.audit(ctx, j.ID, map[string]any{
			"event": "judge_family_mismatch", "generator_family": genFamily,
			"judge_family": judgeFamily, "judge_role": judgeRole, "generator_role": genRole,
		})
		verdict = acceptance.Verdict{Winner: acceptance.LoserWinner(previousID != ""), Confidence: 0,
			Reasons: []string{"no cross-family judge available; deterministic evidence not decisive"}}
		decided = true
	}
	if !decided && d.Cfg.Execution.PairwiseJudgeEnabled() && previousID != "" {
		prevContent, perr := d.Artifacts.ReadContent(ctx, previousID)
		if perr != nil {
			d.audit(ctx, j.ID, map[string]any{
				"event": "pairwise_judge_fallback", "reason": "previous content unavailable",
				"error": perr.Error(),
			})
		} else if pv, ok, jerr := d.runPairwiseJudge(ctx, j, p, t, judgeRole,
			string(fullContent), string(prevContent)); jerr != nil {
			d.audit(ctx, j.ID, map[string]any{
				"event": "pairwise_judge_fallback", "reason": "judge call failed",
				"error": jerr.Error(),
			})
		} else if ok {
			verdict = pv
			judgeCalled = true
			decided = true
		}
	}
	if !decided {
		instructions := buildComparisonInstructions(final, records, previousID, previousRecords, previousAvgScore, previousAvgConf)
		judgeContent := truncateForLimit(string(fullContent), comparisonContentLimit)
		v, err := d.runComparisonJudge(ctx, j, p, t, judgeRole, instructions, judgeContent, plan.JudgeCount)
		if err != nil {
			return err
		}
		verdict = v
		judgeCalled = true
	}

	// §19.3 deterministic guard (unchanged contract): applies to the LLM
	// verdict and to the deterministic ones alike. A "new" verdict with no
	// record backing is downgraded.
	//
	// known-issues D1: the guard now consults an EvaluationRecord describing
	// the synthesized FINAL artifact (not just the pre-synthesis proposals),
	// so §19.3 reads the bytes actually being compared. The record is
	// persisted after the cost-aware block below so the proposal-based
	// max-score there is unchanged.
	threshold := d.Cfg.Execution.MinJudge()
	guardRecords := append(append([]evaluation.Record(nil), records...), finalRecord)
	preGuardWinner := verdict.Winner
	verdict = acceptance.Decide(verdict, guardRecords, threshold, previousID != "")

	// F4-T4 reward-hacking guard: a decisive verifier failure overrides an
	// LLM "new". A judge that accepts a candidate the parser rejected is
	// either mistaken or gaming the rubric; the verifier wins.
	if resolved, overridden := acceptance.ResolveRewardHack(verdict, ver, previousID != ""); overridden {
		verdict = resolved
		d.audit(ctx, j.ID, map[string]any{
			"event": "judge_verifier_contradiction", "resolved_to": verdict.Winner,
			"verifiers": ver.Verifiers, "reasons": ver.Reasons,
		})
	}

	// F4-T6 cost-aware acceptance: a quality tie at materially lower token
	// cost wins. Applied only when the verdict declines the new artifact
	// (winner "previous"), the final artifact has no decisive hard-verifier
	// failure, and both sides have a known quality score. A failed hard
	// verifier is authoritative (the reward-hacking guard) and must never be
	// undone by a cost tie.
	if d.Cfg.Execution.CostAwareEnabled() && previousID != "" && verdict.Winner == "previous" &&
		acceptance.CostAwareEligible(ver, finalRecord, previousAvgScore) {
		newScore := finalRecord.Score
		if !ver.Decisive() {
			// No deterministic verifier applied (d.g. media): fall back to
			// the best proposal score, which is the only quality signal the
			// run produced.
			newScore = d.maxEvaluationScore(ctx, j.ID)
		}
		newTokens, _ := d.eventStats(ctx, j.ID)
		prevTokens := d.jobTokenCost(ctx, previousJobID)
		if acceptance.CostTieWins(newScore, previousAvgScore, d.Cfg.Execution.QualityTieMarginValue(), newTokens, prevTokens) {
			verdict.Winner = "new"
			verdict.Confidence = 1
			verdict.Reasons = append(verdict.Reasons, fmt.Sprintf(
				"cost-aware: quality tie (%.2f vs %.2f) at lower token cost (%d < %d)",
				newScore, previousAvgScore, newTokens, prevTokens))
			d.audit(ctx, j.ID, map[string]any{
				"event": "cost_aware_accept", "new_tokens": newTokens, "previous_tokens": prevTokens,
				"new_score": newScore, "previous_score": previousAvgScore,
			})
		}
	}

	d.audit(ctx, j.ID, map[string]any{
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
	// known-issues D1: persist the final artifact's EvaluationRecord now that
	// the cost-aware max-score above was computed from the proposal records.
	if err := d.persistEvaluationRecord(ctx, finalRecord); err != nil {
		return err
	}
	tokenCost, _ := d.eventStats(ctx, j.ID)
	wallMS := int64(0)
	if j.StartedAt != nil {
		wallMS = time.Since(*j.StartedAt).Milliseconds()
	}
	d.audit(ctx, j.ID, map[string]any{
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
		// P0 diagnostics: these are the exact inputs the §19.3 guard saw.
		// A failed job whose external check passed is otherwise opaque:
		// the report can now distinguish "the judge said no" from "the
		// guard downgraded a supported new" without a live DB.
		"guard_pre_winner":           preGuardWinner,
		"guard_post_winner":          verdict.Winner,
		"guard_threshold":            threshold,
		"has_previous":               previousID != "",
		"final_record_passed":        finalRecord.PassedTests,
		"final_record_confidence":    finalRecord.Confidence,
		"final_record_better":        finalRecord.BetterThanPrevious,
		"final_verifier_applied":     ver.Applied,
		"final_verifier_hard":        ver.HardDecisive(),
		"final_verifier_hard_passed": ver.HardPassed,
	})

	d.note(ctx, j.ID, p.ID, notebook.KindDecision, "comparing",
		fmt.Sprintf("winner=%s confidence=%.2f reasons=%v", verdict.Winner, verdict.Confidence, verdict.Reasons))

	// §9.3 status transitions.
	if verdict.Winner != "new" {
		d.captureComparisonCorrection(ctx, p.ID, j.ID, final.ID, verdict)
	}
	switch verdict.Winner {
	case "new":
		if previousID != "" {
			// M3-T3 commit 3.2: the supersede + accept pair is one atomic
			// operation; a crash between them cannot leave the project
			// with zero accepted artifacts.
			if err := d.Artifacts.SupersedeAndAccept(ctx, previousID, final.ID); err != nil {
				return fmt.Errorf("supersede+accept: %w", err)
			}
		} else {
			if err := d.Artifacts.SetStatus(ctx, final.ID, artifact.StatusAccepted); err != nil {
				return fmt.Errorf("accepting new: %w", err)
			}
		}
		// F3-T5 (§14, ADR-0030): record the accepted artifact to the
		// project repository on an agent branch. Best-effort.
		d.recordGitCommit(ctx, p, final)
		_, err = d.Jobs.Transition(ctx, j.ID, job.StateCompleted)
		return err
	case "previous":
		if err := d.Artifacts.SetStatus(ctx, final.ID, artifact.StatusRejected); err != nil {
			return fmt.Errorf("rejecting new: %w", err)
		}
		_, err = d.Jobs.Transition(ctx, j.ID, job.StateCompleted)
		return err
	default: // "none"
		if err := d.Artifacts.SetStatus(ctx, final.ID, artifact.StatusRejected); err != nil {
			return fmt.Errorf("rejecting new (none winner): %w", err)
		}
		_, err = d.Jobs.Transition(ctx, j.ID, job.StateFailed)
		return err
	}
}

// persistEvaluationRecord writes one EvaluationRecord (nil-safe on the repo
// seam). Each record is paired with an audit event in the same transaction
// (evaluation.Repo.Create).
func (d *Deps) persistEvaluationRecord(ctx context.Context, rec evaluation.Record) error {
	if d.Eval == nil {
		return nil
	}
	if _, err := d.Eval.Create(ctx, rec); err != nil {
		return fmt.Errorf("persisting final evaluation record: %w", err)
	}
	return nil
}

// runComparisonJudge calls the LLM comparison judge. When count > 1 it
// requires a majority (quorum, F4-T4) and audits the vote; a call that fails
// to parse abstains rather than failing the job. When every call fails it
// returns an error (fail loud — a judge that cannot answer is not a
// verdict).
func (d *Deps) runComparisonJudge(ctx context.Context, j job.Job, p project.Project, t project.Task,
	judgeRole, instructions, content string, count int) (acceptance.Verdict, error) {

	if count <= 1 {
		return d.comparisonCall(ctx, j, p, t, judgeRole, instructions, content, 0)
	}
	var (
		best    acceptance.Verdict
		gotBest bool
		votes   []string
	)
	for i := 0; i < count; i++ {
		v, err := d.comparisonCall(ctx, j, p, t, judgeRole, instructions, content, i)
		if err != nil {
			continue // abstention
		}
		votes = append(votes, v.Winner)
		if !gotBest {
			best, gotBest = v, true
		}
	}
	if !gotBest {
		return acceptance.Verdict{}, fmt.Errorf("judge quorum: all %d comparison calls failed", count)
	}
	majority := acceptance.MajorityWinner(votes)
	d.audit(ctx, j.ID, map[string]any{
		"event": "judge_quorum", "count": count, "votes": votes, "majority": majority,
	})
	if majority != "" {
		best.Winner = majority
	}
	return best, nil
}

// comparisonCall is one LLM comparison invocation plus its parse and
// coercion audit. voteIndex salts the derived judgment seed so a quorum's
// votes are decorrelated (known-issues M7); it is 0 for a single call.
func (d *Deps) comparisonCall(ctx context.Context, j job.Job, p project.Project, t project.Task,
	judgeRole, instructions, content string, voteIndex int) (acceptance.Verdict, error) {

	resp, err := d.call(ctx, j, p, t, llm.PhaseComparing, judgeRole, instructions,
		[]prompt.CandidateArtifact{{Kind: "candidate", Content: content}},
		withSeedSalt(voteIndex))
	if err != nil {
		return acceptance.Verdict{}, err
	}
	v, coercions, err := acceptance.ParseComparisonVerdict(resp.Content)
	if err != nil {
		// M3-T3 commit 3.3: the unknown-winner case is a typed error. Audit
		// the downgrade and proceed with Winner "none" (the safe default).
		if acceptance.IsUnknownWinner(err) {
			d.audit(ctx, j.ID, map[string]any{
				"event": "comparison_unknown_winner_downgraded", "raw_winner": v.Winner,
				"downgraded_to": "none",
			})
			v.Winner = "none"
		} else {
			return acceptance.Verdict{}, fmt.Errorf("parsing comparison verdict: %w", err)
		}
	}
	if len(coercions) > 0 {
		d.audit(ctx, j.ID, map[string]any{
			"event": "verdict_coerced", "phase": string(llm.PhaseComparing), "fields": coercions,
		})
	}
	return v, nil
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

// ComparisonContentLimit is the exported form used by the root's behavioral
// tests to assert the judge head is bounded while the verifier sees the full
// artifact.
const ComparisonContentLimit = comparisonContentLimit

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

// truncateForLimit returns the first limit bytes of s. It is the in-memory
// equivalent of the former osReadFileLimited now that the full artifact
// content is read once (hash-verified) and shared between the verifier
// (full) and the judge (truncated). limit <= 0 returns s unchanged.
func truncateForLimit(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit]
}
