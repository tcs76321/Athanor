// M3-T3 commit 3.1 tests: buildComparisonInstructions must include the
// "Previous-record summary" section when the previous artifact has any
// evaluation history, and must omit it when the previous is absent or has no
// records.
//
// The test is pure (no DB): the previous-records slice is passed directly to
// the prompt builder, mirroring the call site in phaseCompare.
package engine

import (
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/evaluation"
)

// makeFinalArtifact returns an in-memory Artifact with a stable StoragePath so
// the prompt builder's read can be exercised. The read is best-effort; the test
// only asserts the prompt text, not the file content.
func makeFinalArtifact(id string) artifact.Artifact {
	return artifact.Artifact{ID: id, StoragePath: "", Kind: artifact.KindDocument, Status: artifact.StatusCandidate, Version: 1}
}

// TestBuildComparisonInstructions_OmitsPreviousSectionWhenEmpty is the "fresh
// project" case: no previous artifact, so the prompt has no Previous-record
// summary section.
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

// TestBuildComparisonInstructions_OmitsPreviousSectionWhenZeroRecords is the
// "previous artifact exists but has no history" case.
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

// TestBuildComparisonInstructions_IncludesPreviousSection is the happy path: a
// previous with 2 records, average score 0.75, average confidence 0.8.
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
	if !strings.Contains(got, "EvaluationRecords for the new artifact") {
		t.Errorf("new-artifact records section must still be present; got:\n%s", got)
	}
}
