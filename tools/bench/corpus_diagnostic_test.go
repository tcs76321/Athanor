package main

import "testing"

// TestDiagnosticCorpusParses keeps the non-frozen cognitive-loop diagnostic
// loadable by the ordinary probe runner. It is deliberately separate from the
// frozen v2 digest tests.
func TestDiagnosticCorpusParses(t *testing.T) {
	goals, err := loadCorpus("../../eval/diagnostic/tasks.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(goals) != 3 {
		t.Fatalf("goals = %d, want 3", len(goals))
	}
	seen := map[string]bool{}
	for _, goal := range goals {
		seen[goal.Name] = true
	}
	for _, want := range []string{"diag-layered-code", "diag-structured-brief", "diag-contradiction-brief"} {
		if !seen[want] {
			t.Errorf("missing diagnostic task %q", want)
		}
	}
}
