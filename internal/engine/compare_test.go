// M3-T3 commit 3.1 tests: buildComparisonInstructions must
// include the "Previous-record summary" section when the
// previous artifact has any evaluation history, and must
// omit it when the previous is absent or has no records.
//
// The test is pure (no DB): the previous-records slice is
// passed directly to the prompt builder, mirroring the
// call site in phaseCompare.
package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/evaluation"
)

// makeFinalArtifact returns an in-memory Artifact with a
// stable StoragePath so the prompt builder's read can be
// exercised. The read is best-effort; the test only asserts
// the prompt text, not the file content.
func makeFinalArtifact(id string) artifact.Artifact {
	return artifact.Artifact{ID: id, StoragePath: "", Kind: artifact.KindDocument, Status: artifact.StatusCandidate, Version: 1}
}

// TestBuildComparisonInstructions_OmitsPreviousSectionWhenEmpty
// is the "fresh project" case: no previous artifact, so the
// prompt has no Previous-record summary section.
func TestBuildComparisonInstructions_OmitsPreviousSectionWhenEmpty(t *testing.T) {
	final := makeFinalArtifact("new-1")
	records := []evaluation.Record{
		{ArtifactID: "cand-1", PassedTests: true, BetterThanPrevious: true, Confidence: 0.9, Summary: "good"},
	}
	got := buildComparisonInstructions(final, records, "", nil, 0, 0)
	if strings.Contains(got, "Previous-record summary") {
		t.Errorf("prompt should NOT contain 'Previous-record summary' when previousID is empty; got:\n%s", got)
	}
	if !strings.Contains(got, "EvaluationRecords for the new artifact") {
		t.Errorf("prompt must still contain the new-artifact records section")
	}
}

// TestBuildComparisonInstructions_OmitsPreviousSectionWhenZeroRecords
// is the "previous artifact exists but has no history" case:
// the previous_id is non-empty, but the previous's own
// records are empty. The §19.3 guard still has the previous
// id (so the LLM knows which artifact it's comparing
// against), but the section is omitted because the count
// is zero.
func TestBuildComparisonInstructions_OmitsPreviousSectionWhenZeroRecords(t *testing.T) {
	final := makeFinalArtifact("new-1")
	records := []evaluation.Record{{ArtifactID: "cand-1", PassedTests: true, Confidence: 0.9}}
	got := buildComparisonInstructions(final, records, "prev-1", nil, 0, 0)
	if strings.Contains(got, "Previous-record summary") {
		t.Errorf("prompt should NOT contain the section when previousRecords is empty; got:\n%s", got)
	}
	if !strings.Contains(got, "previous artifact_id: prev-1") {
		t.Errorf("prompt should still include previous_id; got:\n%s", got)
	}
}

// TestBuildComparisonInstructions_IncludesPreviousSection is
// the happy path: a previous with 2 records, average score
// 0.75, average confidence 0.8. The section is rendered with
// the count, the averages, and each record.
func TestBuildComparisonInstructions_IncludesPreviousSection(t *testing.T) {
	final := makeFinalArtifact("new-1")
	records := []evaluation.Record{
		{ArtifactID: "cand-1", PassedTests: true, BetterThanPrevious: true, Confidence: 0.9, Summary: "good"},
	}
	previousRecords := []evaluation.Record{
		{ArtifactID: "prev-1", PassedTests: true, BetterThanPrevious: false, Confidence: 0.7, Summary: "ok", Score: 0.6},
		{ArtifactID: "prev-1", PassedTests: true, BetterThanPrevious: true, Confidence: 0.9, Summary: "better", Score: 0.9},
	}
	got := buildComparisonInstructions(final, records, "prev-1", previousRecords, 0.75, 0.8)

	if !strings.Contains(got, "Previous-record summary") {
		t.Fatalf("prompt must contain the section header; got:\n%s", got)
	}
	if !strings.Contains(got, "count: 2") {
		t.Errorf("section must include count: 2; got:\n%s", got)
	}
	if !strings.Contains(got, "avg_score: 0.75") {
		t.Errorf("section must include avg_score: 0.75; got:\n%s", got)
	}
	if !strings.Contains(got, "avg_confidence: 0.80") {
		t.Errorf("section must include avg_confidence: 0.80; got:\n%s", got)
	}
	if !strings.Contains(got, "ok") || !strings.Contains(got, "better") {
		t.Errorf("section must include the per-record summaries; got:\n%s", got)
	}
	// The new-artifact records must still be present
	// (the section is additive, not a replacement).
	if !strings.Contains(got, "EvaluationRecords for the new artifact") {
		t.Errorf("new-artifact records section must still be present; got:\n%s", got)
	}
}

// TestParseComparisonVerdict_TrimsWhitespace covers the
// M3-T3 commit 3.3 fix: a winner with leading/trailing
// whitespace is honored after TrimSpace.
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
			got, _, err := parseComparisonVerdict(tc.input)
			if err != nil {
				t.Fatalf("parseComparisonVerdict: %v", err)
			}
			if got.Winner != tc.wantWinner {
				t.Errorf("Winner = %q, want %q", got.Winner, tc.wantWinner)
			}
		})
	}
}

// TestParseComparisonVerdict_UnknownWinnerReturnsTypedError
// covers the M3-T3 commit 3.3 behavior: an unknown winner
// returns an error that wraps errUnknownWinner. The engine's
// phaseCompare catches this, audits the downgrade, and
// proceeds with winner="none".
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
			_, _, err := parseComparisonVerdict(tc.input)
			if err == nil {
				t.Fatalf("parseComparisonVerdict returned nil error for unknown winner; want typed error")
			}
			if !isUnknownWinnerErr(err) {
				t.Errorf("error = %v, want errors.Is(err, errUnknownWinner)", err)
			}
		})
	}
}

// TestIsUnknownWinnerErr_TrueOnlyForTheSentinel pins the
// errors.Is contract: the helper returns true iff the
// error chain contains errUnknownWinner.
func TestIsUnknownWinnerErr_TrueOnlyForTheSentinel(t *testing.T) {
	if !isUnknownWinnerErr(fmt.Errorf("wrapped: %w", errUnknownWinner)) {
		t.Error("wrapped errUnknownWinner should be detected")
	}
	if isUnknownWinnerErr(nil) {
		t.Error("nil should not be detected as errUnknownWinner")
	}
	if isUnknownWinnerErr(fmt.Errorf("plain error")) {
		t.Error("plain error should not be detected as errUnknownWinner")
	}
}
