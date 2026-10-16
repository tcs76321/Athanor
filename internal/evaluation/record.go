// Package evaluation persists the per-candidate evaluation records
// produced by the §13.1 Phase 3 (Evaluating) and consumed by Phase 6
// (Comparing). The records are the durable substrate of the comparison
// system: the comparison phase (M3-T3) and the quality probes (M3-T7)
// read them instead of re-running evaluation.
//
// An EvaluationRecord captures the §19.2 output schema:
//
//	{
//	  "artifact_id": "uuid",
//	  "compared_against": "uuid or null",
//	  "score": 0.0,
//	  "passed_tests": true,
//	  "failed_tests": [],
//	  "missing_criteria": [],
//	  "security_issues": [],
//	  "style_issues": [],
//	  "better_than_previous": true,
//	  "confidence": 0.0,
//	  "summary": "string"
//	}
//
// Storage: one row per candidate per job (migration 0007). The
// list-valued fields (failed_tests, missing_criteria, security_issues,
// style_issues) are stored as JSON text per the §23.1-blessed
// short-string-list pattern; the Go side encodes on write and decodes
// on read. The repo never re-marshals user data — what you put in
// round-trips byte-for-byte.
//
// This package is a pure data layer: it imports only `store` and
// `ids`. It does not import `engine`, `job`, `artifact`, or `project`,
// so it cannot form an import cycle with them, and a unit test can
// exercise it without booting any other package.
package evaluation

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/ids"
)

// Record is one §19.2 evaluation record.
type Record struct {
	// ID is the application-generated UUID (§5).
	ID string
	// JobID is the job whose divergence produced the candidate.
	JobID string
	// ArtifactID is the candidate being judged.
	ArtifactID string
	// ComparedAgainst is the project's previous accepted artifact,
	// or empty when the project has no prior accepted artifact.
	ComparedAgainst string
	// Score is a unitless 0.0–1.0 quality score from the security
	// persona. §13.1 leaves the exact formula to T2; the record just
	// stores whatever the evaluator produced.
	Score float64
	// PassedTests is the evaluator's OVERALL pass flag for the candidate
	// (the evaluation verdict's `passed`), not specifically "the test command
	// exited 0" — the name is historical (known-issues L27). For a code
	// candidate the engine's deterministic verifier additionally records the
	// real test outcome, and a hard verifier failure overrides the verdict. A
	// missing test run does not by itself fail the record; the security
	// persona decides via the missing_criteria list.
	PassedTests bool
	// FailedTests is the list of named tests that failed (code
	// archetype; empty otherwise).
	FailedTests []string
	// MissingCriteria is the list of acceptance criteria the
	// evaluator found unmet. Drives the comparison gate.
	MissingCriteria []string
	// SecurityIssues is the list of security findings. Drives the
	// §19.3 "no_new_security_issue" rule.
	SecurityIssues []string
	// StyleIssues is the list of style/lint findings. Recorded for
	// the quality probe; the §19.3 rule does not gate on it.
	StyleIssues []string
	// BetterThanPrevious is the evaluator's binary judgment against
	// the prior accepted artifact (or true when no prior exists).
	BetterThanPrevious bool
	// Confidence is the evaluator's 0.0–1.0 confidence in its
	// judgment. §19.3 requires confidence > min_judge_confidence
	// (config) before a "new wins" verdict stands.
	Confidence float64
	// Summary is a one-sentence human-readable rationale.
	Summary string
	// CreatedAt is when the record was persisted. The engine stamps
	// this at write time so the comparison phase can order records
	// deterministically.
	CreatedAt time.Time
}

// encodeList marshals a string slice to JSON text, returning "[]" for
// nil so the column's NOT NULL DEFAULT '[]' invariant always holds.
func encodeList(s []string) string {
	if s == nil {
		return "[]"
	}
	b, err := json.Marshal(s)
	if err != nil {
		// json.Marshal on a []string cannot fail. The branch exists
		// for defense-in-depth and to keep the function total.
		return "[]"
	}
	return string(b)
}

// decodeList is the inverse of encodeList. A malformed payload is an
// error — the column is append-only and the contract is "what you put
// in round-trips byte-for-byte."
func decodeList(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("evaluation: decoding list column: %w", err)
	}
	return out, nil
}

// NewRecord stamps the immutable fields (ID, CreatedAt) on a Record
// built by the engine. The caller fills the scoring fields.
func NewRecord(jobID, artifactID string) Record {
	return Record{
		ID:         ids.New(),
		JobID:      jobID,
		ArtifactID: artifactID,
		CreatedAt:  time.Now().UTC(),
	}
}
