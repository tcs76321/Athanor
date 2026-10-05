package dag

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseValid(t *testing.T) {
	raw := "Here is the plan:\n```json\n" + `{
	  "tasks": [
	    {"key":"schema","title":"Define schema","description":"tables","acceptance_criteria":["schema.sql exists"],"budget":{"max_jobs":1,"max_llm_calls":4,"max_tokens":8000,"max_wall_time":"20m"}},
	    {"key":"impl","parent":"phase","title":"Implement","depends_on":["schema"],"acceptance_criteria":["tests pass"]}
	  ]
	}` + "\n```\ndone"
	g, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(g.Nodes))
	}
	if g.Nodes[0].Key != "schema" || g.Nodes[0].Budget.MaxWallTime != 20*time.Minute {
		t.Errorf("node 0 = %+v", g.Nodes[0])
	}
	if g.Nodes[1].ParentKey != "phase" || len(g.Nodes[1].DependsOn) != 1 || g.Nodes[1].DependsOn[0] != "schema" {
		t.Errorf("node 1 = %+v", g.Nodes[1])
	}
}

// TestParseEmbeddedBraceInString proves the brace scanner is quote-aware.
func TestParseEmbeddedBraceInString(t *testing.T) {
	raw := `{"tasks":[{"key":"a","title":"uses } and { in a title","acceptance_criteria":["x"]}]}`
	g, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if g.Nodes[0].Title != "uses } and { in a title" {
		t.Errorf("title = %q", g.Nodes[0].Title)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no object", "no json here", "no JSON object"},
		{"unterminated", `{"tasks":[`, "unterminated"},
		{"empty tasks", `{"tasks":[]}`, "no tasks"},
		{"missing key", `{"tasks":[{"title":"t"}]}`, "no key"},
		{"missing title", `{"tasks":[{"key":"a"}]}`, "no title"},
		{"bad duration", `{"tasks":[{"key":"a","title":"t","budget":{"max_wall_time":"soon"}}]}`, "max_wall_time"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.in)
			if err == nil {
				t.Fatalf("expected error")
			}
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("error type = %T, want *ParseError", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not contain %q", err, c.want)
			}
		})
	}
}

func TestBudgetJSONRoundTrip(t *testing.T) {
	b := Budget{MaxJobs: 2, MaxLLMCalls: 6, MaxTokens: 12000, MaxWallTime: 90 * time.Second}
	raw, err := b.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"max_wall_time":"1m30s"`) {
		t.Fatalf("marshalled budget = %s", raw)
	}
	var got Budget
	if err := got.UnmarshalJSON(raw); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if got != b {
		t.Errorf("round-trip = %+v, want %+v", got, b)
	}
}
