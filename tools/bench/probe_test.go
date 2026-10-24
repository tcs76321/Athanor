// M3-T7 scaffold tests: the result-type contract is
// the part of the probe that can be exercised without
// a running daemon or a real LLM. The three
// sub-measurements (calibration, stability, diversity)
// land in follow-up commits; this test pins the
// scaffold's shape so those commits are additive
// rather than redesigns.
package main

import (
	"encoding/json"
	"testing"
)

// TestDialeticalResult_RoundTripsJSON: the result
// type is the data contract between this probe and
// any future post-mortem notebook. A field rename is
// a breaking change; this test pins the JSON shape.
func TestDialeticalResult_RoundTripsJSON(t *testing.T) {
	r := dialecticalResult{
		Number:             1,
		Goal:               "Write a hello world in Python",
		JobID:              "job-abc",
		JobState:           "completed",
		ReportedConfidence: 0.85,
		ObservedScore:      0.78,
		Candidates: []candidate{
			{ArtifactID: "art-1", Text: "print('hi')", Length: 10},
		},
		StabilityVariance: 0.02,
		Notes:             "T-a: low calibration; T-b: stable; T-c: diverse",
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got dialecticalResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ReportedConfidence != 0.85 {
		t.Errorf("ReportedConfidence = %f, want 0.85", got.ReportedConfidence)
	}
	if got.ObservedScore != 0.78 {
		t.Errorf("ObservedScore = %f, want 0.78", got.ObservedScore)
	}
	if got.StabilityVariance != 0.02 {
		t.Errorf("StabilityVariance = %f, want 0.02", got.StabilityVariance)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].ArtifactID != "art-1" {
		t.Errorf("Candidates = %+v, want one with ArtifactID=art-1", got.Candidates)
	}
}

// TestDaemonsAddress_DefaultAndOverride pins the
// daemon-URL resolution contract: env override wins,
// default is the loopback port.
func TestDaemonsAddress_DefaultAndOverride(t *testing.T) {
	if got := daemonURL(); got != "http://127.0.0.1:7420" {
		t.Errorf("default daemonURL = %q, want loopback", got)
	}
	t.Setenv("ATHANOR_ADDR", "http://example.invalid:9999")
	if got := daemonURL(); got != "http://example.invalid:9999" {
		t.Errorf("env-overridden daemonURL = %q, want example.invalid", got)
	}
}
