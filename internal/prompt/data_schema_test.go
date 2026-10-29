package prompt

import (
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/llm"
)

// TestDataSchemaIsPinnedAndRendered pins the P0 data contract: the schema
// reaches the prompt and lives in a pinned tier, so the §10.5 ladder cannot
// evict it even under extreme pressure.
func TestDataSchemaIsPinnedAndRendered(t *testing.T) {
	schema := `{"type":"array","items":{"type":"object","required":["id"]}}`
	res, err := Assemble(Input{
		Phase:   llm.PhaseDiverging,
		Project: Project{Name: "data-demo", Archetype: "data", Goal: "normalize events"},
		Task:    Task{Title: "normalize"},
		Criteria: []string{
			"output validates against the schema",
		},
		DataSchema: schema,
		// A ceiling that forces every evictable tier out. Tier 2 is pinned.
		Ceiling: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, schema) {
		t.Fatalf("schema missing from assembled prompt:\n%s", res.Text)
	}
	found := false
	for _, s := range res.Sections {
		if s.Name == SectionDataSchema {
			found = true
			if s.Tokens <= 0 {
				t.Error("data schema section has no token weight")
			}
		}
	}
	if !found {
		t.Error("data schema section not present")
	}
	if res.Eviction.Suppresses(TierTaskCriteria) {
		t.Error("tier 2 was evicted; pinned tiers must never be suppressed")
	}
}
