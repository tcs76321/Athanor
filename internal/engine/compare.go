package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/prompt"
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

// phaseCompare (§13.1 Phase 6, M3-T1): deterministically pick the
// winner of the comparison (§19.3). The LLM produces a structured
// JSON verdict, but the engine enforces the rule:
//
//	new wins ⟺ exists EvaluationRecord with
//	  better_than_previous == true AND confidence > min_judge_confidence
//
// The §19.3 guard is a safety belt: the LLM cannot flip a losing
// candidate into a winner by saying "winner: new" without backing
// it up. When the LLM's verdict conflicts with the guard, the
// engine downgrades (new → previous, or new → none if no previous
// exists) and audits the downgrade.
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

	// Previous-side context: the project's last accepted artifact,
	// plus its evaluation history. M3-T3 commit 3.1 adds the
	// "Previous-record summary" section to the comparison prompt
	// so the LLM judge can reason about how the previous scored
	// when it was a candidate, not just that it exists.
	var (
		previousID       string
		previousMeta     string
		previousRecords  []evaluation.Record
		previousAvgScore float64
		previousAvgConf  float64
	)
	if prev, err := e.artifacts.LatestAcceptedByProject(ctx, p.ID); err == nil {
		previousID = prev.ID
		previousMeta = prev.ID
		// Load the previous's own evaluation history (the
		// records that *describe* the previous when it was a
		// candidate). A previous with no records is fine —
		// the prompt just omits the "Previous-record summary"
		// section. The DB error is non-fatal: a corrupt or
		// partially-migrated DB should not fail the
		// comparison phase.
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
			// Best-effort: log and proceed with empty
			// previous-record history. The §19.3 guard
			// does not depend on the previous's records.
			e.audit(ctx, j.ID, map[string]any{
				"event":       "previous_records_load_failed",
				"previous_id": prev.ID,
				"error":       perr.Error(),
			})
		}
	} else if !errors.Is(err, artifact.ErrNotFound) {
		return fmt.Errorf("loading previous accepted artifact: %w", err)
	}

	instructions := buildComparisonInstructions(final, records, previousID, previousRecords, previousAvgScore, previousAvgConf)
	// M5-T5: the candidate's bytes ride §11.2 §12 (tier 3, never evicted).
	// ADR-0013's 4 KB comparison limit is applied here, before assembly,
	// because the assembler renders tier 3 verbatim and never truncates.
	candidateContent, _ := osReadFileLimited(final.StoragePath, comparisonContentLimit)
	plan := e.planFor(ctx, j, p, t)
	judgeRole := e.roleFor(plan, llm.PhaseComparing, llm.RoleSecurity)
	resp, err := e.call(ctx, j, p, t, llm.PhaseComparing, judgeRole, instructions,
		[]prompt.CandidateArtifact{{Kind: "candidate", Content: candidateContent}})
	if err != nil {
		return err
	}

	verdict, coercions, err := parseComparisonVerdict(resp.Content)
	if err != nil {
		// M3-T3 commit 3.3: the unknown-winner case is
		// now a typed error, not a silent downgrade. The
		// caller (here) audits the downgrade and proceeds
		// with the verdict's Winner set to "none" (the
		// safe default). All other parse errors are
		// hard-failures.
		if isUnknownWinnerErr(err) {
			e.audit(ctx, j.ID, map[string]any{
				"event":           "comparison_unknown_winner_downgraded",
				"raw_winner":      verdict.Winner,
				"downgraded_to":   "none",
				"new_artifact_id": final.ID,
			})
			verdict.Winner = "none"
		} else {
			return fmt.Errorf("parsing comparison verdict: %w", err)
		}
	}
	if len(coercions) > 0 {
		// M3-T7.5: valid JSON with drifted types, recovered by coercion.
		e.audit(ctx, j.ID, map[string]any{
			"event": "verdict_coerced", "phase": string(llm.PhaseComparing), "fields": coercions,
		})
	}

	// M3-T2 commit 2.5: the §19.3 deterministic guard is now
	// a pure function in decide.go. The caller resolves the
	// threshold (config default 0.7 when zero) and
	// `hasPrevious` (whether the project has a prior
	// accepted artifact) before the call.
	// §19.3 deterministic guard, parameterized by
	// `cfg.Execution.MinJudgeConfidence`. The default value
	// (0.7) is filled in once by `internal/config/defaults.go`
	// at load time. Setting the field explicitly to 0
	// disables the guard end-to-end — the LLM's verdict
	// stands even with no back-up record — matching
	// `DecideWinner`'s documented `threshold <= 0` disabled
	// sentinel. There is intentionally no rescue to 0.7 here:
	// the defaults package is the single source of the
	// default, and an operator who sets 0 expects the guard
	// to be off. `Execution.MinJudge()` resolves the
	// `*float64` (which is nil for "unset" and 0.0 for
	// "explicitly disabled") to a plain float64 with the
	// 0.7 default applied only when nil.
	threshold := e.cfg.Execution.MinJudge()
	verdict = DecideWinner(verdict, records, threshold, previousID != "")

	e.audit(ctx, j.ID, map[string]any{
		"event":                "comparison",
		"winner":               verdict.Winner,
		"confidence":           verdict.Confidence,
		"reasons":              verdict.Reasons,
		"missing_requirements": verdict.MissingRequirements,
		"new_artifact_id":      final.ID,
		"previous_id":          previousMeta,
		"records":              len(records),
		// M3-T3 commit 3.1: previous-record context so
		// post-mortem readers can see how the previous
		// scored when it was a candidate. Used by the
		// M3-T7 quality probe for calibration analysis.
		"previous_records_count":  len(previousRecords),
		"previous_avg_score":      previousAvgScore,
		"previous_avg_confidence": previousAvgConf,
	})

	// §9.3 status transitions.
	switch verdict.Winner {
	case "new":
		if previousID != "" {
			// M3-T3 commit 3.2: the supersede + accept
			// pair is now one atomic operation. A crash
			// between the two can no longer leave the
			// project with zero accepted artifacts; the
			// transaction commits both updates or neither.
			if err := e.artifacts.SupersedeAndAccept(ctx, previousID, final.ID); err != nil {
				return fmt.Errorf("supersede+accept: %w", err)
			}
		} else {
			if err := e.artifacts.SetStatus(ctx, final.ID, artifact.StatusAccepted); err != nil {
				return fmt.Errorf("accepting new: %w", err)
			}
		}
		// F3-T5 (§14, ADR-0030): record the accepted artifact to the
		// project repository on an agent branch. Best-effort; the
		// acceptance above is already committed, so a git failure is
		// audited rather than fatal.
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
// M3-T3 commit 3.3 added two refinements on top of the M3-T1
// version: (a) the parsed Winner field is TrimSpace'd
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
