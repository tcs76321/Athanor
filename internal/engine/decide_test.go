// M3-T2 commit 2.5: table-driven tests for DecideWinner.
//
// DecideWinner is a pure function, so the test is a single
// table with the LLM verdict, the record set, the threshold,
// and the hasPrevious flag as inputs, and the expected final
// winner + the expected downgrade-reason substring as
// outputs. Every row corresponds to a row in ADR-0014 §3.4.
//
// The package doc on decide.go states the §19.3 rule:
//   - LLM says "new", no record meets threshold, no previous → "none"
//   - LLM says "new", no record meets threshold, previous exists → "previous"
//   - LLM says "new", a record meets threshold → "new" (regardless of previous)
//   - LLM says "previous" or "none" → never upgraded by the guard
//   - boundary: confidence == threshold (not strictly greater) → does NOT meet
package engine

import (
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/evaluation"
)

// verdictFor builds a comparisonVerdict with a fixed winner
// and a single starter reason. Keeps the test table compact.
func verdictFor(winner string, reasons ...string) comparisonVerdict {
	return comparisonVerdict{Winner: winner, Reasons: reasons, Confidence: 0.9}
}

// recordFor builds an evaluation.Record with the given
// better-than-previous and confidence fields. The other
// fields are zero-valued, which is fine for DecideWinner
// (it reads only BetterThanPrevious and Confidence).
func recordFor(better bool, confidence float64) evaluation.Record {
	return evaluation.Record{BetterThanPrevious: better, Confidence: confidence}
}

