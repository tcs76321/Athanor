// Package acceptance owns the engine's deterministic acceptance decisions:
// the §19.3 guard, the reward-hacking and cost-aware guards, the final-artifact
// record, the per-capability rubric, verdict parsing, and the comparison
// verdict shapes.
//
// It is pure: no database, no model call, no pod, no clock. The phase package
// gathers evidence and calls these functions; the root owns lifecycle and
// side effects. Keeping the decisions here makes them exhaustively testable and
// makes it structurally impossible for a new phase to bypass the §19.3 rule.
package acceptance

import (
	"fmt"

	"github.com/tcs76321/athanor/internal/evaluation"
)

// Verdict is the §13.1 Phase 6 comparison verdict the security persona
// produces. The engine enforces the §19.3 rule against it.
type Verdict struct {
	Winner              string   `json:"winner"` // "new" | "previous" | "none"
	Confidence          float64  `json:"confidence"`
	Reasons             []string `json:"reasons"`
	MissingRequirements []string `json:"missing_requirements"`
}

// EvaluationVerdict is the §13.1 Phase 3 evaluation verdict. The engine folds
// deterministic verifier evidence into it before persisting an
// EvaluationRecord.
type EvaluationVerdict struct {
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

// Decide applies the §19.3 rule to the LLM's verdict and returns the verdict
// the engine should act on. The input verdict is not mutated; a new value is
// returned. If a downgrade happened, the new Reasons slice contains the
// original reasons plus a single appended line explaining the downgrade. On the
// no-downgrade path, the function ensures Reasons is non-empty by appending a
// one-line annotation so the comparison audit row always has something to read.
//
// threshold is the resolved min_judge_confidence. A threshold <= 0 disables the
// guard (every record meets it), which matches the §19.3 fail-open fallback.
//
// hasPrevious is true when the project has a prior accepted artifact.
func Decide(verdict Verdict, records []evaluation.Record, threshold float64, hasPrevious bool) Verdict {
	if threshold <= 0 {
		return withReason(verdict, "§19.3 guard disabled by config (min_judge_confidence <= 0); LLM verdict honored")
	}
	// §19.3: new wins when at least one record is backed and exceeds the
	// confidence threshold. With no prior artifact, "better or equal" is
	// trivially satisfied, so a record that passed evaluation also backs
	// "new" (ADR-0043).
	strongNew := false
	for _, r := range records {
		backed := r.BetterThanPrevious
		if !hasPrevious {
			backed = r.BetterThanPrevious || r.PassedTests
		}
		if backed && r.Confidence > threshold {
			strongNew = true
			break
		}
	}
	if verdict.Winner != "new" {
		// The guard only blocks an unsupported "new"; it never promotes an
		// unsupported "previous" or "none".
		return withReason(verdict, "LLM verdict is not 'new'; §19.3 guard did not modify it")
	}
	if strongNew {
		return withReason(verdict, "§19.3 guard satisfied: at least one EvaluationRecord met better_than_previous + confidence > threshold; LLM verdict honored")
	}
	out := verdict
	if hasPrevious {
		out.Winner = "previous"
		out.Reasons = append(out.Reasons,
			fmt.Sprintf("downgraded from 'new' to 'previous': no EvaluationRecord met better_than_previous + confidence > %.2f", threshold))
	} else {
		out.Winner = "none"
		out.Reasons = append(out.Reasons,
			fmt.Sprintf("downgraded from 'new' to 'none': no prior accepted artifact and no passing EvaluationRecord met confidence > %.2f", threshold))
	}
	return out
}

// withReason returns a copy of verdict with reason appended to Reasons. A nil
// Reasons is treated as a non-nil empty slice, so the audit row is always
// readable.
func withReason(verdict Verdict, reason string) Verdict {
	out := verdict
	if out.Reasons == nil {
		out.Reasons = []string{}
	}
	out.Reasons = append(out.Reasons, reason)
	return out
}
