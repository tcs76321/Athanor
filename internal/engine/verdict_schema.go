// M3-T7.5b (ADR-0012 §D6 upgrade): JSON schemas for the two structured
// judgment phases. ADR-0012 originally set `format: "json"`, which
// guarantees parseable JSON but not field types; the M3-T7 smoke run saw
// the 9B judge emit `"confidence": "0.95"` and `"style_issues": "..."`,
// hard-failing the job. Passing a schema instead asks Ollama to
// grammar-constrain the response to these types. The tolerant parser
// (M3-T7.5a) is the fallback for models or Ollama versions that ignore
// the schema.
package engine

import "github.com/tcs76321/athanor/internal/llm"

// evalVerdictSchema mirrors evalVerdict's §13.1 shape.
var evalVerdictSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"passed":               map[string]any{"type": "boolean"},
		"score":                map[string]any{"type": "number"},
		"failed_tests":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"missing_criteria":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"security_issues":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"style_issues":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"better_than_previous": map[string]any{"type": "boolean"},
		"confidence":           map[string]any{"type": "number"},
		"summary":              map[string]any{"type": "string"},
	},
	"required": []string{
		"passed", "score", "failed_tests", "missing_criteria", "security_issues",
		"style_issues", "better_than_previous", "confidence", "summary",
	},
}

// comparisonVerdictSchema mirrors comparisonVerdict's §13.1 shape.
var comparisonVerdictSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"winner":               map[string]any{"type": "string", "enum": []string{"new", "previous", "none"}},
		"confidence":           map[string]any{"type": "number"},
		"reasons":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"missing_requirements": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	},
	"required": []string{"winner", "confidence", "reasons", "missing_requirements"},
}

// verdictSchemaFor returns the JSON schema to send as Ollama's `format`
// for a phase, or nil for phases that produce prose.
func verdictSchemaFor(phase string) map[string]any {
	if !phaseProducesJSON(phase) {
		return nil
	}
	switch phase {
	case llm.PhaseEvaluating:
		return evalVerdictSchema
	case llm.PhaseComparing:
		return comparisonVerdictSchema
	default:
		return nil
	}
}