func TestDecideWinner(t *testing.T) {
	cases := []struct {
		name        string
		verdict     comparisonVerdict
		records     []evaluation.Record
		threshold   float64
		hasPrevious bool
		// wantWinner is the expected post-DecideWinner.Winner.
		wantWinner string
		// wantReasonContains, when non-empty, is a substring
		// the appended downgrade reason must contain.
		wantReasonContains string
	}{
		// 1. LLM says "new", no record meets threshold,
		//    no previous → "none".
		{
			name:               "new_no_record_no_previous",
			verdict:            verdictFor("new"),
			records:            []evaluation.Record{recordFor(true, 0.5)}, // below threshold
			threshold:          0.7,
			hasPrevious:        false,
			wantWinner:         "none",
			wantReasonContains: "no prior accepted artifact",
		},
		// 2. LLM says "new", no record meets threshold,
		//    previous exists → "previous".
		{
			name:               "new_no_record_with_previous",
			verdict:            verdictFor("new"),
			records:            []evaluation.Record{recordFor(false, 0.9)}, // not better
			threshold:          0.7,
			hasPrevious:        true,
			wantWinner:         "previous",
			wantReasonContains: "no EvaluationRecord met",
		},
		// 3. LLM says "new", a record meets threshold →
		//    "new" stays, regardless of previous.
		{
			name:               "new_record_meets_with_previous",
			verdict:            verdictFor("new"),
			records:            []evaluation.Record{recordFor(true, 0.9)},
			threshold:          0.7,
			hasPrevious:        true,
			wantWinner:         "new",
			wantReasonContains: "",
		},
		// 3b. LLM says "new", a record meets threshold, no
		//     previous → "new" stays.
		{
			name:               "new_record_meets_no_previous",
			verdict:            verdictFor("new"),
			records:            []evaluation.Record{recordFor(true, 0.9)},
			threshold:          0.7,
			hasPrevious:        false,
			wantWinner:         "new",
			wantReasonContains: "",
		},
		// 4. LLM says "previous", no record meets threshold →
		//    guard does NOT flip; verdict stays "previous".
		{
			name:               "previous_no_record_with_previous",
			verdict:            verdictFor("previous"),
			records:            []evaluation.Record{recordFor(false, 0.9)},
			threshold:          0.7,
			hasPrevious:        true,
			wantWinner:         "previous",
			wantReasonContains: "",
		},
		// 5. LLM says "none", no record meets threshold →
		//    guard does NOT flip; verdict stays "none".
		{
			name:               "none_no_record_no_previous",
			verdict:            verdictFor("none"),
			records:            []evaluation.Record{recordFor(false, 0.9)},
			threshold:          0.7,
			hasPrevious:        false,
			wantWinner:         "none",
			wantReasonContains: "",
		},
		// 6. LLM says "previous" but a record DOES meet
		//    threshold → guard does NOT promote; verdict
		//    stays "previous" (the §19.3 rule is a floor
		//    on acceptance, not a recommendation engine).
		{
			name:               "previous_record_meets_stays_previous",
			verdict:            verdictFor("previous"),
			records:            []evaluation.Record{recordFor(true, 0.9)},
			threshold:          0.7,
			hasPrevious:        true,
			wantWinner:         "previous",
			wantReasonContains: "",
		},
		// 6b. LLM says "none" but a record meets threshold →
		//     verdict stays "none".
		{
			name:               "none_record_meets_stays_none",
			verdict:            verdictFor("none"),
			records:            []evaluation.Record{recordFor(true, 0.9)},
			threshold:          0.7,
			hasPrevious:        false,
			wantWinner:         "none",
			wantReasonContains: "",
		},
		// 7. Ties: multiple records, only one meets
		//    threshold → "new" stays.
		{
			name:    "ties_one_meets",
			verdict: verdictFor("new"),
			records: []evaluation.Record{
				recordFor(false, 0.5),
				recordFor(true, 0.9), // meets
				recordFor(true, 0.5), // below threshold
			},
			threshold:          0.7,
			hasPrevious:        true,
			wantWinner:         "new",
			wantReasonContains: "",
		},
		// 8. Boundary: confidence == threshold (not strictly
		//    greater) → does NOT meet the bar → downgrade.
		{
			name:               "boundary_equals_threshold_downgrades",
			verdict:            verdictFor("new"),
			records:            []evaluation.Record{recordFor(true, 0.7)}, // == threshold
			threshold:          0.7,
			hasPrevious:        true,
			wantWinner:         "previous",
			wantReasonContains: "no EvaluationRecord met",
		},
		// 9. Empty records slice + LLM "new" → downgrade.
		{
			name:               "empty_records",
			verdict:            verdictFor("new"),
			records:            nil,
			threshold:          0.7,
			hasPrevious:        false,
			wantWinner:         "none",
			wantReasonContains: "no prior accepted artifact",
		},
		// 10. Disabled guard (threshold <= 0): every record
		//     meets the bar (effectively). LLM "new" stays.
		{
			name:               "disabled_threshold_keeps_new",
			verdict:            verdictFor("new"),
			records:            []evaluation.Record{recordFor(false, 0.0)},
			threshold:          0,
			hasPrevious:        true,
			wantWinner:         "new",
			wantReasonContains: "",
		},
		// 11. Pre-existing reasons are preserved through the
		//     downgrade (the downgrade appends, does not
		//     replace).
		{
			name:               "downgrade_preserves_existing_reasons",
			verdict:            verdictFor("new", "LLM rationale 1", "LLM rationale 2"),
			records:            []evaluation.Record{},
			threshold:          0.7,
			hasPrevious:        true,
			wantWinner:         "previous",
			wantReasonContains: "downgraded from 'new' to 'previous'",
		},
		// 12. Upper boundary: confidence just above threshold
		//     (threshold + 0.0001) → "new" stays. The strict-
		//     greater check means the record is accepted as
		//     strongNew, even though the margin is tiny.
		{
			name:               "boundary_just_above_threshold_stays_new",
			verdict:            verdictFor("new"),
			records:            []evaluation.Record{recordFor(true, 0.7001)}, // threshold + 0.0001
			threshold:          0.7,
			hasPrevious:        true,
			wantWinner:         "new",
			wantReasonContains: "",
		},
		// 13. Ties: multiple records all meet the threshold
		//     → still "new" (the first one in the slice
		//     triggers the short-circuit). Sanity check on
		//     the for-loop's break-on-first-match behavior.
		{
			name:    "ties_multiple_meet",
			verdict: verdictFor("new"),
			records: []evaluation.Record{
				recordFor(true, 0.9),
				recordFor(true, 0.95),
				recordFor(true, 0.8),
			},
			threshold:          0.7,
			hasPrevious:        true,
			wantWinner:         "new",
			wantReasonContains: "",
		},
		// 14. The guard does not promote "previous" → "new"
		//     even when a record would back it. Verifies the
		//     direction: the LLM's "previous" verdict stays
		//     "previous" regardless of records. (This is
		//     already covered by row 6 in spirit; the
		//     separate row makes the contract explicit.)
		{
			name:    "previous_verdict_with_strong_record_stays_previous",
			verdict: verdictFor("previous"),
			records: []evaluation.Record{
				recordFor(true, 0.95), // would back "new" if asked
			},
			threshold:          0.7,
			hasPrevious:        true,
			wantWinner:         "previous",
			wantReasonContains: "",
		},
		// 15. The guard does not promote "none" → "new" even
		//     when a record would back it. The §19.3 rule is a
		//     floor on acceptance, not a recommendation
		//     engine.
		{
			name:    "none_verdict_with_strong_record_stays_none",
			verdict: verdictFor("none"),
			records: []evaluation.Record{
				recordFor(true, 0.95),
			},
			threshold:          0.7,
			hasPrevious:        true,
			wantWinner:         "none",
			wantReasonContains: "",
		},
		// 16. No previous, LLM "previous" → stays "previous"
		//     (the guard does nothing; the §19.3 rule is not
		//     sensitive to whether a previous exists when the
		//     LLM said "previous" or "none").
		{
			name:               "no_previous_previous_verdict_stays_previous",
			verdict:            verdictFor("previous"),
			records:            nil,
			threshold:          0.7,
			hasPrevious:        false,
			wantWinner:         "previous",
			wantReasonContains: "",
		},
		// 17. No previous, LLM "none" → stays "none".
		{
			name:               "no_previous_none_verdict_stays_none",
			verdict:            verdictFor("none"),
			records:            nil,
			threshold:          0.7,
			hasPrevious:        false,
			wantWinner:         "none",
			wantReasonContains: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecideWinner(tc.verdict, tc.records, tc.threshold, tc.hasPrevious)
			if got.Winner != tc.wantWinner {
				t.Errorf("Winner = %q, want %q (reasons = %v)",
					got.Winner, tc.wantWinner, got.Reasons)
			}
			if tc.wantReasonContains != "" {
				found := false
				for _, r := range got.Reasons {
					if strings.Contains(r, tc.wantReasonContains) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Reasons = %v, want one to contain %q",
						got.Reasons, tc.wantReasonContains)
				}
			}
			// Reasons from the input verdict are preserved
			// through a downgrade (case 11). Sanity-check
			// the contract explicitly here too.
			if tc.wantReasonContains != "" && len(tc.verdict.Reasons) > 0 {
				for _, r := range tc.verdict.Reasons {
					found := false
					for _, out := range got.Reasons {
						if out == r {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("Reasons lost a pre-existing reason %q (got %v)",
							r, got.Reasons)
					}
				}
			}
		})
	}
}

