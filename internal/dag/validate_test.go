package dag

import (
	"errors"
	"testing"
	"time"
)

// node builds a valid leaf task with one criterion.
func node(key string, deps ...string) Node {
	return Node{Key: key, Title: key, DependsOn: deps, Criteria: []string{"c:" + key}}
}

func wantKind(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error, got nil", kind)
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error type = %T, want *ValidationError (%v)", err, err)
	}
	if ve.Kind != kind {
		t.Fatalf("kind = %s, want %s (%v)", ve.Kind, kind, err)
	}
}

func TestValidateHappyPathAndOrder(t *testing.T) {
	g := Graph{Nodes: []Node{
		{Key: "plan", Title: "plan"}, // parent, no criteria required
		{Key: "a", ParentKey: "plan", Title: "a", Criteria: []string{"x"}},
		{Key: "b", ParentKey: "plan", Title: "b", DependsOn: []string{"a"}, Criteria: []string{"y"}},
	}}
	order, err := Validate(g, Limits{MaxTasks: 10, MaxDepth: 3, MaxTotalJobs: 10})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("order = %v, want 3 entries", order)
	}
	pos := map[string]int{}
	for i, k := range order {
		pos[k] = i
	}
	if pos["a"] > pos["b"] {
		t.Errorf("dependency order violated: %v", order)
	}
}

func TestValidateRejections(t *testing.T) {
	cases := []struct {
		name   string
		graph  Graph
		limits Limits
		want   ErrorKind
	}{
		{"empty", Graph{}, Limits{}, KindEmpty},
		{"too many", Graph{Nodes: []Node{node("a"), node("b")}}, Limits{MaxTasks: 1}, KindTooMany},
		{"duplicate key", Graph{Nodes: []Node{node("a"), node("a")}}, Limits{}, KindDuplicateKey},
		{"unknown dep", Graph{Nodes: []Node{node("a", "ghost")}}, Limits{}, KindUnknownRef},
		{"self dep", Graph{Nodes: []Node{node("a", "a")}}, Limits{}, KindSelfRef},
		{"unknown parent", Graph{Nodes: []Node{{Key: "a", ParentKey: "ghost", Criteria: []string{"c"}}}}, Limits{}, KindUnknownRef},
		{"parent self", Graph{Nodes: []Node{{Key: "a", ParentKey: "a", Criteria: []string{"c"}}}}, Limits{}, KindSelfRef},
		{"dependency cycle", Graph{Nodes: []Node{node("a", "b"), node("b", "a")}}, Limits{}, KindCycle},
		{"parent cycle", Graph{Nodes: []Node{
			{Key: "a", ParentKey: "b", Criteria: []string{"c"}},
			{Key: "b", ParentKey: "a", Criteria: []string{"c"}},
		}}, Limits{}, KindCycle},
		{"coverage", Graph{Nodes: []Node{{Key: "a", Title: "a"}}}, Limits{}, KindCoverage},
		{"depth", Graph{Nodes: []Node{node("a"), node("b", "a"), node("c", "b")}}, Limits{MaxDepth: 2}, KindDepth},
		{"negative budget", Graph{Nodes: []Node{{Key: "a", Title: "a", Criteria: []string{"c"}, Budget: Budget{MaxJobs: -1}}}}, Limits{}, KindBudget},
		{"total budget", Graph{Nodes: []Node{
			{Key: "a", Title: "a", Criteria: []string{"c"}, Budget: Budget{MaxJobs: 5}},
			{Key: "b", Title: "b", Criteria: []string{"c"}, Budget: Budget{MaxJobs: 5}},
		}}, Limits{MaxTotalJobs: 8}, KindBudget},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Validate(c.graph, c.limits)
			wantKind(t, err, c.want)
		})
	}
}

// TestValidateParentWithoutCriteriaIsAllowed proves coverage is about
// leaves: a phase node that parents other tasks need not carry criteria.
func TestValidateParentWithoutCriteriaIsAllowed(t *testing.T) {
	g := Graph{Nodes: []Node{
		{Key: "phase", Title: "phase"},
		{Key: "leaf", ParentKey: "phase", Title: "leaf", Criteria: []string{"done"}},
	}}
	if _, err := Validate(g, Limits{}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// TestValidateDisabledBounds proves a non-positive limit disables that
// bound (the test-facing escape hatch).
func TestValidateDisabledBounds(t *testing.T) {
	g := Graph{Nodes: []Node{
		{Key: "a", Title: "a", Criteria: []string{"c"}, Budget: Budget{MaxJobs: 1000, MaxWallTime: time.Hour}},
		{Key: "b", Title: "b", Criteria: []string{"c"}, DependsOn: []string{"a"}},
		{Key: "c", Title: "c", Criteria: []string{"c"}, DependsOn: []string{"b"}},
	}}
	if _, err := Validate(g, Limits{}); err != nil {
		t.Fatalf("Validate with no bounds: %v", err)
	}
}

func TestValidateDeterministicOrder(t *testing.T) {
	g := Graph{Nodes: []Node{node("b"), node("a"), node("c", "a", "b")}}
	first, err := Validate(g, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Validate(g, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 || first[0] != second[0] || first[1] != second[1] || first[2] != second[2] {
		t.Errorf("order not deterministic: %v vs %v", first, second)
	}
	if first[len(first)-1] != "c" {
		t.Errorf("expected dependent last, got %v", first)
	}
}
