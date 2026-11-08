package repair

import (
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/project"
)

func TestBudgetAllows(t *testing.T) {
	cases := []struct {
		name string
		b    Budget
		want bool
	}{
		{"enabled with room", Budget{Enabled: true, Max: 2, Used: 0}, true},
		{"enabled at limit", Budget{Enabled: true, Max: 2, Used: 2}, false},
		{"disabled", Budget{Enabled: false, Max: 2, Used: 0}, false},
		{"zero max", Budget{Enabled: true, Max: 0, Used: 0}, false},
		{"over limit", Budget{Enabled: true, Max: 1, Used: 2}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.b.Allows(); got != tc.want {
				t.Errorf("Allows() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPassed(t *testing.T) {
	if !Passed(evaluation.Record{PassedTests: true}) {
		t.Error("clean pass must report passed")
	}
	if Passed(evaluation.Record{PassedTests: false}) {
		t.Error("failed tests must not report passed")
	}
	if Passed(evaluation.Record{PassedTests: true, MissingCriteria: []string{"x"}}) {
		t.Error("missing criteria must not report passed")
	}
	if Passed(evaluation.Record{PassedTests: true, SecurityIssues: []string{"x"}}) {
		t.Error("security issues must not report passed")
	}
}

func TestBestCandidate(t *testing.T) {
	cands := []artifact.Artifact{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	records := []evaluation.Record{
		{ArtifactID: "a", Score: 0.4},
		{ArtifactID: "b", Score: 0.8},
		{ArtifactID: "b", Score: 0.9}, // later record wins for the same artifact
	}
	got, rec, ok := BestCandidate(cands, records)
	if !ok || got.ID != "b" || rec.Score != 0.9 {
		t.Errorf("BestCandidate = (%q, %.2f, %v), want (b, 0.90, true)", got.ID, rec.Score, ok)
	}
	if _, _, ok := BestCandidate(nil, records); ok {
		t.Error("no candidates must report not-ok")
	}
	if _, _, ok := BestCandidate(cands, nil); ok {
		t.Error("no records must report not-ok")
	}
	if _, _, ok := BestCandidate([]artifact.Artifact{{ID: "x"}}, records); ok {
		t.Error("an unevaluated candidate must not be chosen")
	}
}

func TestInstructions(t *testing.T) {
	rec := evaluation.Record{
		MissingCriteria: []string{"criterion one"},
		FailedTests:     []string{"test_foo failed"},
		SecurityIssues:  []string{"unsafe shell"},
		Summary:         "the candidate is incomplete",
	}
	got := Instructions(rec, project.CapabilityCode, project.LanguageGo)
	for _, want := range []string{"SELF-REFINE", "criterion one", "test_foo failed", "unsafe shell", "the candidate is incomplete", "Go source"} {
		if !strings.Contains(got, want) {
			t.Errorf("Instructions missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(Instructions(evaluation.Record{}, project.CapabilityText, ""), "source") {
		t.Error("a non-code instruction must not name a source language")
	}
}

func TestParseObservation(t *testing.T) {
	path, requested, err := ParseObservation(`{"read_file":"internal/foo.go"}`)
	if err != nil || !requested || path != "internal/foo.go" {
		t.Errorf("valid = (%q, %v, %v)", path, requested, err)
	}
	if _, requested, err := ParseObservation(`{"read_file":null}`); err != nil || requested {
		t.Errorf("null = (%v, %v), want (false, nil)", requested, err)
	}
	if _, requested, err := ParseObservation(`{"read_file":"   "}`); err != nil || requested {
		t.Errorf("blank = (%v, %v), want (false, nil)", requested, err)
	}
	if _, _, err := ParseObservation("no json"); err == nil {
		t.Error("malformed must return an error")
	}
}