// TestDecideWinner_ReasonsAlwaysNonEmpty covers the
// post-mortem-readability contract: regardless of which
// branch DecideWinner takes (disabled, no-record, or
// satisfied), the returned verdict's `Reasons` slice is
// non-nil and has at least one entry. The audit row the
// engine writes (`comparison` event with the
// post-DecideWinner verdict) is then guaranteed to have
// something to read in a post-mortem, even when the LLM
// returned an empty `reasons` array.
func TestDecideWinner_ReasonsAlwaysNonEmpty(t *testing.T) {
	cases := []struct {
		name        string
		verdict     comparisonVerdict
		records     []evaluation.Record
		threshold   float64
		hasPrevious bool
	}{
		{
			name:        "disabled_threshold",
			verdict:     verdictFor("new"),
			threshold:   0,
			hasPrevious: true,
		},
		{
			name:        "verdict_not_new",
			verdict:     verdictFor("previous"),
			threshold:   0.7,
			hasPrevious: true,
		},
		{
			name:    "satisfied_guard",
			verdict: verdictFor("new"),
			records: []evaluation.Record{
				recordFor(true, 0.9),
			},
			threshold:   0.7,
			hasPrevious: true,
		},
		{
			name:        "downgraded_to_previous",
			verdict:     verdictFor("new"),
			records:     []evaluation.Record{recordFor(false, 0.5)},
			threshold:   0.7,
			hasPrevious: true,
		},
		{
			name:        "downgraded_to_none",
			verdict:     verdictFor("new"),
			records:     []evaluation.Record{recordFor(true, 0.5)},
			threshold:   0.7,
			hasPrevious: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecideWinner(tc.verdict, tc.records, tc.threshold, tc.hasPrevious)
			if got.Reasons == nil {
				t.Errorf("Reasons is nil; want non-nil with at least one entry")
			}
			if len(got.Reasons) == 0 {
				t.Errorf("Reasons is empty; want at least one entry (post-mortem readability)")
			}
		})
	}
}
