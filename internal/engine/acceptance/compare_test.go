package acceptance

import (
	"fmt"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/verify"
)

// TestParseComparisonVerdict_TrimsWhitespace covers the M3-T3 commit 3.3 fix:
// a winner with leading/trailing whitespace is honored after TrimSpace.
func TestParseComparisonVerdict_TrimsWhitespace(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		wantWinner string
	}{
		{"trailing newline", `{"winner":"new\n","confidence":0.9,"reasons":[],"missing_requirements":[]}`, "new"},
		{"leading spaces", `{"winner":"   new","confidence":0.9,"reasons":[],"missing_requirements":[]}`, "new"},
		{"both", `{"winner":"\tnew\n","confidence":0.9,"reasons":[],"missing_requirements":[]}`, "new"},
		{"whitespace around previous", `{"winner":"  previous\t","confidence":0.9,"reasons":[],"missing_requirements":[]}`, "previous"},
		{"whitespace around none", `{"winner":" none\n","confidence":0.9,"reasons":[],"missing_requirements":[]}`, "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := ParseComparisonVerdict(tc.input)
			if err != nil {
				t.Fatalf("ParseComparisonVerdict: %v", err)
			}
			if got.Winner != tc.wantWinner {
				t.Errorf("Winner = %q, want %q", got.Winner, tc.wantWinner)
			}
		})
	}
}

// TestParseComparisonVerdict_UnknownWinnerReturnsTypedError pins the M3-T3
// contract: an unknown winner returns an error that wraps ErrUnknownWinner.
func TestParseComparisonVerdict_UnknownWinnerReturnsTypedError(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty string", `{"winner":"","confidence":0.9,"reasons":[],"missing_requirements":[]}`},
		{"typo", `{"winner":"newish","confidence":0.9,"reasons":[],"missing_requirements":[]}`},
		{"uppercase", `{"winner":"NEW","confidence":0.9,"reasons":[],"missing_requirements":[]}`},
		{"random word", `{"winner":"maybe","confidence":0.9,"reasons":[],"missing_requirements":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ParseComparisonVerdict(tc.input)
			if err == nil {
				t.Fatalf("ParseComparisonVerdict returned nil error for unknown winner; want typed error")
			}
			if !IsUnknownWinner(err) {
				t.Errorf("error = %v, want errors.Is(err, ErrUnknownWinner)", err)
			}
		})
	}
}

// TestIsUnknownWinner_TrueOnlyForTheSentinel pins the errors.Is contract.
func TestIsUnknownWinner_TrueOnlyForTheSentinel(t *testing.T) {
	if !IsUnknownWinner(fmt.Errorf("wrapped: %w", ErrUnknownWinner)) {
		t.Error("wrapped ErrUnknownWinner should be detected")
	}
	if IsUnknownWinner(nil) {
		t.Error("nil should not be detected as ErrUnknownWinner")
	}
	if IsUnknownWinner(fmt.Errorf("plain error")) {
		t.Error("plain error should not be detected as ErrUnknownWinner")
	}
}

// TestFinalRecordBacksDeterministicImprovement pins the first-use fix: when a
// hard verifier passes and its score is at least the previous accepted
// artifact's known average score, the final record backs "new".
func TestFinalRecordBacksDeterministicImprovement(t *testing.T) {
	ver := verify.Result{Applied: 2, Passed: true, HardApplied: 1, HardPassed: true, Score: 1}
	rec := FinalRecord(job.Job{ID: "j1"}, artifact.Artifact{ID: "a1"}, "prev", 0.9, ver)

	if !rec.BetterThanPrevious {
		t.Error("hard pass at or above the previous score must set BetterThanPrevious")
	}
	if rec.Confidence != 1 {
		t.Errorf("confidence = %v, want 1 for a hard verifier", rec.Confidence)
	}
	if !rec.PassedTests {
		t.Error("deterministic pass must set PassedTests")
	}

	got := Decide(Verdict{Winner: "new", Confidence: 0.8}, []evaluation.Record{rec}, 0.7, true)
	if got.Winner != "new" {
		t.Errorf("Decide = %q, want new when the verifier backs better-or-equal; reasons=%v", got.Winner, got.Reasons)
	}
}

// TestFinalRecordNoHistoryDoesNotBackNew keeps the conservative contract: a
// previous accepted artifact with no evaluation history has no known score, so
// the guard still downgrades an unsupported "new".
func TestFinalRecordNoHistoryDoesNotBackNew(t *testing.T) {
	ver := verify.Result{Applied: 1, Passed: true, HardApplied: 1, HardPassed: true, Score: 1}
	rec := FinalRecord(job.Job{ID: "j1"}, artifact.Artifact{ID: "a1"}, "prev", 0, ver)

	if rec.BetterThanPrevious {
		t.Error("no previous score must not set BetterThanPrevious")
	}
	if got := Decide(Verdict{Winner: "new", Confidence: 0.99}, []evaluation.Record{rec}, 0.7, true); got.Winner != "previous" {
		t.Errorf("Decide = %q, want previous without deterministic backing", got.Winner)
	}
}

// TestFinalRecordDeterministicNoPrevious keeps the no-previous path unchanged:
// there is nothing to be better than, so BetterThanPrevious stays false and the
// hard verifier supplies confidence 1.
func TestFinalRecordDeterministicNoPrevious(t *testing.T) {
	ver := verify.Result{Applied: 1, Passed: true, HardApplied: 1, HardPassed: true, Score: 1}
	rec := FinalRecord(job.Job{ID: "j1"}, artifact.Artifact{ID: "a1"}, "", 0, ver)

	if rec.BetterThanPrevious {
		t.Error("no-previous deterministic path must not set BetterThanPrevious")
	}
	if rec.Confidence != 1 {
		t.Errorf("confidence = %v, want 1 for a hard verifier", rec.Confidence)
	}
}
