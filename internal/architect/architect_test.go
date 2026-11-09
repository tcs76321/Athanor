package architect

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/workflow"
)

type fakeLLM struct {
	response string
	err      error
}

func (f fakeLLM) Complete(context.Context, string) (string, error) { return f.response, f.err }

func TestProposeValidModelProposal(t *testing.T) {
	raw := `{
	  "questions": [],
	  "frame": {"name":"Ledger CLI","goal":"Build a ledger CLI with tests","outcome":"A tested CLI","capabilities":["code"],"first_cycle":"build"},
	  "graph": {"blocks":[{"id":"implement","kind":"implement"},{"id":"test","kind":"test"}],
	            "edges":[{"from":"implement","to":"test"}]}
	}`
	s := New(fakeLLM{response: raw})
	p, err := s.Propose(context.Background(), []Message{{Role: "user", Content: "Build a ledger CLI."}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Degraded {
		t.Fatalf("degraded: %s", p.Notes)
	}
	if !p.Ready() {
		t.Fatalf("proposal not ready: %+v", p)
	}
	if p.Frame.FirstCycle != "build" || p.Frame.Name != "Ledger CLI" {
		t.Errorf("frame = %+v", p.Frame)
	}
	if err := workflow.Validate(p.Graph, workflow.DefaultLimits()); err != nil {
		t.Errorf("graph invalid: %v", err)
	}
}

func TestProposeFallsBackOnInvalidGraph(t *testing.T) {
	raw := `{"questions":[],"frame":{"name":"X","goal":"Build a thing","first_cycle":"build"},
	  "graph":{"blocks":[{"id":"a","kind":"teleport"}],"edges":[]}}`
	s := New(fakeLLM{response: raw})
	p, err := s.Propose(context.Background(), []Message{{Role: "user", Content: "Build a thing."}})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Degraded {
		t.Fatal("invalid graph was not degraded")
	}
	if err := workflow.Validate(p.Graph, workflow.DefaultLimits()); err != nil {
		t.Fatalf("fallback graph invalid: %v", err)
	}
	if !p.Ready() {
		t.Fatal("fallback should be approvable")
	}
}

func TestProposeFallsBackOnModelFailure(t *testing.T) {
	s := New(fakeLLM{err: errors.New("no model")})
	p, err := s.Propose(context.Background(), []Message{{Role: "user", Content: "Build a small REST API."}})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Degraded || !p.Ready() {
		t.Fatalf("fallback = %+v", p)
	}
	if !strings.Contains(p.Notes, "no model") {
		t.Errorf("notes = %q, want the model error", p.Notes)
	}
	if p.Frame.FirstCycle != "build" {
		t.Errorf("first cycle = %q, want build", p.Frame.FirstCycle)
	}
}

func TestProposeKeepsQuestionsAndDiscovery(t *testing.T) {
	raw := `{"questions":["What genre?","What platform?"],
	  "frame":{"name":"Game","goal":"Make a game","first_cycle":"discovery","capabilities":["code","document"]},
	  "graph":{"blocks":[{"id":"options","kind":"generate_options"},{"id":"critique","kind":"critique"},{"id":"select","kind":"select"}],
	            "edges":[{"from":"options","to":"critique"},{"from":"critique","to":"select"}]}}`
	s := New(fakeLLM{response: raw})
	p, err := s.Propose(context.Background(), []Message{{Role: "user", Content: "I want to make a game but I don't know what."}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Ready() {
		t.Fatal("questions should keep the proposal unready")
	}
	if len(p.Questions) != 2 || p.Frame.FirstCycle != "discovery" {
		t.Errorf("proposal = %+v", p)
	}
	if err := workflow.Validate(p.Graph, workflow.DefaultLimits()); err != nil {
		t.Errorf("graph invalid: %v", err)
	}
}

func TestProposeNormalizesIDsAndEdges(t *testing.T) {
	raw := `{"questions":[],"frame":{"name":"Doc","goal":"Write a brief","first_cycle":"design"},
	  "graph":{"blocks":[{"id":"Research","kind":"research"},{"id":"Fetch","kind":"fetch"}],
	            "edges":[{"from":"Research","to":"Fetch"}]}}`
	s := New(fakeLLM{response: raw})
	p, err := s.Propose(context.Background(), []Message{{Role: "user", Content: "Write a brief."}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Degraded {
		t.Fatalf("normalized graph degraded: %s", p.Notes)
	}
	if p.Graph.Blocks[0].ID != "research" || p.Graph.Edges[0].From != "research" {
		t.Errorf("graph = %+v", p.Graph)
	}
}

func TestProposeAlignsSelectionGraphToDiscovery(t *testing.T) {
	// The model may label a choose-a-direction graph "build". A graph that ends
	// in a human select is a discovery cycle: the user is choosing, not shipping.
	raw := `{"questions":[],"frame":{"name":"Game","goal":"Make a game","first_cycle":"build","capabilities":["code"]},
	  "graph":{"blocks":[{"id":"research","kind":"research"},{"id":"options","kind":"generate_options"},{"id":"select","kind":"select"}],
	            "edges":[{"from":"research","to":"options"},{"from":"options","to":"select"}]}}`
	s := New(fakeLLM{response: raw})
	p, err := s.Propose(context.Background(), []Message{{Role: "user", Content: "Research directions for a game."}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Degraded {
		t.Fatalf("degraded: %s", p.Notes)
	}
	if p.Frame.FirstCycle != "discovery" {
		t.Errorf("first cycle = %q, want discovery", p.Frame.FirstCycle)
	}
	if err := workflow.Validate(p.Graph, workflow.DefaultLimits()); err != nil {
		t.Errorf("research -> options graph invalid: %v", err)
	}
}

func TestProposeRejectsEmptyConversation(t *testing.T) {
	s := New(nil)
	if _, err := s.Propose(context.Background(), nil); err == nil {
		t.Fatal("empty conversation accepted")
	}
	if _, err := s.Propose(context.Background(), []Message{{Role: "assistant", Content: "hi"}}); err == nil {
		t.Fatal("assistant-only conversation accepted")
	}
}

func TestFallbackVagueGoalIsDiscovery(t *testing.T) {
	p := fallbackProposal("I don't know what game to make.", []Message{{Role: "user", Content: "I don't know what game to make."}})
	if p.Frame.FirstCycle != "discovery" {
		t.Errorf("first cycle = %q, want discovery", p.Frame.FirstCycle)
	}
	if len(p.Graph.Blocks) != 3 || p.Graph.Blocks[0].Kind != workflow.KindGenerateOptions {
		t.Errorf("fallback graph = %+v", p.Graph)
	}
}

func TestFallbackFillsFrame(t *testing.T) {
	p := fallbackProposal("Build a small REST API with tests.", []Message{{Role: "user", Content: "Build a small REST API with tests."}})
	if p.Frame.Name == "" || p.Frame.Goal == "" || p.Frame.Outcome == "" {
		t.Errorf("frame = %+v", p.Frame)
	}
	if p.Frame.FirstCycle != "build" {
		t.Errorf("first cycle = %q, want build", p.Frame.FirstCycle)
	}
	if len(p.Frame.Acceptance) == 0 || len(p.Frame.Capabilities) == 0 {
		t.Errorf("frame defaults = %+v", p.Frame)
	}
	if !strings.Contains(p.Frame.ReviewPolicy, "external") {
		t.Errorf("review policy = %q", p.Frame.ReviewPolicy)
	}
}
