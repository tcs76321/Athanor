package verify

import (
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/project"
)

const dataArray = `[
  {"id":1,"created_at":"2026-01-01T00:00:00Z"},
  {"id":2,"created_at":"2026-01-02T00:00:00Z"}
]`

func dataChecks() *project.ExecutionChecks {
	return &project.ExecutionChecks{
		ArrayLength:   2,
		ArrayUniqueBy: "id",
		ArraySortedBy: "id",
		JSONFieldMatches: map[string]string{
			"created_at": `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`,
		},
	}
}

func TestDataChecksPassthroughAndFailures(t *testing.T) {
	r := Default()
	res := r.Run(Input{Archetype: project.ArchetypeData, Content: dataArray, Checks: dataChecks()})
	if !res.HardDecisive() || !res.HardPassed {
		t.Fatalf("valid array rejected: %+v", res)
	}

	cases := []struct {
		name    string
		content string
		reason  string
	}{
		{"wrong length", `[{"id":1,"created_at":"2026-01-01T00:00:00Z"}]`, "array_length"},
		{"duplicate key", `[{"id":1,"created_at":"2026-01-01T00:00:00Z"},{"id":1,"created_at":"2026-01-02T00:00:00Z"}]`, "array_unique_by"},
		{"unsorted", `[{"id":2,"created_at":"2026-01-02T00:00:00Z"},{"id":1,"created_at":"2026-01-01T00:00:00Z"}]`, "array_sorted_by"},
		{"field mismatch", `[{"id":1,"created_at":"yesterday"},{"id":2,"created_at":"2026-01-02T00:00:00Z"}]`, "json_field_matches"},
		{"not an array", `{"id":1}`, "array_length"},
		{"not json", `nope`, "not valid JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Run(Input{Archetype: project.ArchetypeData, Content: tc.content, Checks: dataChecks()})
			if !got.HardDecisive() || got.HardPassed {
				t.Fatalf("expected a hard failure: %+v", got)
			}
			if !containsReason(got.Reasons, tc.reason) {
				t.Errorf("reasons = %v, want one containing %q", got.Reasons, tc.reason)
			}
		})
	}
}

func TestDataChecksExtractFencedJSON(t *testing.T) {
	content := "Here is the result:\n```json\n" + dataArray + "\n```\n"
	res := Default().Run(Input{Archetype: project.ArchetypeData, Content: content, Checks: dataChecks()})
	if !res.HardPassed {
		t.Fatalf("fenced array rejected: %+v", res)
	}
}

func TestDataChecksAndSchemaBothApply(t *testing.T) {
	schema := `{"type":"array","items":{"type":"object","required":["id","created_at"]}}`
	res := Default().Run(Input{
		Archetype: project.ArchetypeData, Content: dataArray,
		DataSchema: schema, Checks: dataChecks(),
	})
	if !res.HardDecisive() || !res.HardPassed {
		t.Fatalf("schema+checks rejected a valid array: %+v", res)
	}
	if len(res.Verifiers) != 2 {
		t.Errorf("verifiers = %v, want json-schema and data-checks", res.Verifiers)
	}
}

func TestDataChecksUnappliedWithoutChecks(t *testing.T) {
	res := Default().Run(Input{Archetype: project.ArchetypeData, Content: dataArray})
	for _, name := range res.Verifiers {
		if name == "data-checks" {
			t.Fatalf("data-checks applied without a declared check: %+v", res)
		}
	}
	if res.HardDecisive() {
		t.Errorf("hard verifier applied without a schema or checks: %+v", res)
	}
}

func TestDataChecksInvalidPatternFailsClosed(t *testing.T) {
	checks := &project.ExecutionChecks{JSONFieldMatches: map[string]string{"id": "["}}
	res := Default().Run(Input{Archetype: project.ArchetypeData, Content: dataArray, Checks: checks})
	if !res.HardDecisive() || res.HardPassed {
		t.Fatalf("invalid regex accepted: %+v", res)
	}
	if !containsReason(res.Reasons, "invalid pattern") {
		t.Errorf("reasons = %v", res.Reasons)
	}
}

func TestDataChecksReasonIsBounded(t *testing.T) {
	// A huge mismatch must not echo the entire document into the prompt.
	checks := &project.ExecutionChecks{ArrayLength: 999999}
	res := Default().Run(Input{Archetype: project.ArchetypeData, Content: dataArray, Checks: checks})
	if !res.HardDecisive() || res.HardPassed {
		t.Fatalf("expected failure: %+v", res)
	}
	for _, reason := range res.Reasons {
		if len(reason) > 4096 {
			t.Errorf("reason is unbounded: %d bytes", len(reason))
		}
	}
	if !strings.Contains(strings.Join(res.Reasons, " "), "array_length") {
		t.Errorf("reasons = %v", res.Reasons)
	}
}
