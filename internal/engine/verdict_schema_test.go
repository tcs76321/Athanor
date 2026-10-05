package engine

import (
	"testing"

	"github.com/tcs76321/athanor/internal/llm"
)

// TestVerdictSchemaFor pins M3-T7.5b: the judgment phases get a JSON
// schema, the prose phases get nil, and the eval schema names the fields
// the evaluator depends on.
func TestVerdictSchemaFor(t *testing.T) {
	for _, phase := range []string{llm.PhaseEvaluating, llm.PhaseComparing} {
		if verdictSchemaFor(phase) == nil {
			t.Errorf("verdictSchemaFor(%q) = nil, want a schema", phase)
		}
	}
	for _, phase := range []string{
		llm.PhasePlanning, llm.PhaseDiverging, llm.PhaseReflecting,
		llm.PhaseSynthesizing, "context_building",
	} {
		if s := verdictSchemaFor(phase); s != nil {
			t.Errorf("verdictSchemaFor(%q) = %v, want nil", phase, s)
		}
	}

	eval := verdictSchemaFor(llm.PhaseEvaluating)
	props, ok := eval["properties"].(map[string]any)
	if !ok {
		t.Fatalf("eval schema has no properties map: %v", eval)
	}
	for _, field := range []string{"passed", "score", "confidence", "summary", "style_issues"} {
		if _, ok := props[field]; !ok {
			t.Errorf("eval schema is missing property %q", field)
		}
	}
	if req, _ := eval["required"].([]string); len(req) == 0 {
		t.Error("eval schema has no required fields")
	}

	// The comparison schema must constrain the winner to the closed set.
	cmp := verdictSchemaFor(llm.PhaseComparing)
	cprops, _ := cmp["properties"].(map[string]any)
	winner, _ := cprops["winner"].(map[string]any)
	enum, _ := winner["enum"].([]string)
	if len(enum) != 3 {
		t.Errorf("comparison winner enum = %v, want 3 values", enum)
	}
}
