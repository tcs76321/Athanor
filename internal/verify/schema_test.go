package verify

import (
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/project"
)

const sampleSchema = `{
  "type": "object",
  "required": ["id", "severity", "ts"],
  "additionalProperties": false,
  "properties": {
    "id": {"type": "string"},
    "severity": {"enum": ["low", "high"]},
    "ts": {"type": "string"}
  }
}`

func containsReason(reasons []string, sub string) bool {
	for _, r := range reasons {
		if strings.Contains(r, sub) {
			return true
		}
	}
	return false
}

func TestDataVerifierValidates(t *testing.T) {
	r := Default()

	good := `{"id":"a","severity":"high","ts":"2026-01-01T00:00:00Z"}`
	if res := r.Run(Input{Archetype: project.ArchetypeData, Content: good, DataSchema: sampleSchema}); !res.HardDecisive() || !res.HardPassed {
		t.Fatalf("valid data not accepted: %+v", res)
	}

	missing := `{"id":"a","severity":"high"}`
	res := r.Run(Input{Archetype: project.ArchetypeData, Content: missing, DataSchema: sampleSchema})
	if !res.HardDecisive() || res.HardPassed {
		t.Fatalf("missing-field data accepted: %+v", res)
	}
	if !containsReason(res.Reasons, "missing required property") {
		t.Errorf("reasons = %v", res.Reasons)
	}

	badEnum := `{"id":"a","severity":"medium","ts":"x"}`
	if res := r.Run(Input{Archetype: project.ArchetypeData, Content: badEnum, DataSchema: sampleSchema}); res.HardPassed {
		t.Errorf("enum violation accepted: %+v", res)
	}

	extra := `{"id":"a","severity":"high","ts":"x","oops":1}`
	if res := r.Run(Input{Archetype: project.ArchetypeData, Content: extra, DataSchema: sampleSchema}); res.HardPassed {
		t.Errorf("additional property accepted: %+v", res)
	}

	notJSON := `id: a, severity: high`
	if res := r.Run(Input{Archetype: project.ArchetypeData, Content: notJSON, DataSchema: sampleSchema}); !res.HardDecisive() || res.HardPassed {
		t.Errorf("non-JSON data accepted: %+v", res)
	}
}

// TestDataSchemaExtractsFencedAndProse pins the ADR-0070 amendment: the engine
// accepts the same single-JSON-value wrapper the benchmark's json_extract does.
func TestDataSchemaExtractsFencedAndProse(t *testing.T) {
	good := `{"id":"a","severity":"high","ts":"2026-01-01T00:00:00Z"}`
	fenced := "Here is the normalized record:\n```json\n" + good + "\n```\n"
	if res := Default().Run(Input{Archetype: project.ArchetypeData, Content: fenced, DataSchema: sampleSchema}); !res.HardPassed {
		t.Errorf("fenced JSON rejected: %+v", res)
	}
	prose := "Result: " + good + "\nDone."
	if res := Default().Run(Input{Archetype: project.ArchetypeData, Content: prose, DataSchema: sampleSchema}); !res.HardPassed {
		t.Errorf("prose-wrapped JSON rejected: %+v", res)
	}
}

func TestDataVerifierUnappliedWithoutSchema(t *testing.T) {
	res := Default().Run(Input{Archetype: project.ArchetypeData, Content: `{"id":"a"}`})
	if res.HardDecisive() {
		t.Errorf("data verifier applied without a schema: %+v", res)
	}
}

func TestDataVerifierAdditionalPropertiesWithoutProperties(t *testing.T) {
	const closed = `{"type":"object","additionalProperties":false}`
	if res := Default().Run(Input{Archetype: project.ArchetypeData, Content: `{"a":1}`, DataSchema: closed}); res.HardPassed {
		t.Errorf("key allowed under additionalProperties:false with no properties declared: %+v", res)
	}
	if res := Default().Run(Input{Archetype: project.ArchetypeData, Content: `{}`, DataSchema: closed}); !res.HardPassed {
		t.Errorf("empty object rejected under a closed schema: %+v", res)
	}
}
