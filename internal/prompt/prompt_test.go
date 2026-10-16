package prompt

import (
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/llm"
)

func sampleInput() Input {
	return Input{
		Phase:   llm.PhasePlanning,
		Project: Project{Name: "my-rest-api", Archetype: "code", Goal: "Build a small REST API for managing a personal book collection."},
		Task:    Task{Title: "Design the endpoint layout", Description: "Propose endpoints for CRUD on books."},
		Criteria: []string{
			"pytest passes with >90% coverage on new endpoints",
			"OpenAPI spec documents every endpoint",
		},
	}
}

// TestAssembleDeterministic is the M1-T3 acceptance criterion: the same
// inputs must produce a byte-identical prompt. Assemble is pure, so
// repeated calls suffice; any nondeterminism (map iteration, timestamps)
// would show here.
func TestAssembleDeterministic(t *testing.T) {
	first, err := Assemble(sampleInput())
	if err != nil {
		t.Fatalf("Assemble() err = %v", err)
	}
	for i := 0; i < 10; i++ {
		again, err := Assemble(sampleInput())
		if err != nil {
			t.Fatalf("Assemble() run %d err = %v", i, err)
		}
		if again.Text != first.Text {
			t.Fatalf("run %d produced different prompt text", i)
		}
		if again.TotalToken != first.TotalToken {
			t.Fatalf("run %d produced different token total", i)
		}
	}
}

// TestAssembleSectionOrder pins the §11.2 order for the M1 subset.
func TestAssembleSectionOrder(t *testing.T) {
	res, err := Assemble(sampleInput())
	if err != nil {
		t.Fatalf("Assemble() err = %v", err)
	}
	wantOrder := []string{
		SectionSystem,
		SectionSecurityAndTools,
		SectionRuntimePolicy,
		SectionProjectContext,
		SectionTaskContext,
		SectionAcceptanceCriteria,
	}
	if len(res.Sections) != len(wantOrder) {
		t.Fatalf("got %d sections (%v), want %d", len(res.Sections), sectionNames(res.Sections), len(wantOrder))
	}
	for i, name := range wantOrder {
		if res.Sections[i].Name != name {
			t.Errorf("section %d = %q, want %q", i, res.Sections[i].Name, name)
		}
	}

	// The full text must also carry the sections in order.
	last := -1
	for _, name := range wantOrder {
		idx := strings.Index(res.Text, sectionText(res, name))
		if idx < 0 {
			t.Fatalf("section %q missing from assembled text", name)
		}
		if idx < last {
			t.Errorf("section %q appears out of order in assembled text", name)
		}
		last = idx
	}
}

// TestAssembleTokenAccounting proves per-section accounting (§11.2: token
// counts logged to the EventLog): each section counts independently and
// the total is the sum.
func TestAssembleTokenAccounting(t *testing.T) {
	res, err := Assemble(sampleInput())
	if err != nil {
		t.Fatalf("Assemble() err = %v", err)
	}
	sum := 0
	for _, s := range res.Sections {
		if s.Tokens != EstimateTokens(s.Text) {
			t.Errorf("section %q tokens = %d, want EstimateTokens(text) = %d", s.Name, s.Tokens, EstimateTokens(s.Text))
		}
		sum += s.Tokens
	}
	// known-issues L5: TotalToken counts the "\n\n" section joins, so it is at
	// least the sum of the section estimates (usually a token or two more).
	if res.TotalToken < sum {
		t.Errorf("TotalToken = %d, want >= %d (sum of sections; joins add the rest)", res.TotalToken, sum)
	}
}

func TestAssembleOmitsEmptySections(t *testing.T) {
	in := Input{
		Phase:   llm.PhaseSynthesizing,
		Project: Project{Name: "p", Archetype: "text", Goal: "g"},
		Task:    Task{Title: "t"},
		// no criteria, no evaluation instructions
	}
	res, err := Assemble(in)
	if err != nil {
		t.Fatalf("Assemble() err = %v", err)
	}
	for _, s := range res.Sections {
		if s.Name == SectionAcceptanceCriteria || s.Name == SectionEvaluationInstructions {
			t.Errorf("empty input produced section %q, want omission", s.Name)
		}
	}
	// A task description is optional; the task section still appears.
	found := false
	for _, s := range res.Sections {
		if s.Name == SectionTaskContext {
			found = true
		}
	}
	if !found {
		t.Error("task context section missing entirely")
	}
}

func TestAssembleRejectsUnknownPhase(t *testing.T) {
	in := sampleInput()
	in.Phase = "hallucinating"
	if _, err := Assemble(in); err == nil || !strings.Contains(err.Error(), "no runtime policy") {
		t.Fatalf("Assemble(unknown phase) err = %v, want runtime-policy error", err)
	}
}

func TestAssembleRejectsMissingProjectAndTask(t *testing.T) {
	in := sampleInput()
	in.Project.Name = ""
	if _, err := Assemble(in); err == nil || !strings.Contains(err.Error(), "project name") {
		t.Fatalf("Assemble(no project) err = %v, want project-name error", err)
	}
	in = sampleInput()
	in.Task.Title = ""
	if _, err := Assemble(in); err == nil || !strings.Contains(err.Error(), "task title") {
		t.Fatalf("Assemble(no task) err = %v, want task-title error", err)
	}
}

// TestAssembleMessageSplit pins the system/user split: §11.2 sections 1–3
// become the system message; project/task/criteria/evaluation become the
// user message.
func TestAssembleMessageSplit(t *testing.T) {
	res, err := Assemble(sampleInput())
	if err != nil {
		t.Fatalf("Assemble() err = %v", err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(res.Messages))
	}
	sys, user := res.Messages[0], res.Messages[1]
	if sys.Role != "system" || user.Role != "user" {
		t.Fatalf("message roles = %q/%q, want system/user", sys.Role, user.Role)
	}
	for _, must := range []string{"You are Athanor", "CONTAINMENT RULES", "PHASE: PLANNING"} {
		if !strings.Contains(sys.Content, must) {
			t.Errorf("system message missing %q", must)
		}
	}
	for _, mustNot := range []string{"PROJECT", "TASK", "ACCEPTANCE CRITERIA"} {
		if strings.Contains(sys.Content, mustNot) {
			t.Errorf("system message must not contain %q", mustNot)
		}
	}
	for _, must := range []string{"PROJECT", "TASK", "ACCEPTANCE CRITERIA", "my-rest-api"} {
		if !strings.Contains(user.Content, must) {
			t.Errorf("user message missing %q", must)
		}
	}
}

// TestCriteriaPreservedVerbatim proves criteria text is passed through
// unchanged — they are requirements, and any rewriting would silently
// weaken them.
func TestCriteriaPreservedVerbatim(t *testing.T) {
	exact := "pytest passes with >90% coverage on new endpoints"
	res, err := Assemble(Input{
		Phase:    llm.PhaseComparing,
		Project:  Project{Name: "p", Archetype: "code", Goal: "g"},
		Task:     Task{Title: "t"},
		Criteria: []string{exact},
	})
	if err != nil {
		t.Fatalf("Assemble() err = %v", err)
	}
	if !strings.Contains(res.Text, "1. "+exact) {
		t.Error("criterion text was not preserved verbatim as item 1")
	}
}

func TestEstimateTokensDeterministic(t *testing.T) {
	if got := EstimateTokens(""); got != 0 {
		t.Errorf("EstimateTokens(\"\") = %d, want 0", got)
	}
	if got := EstimateTokens("abcd"); got != 1 {
		t.Errorf("EstimateTokens(4 bytes) = %d, want 1", got)
	}
	if got := EstimateTokens("abcde"); got != 2 {
		t.Errorf("EstimateTokens(5 bytes) = %d, want 2 (round up)", got)
	}
}

func sectionNames(ss []Section) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name
	}
	return out
}

func sectionText(res Result, name string) string {
	for _, s := range res.Sections {
		if s.Name == name {
			return s.Text
		}
	}
	return ""
}

// TestAssemble_SynthesisPhase_HasNoPreambleInstruction pins the M1-T8.1
// soft-suppression wording: the synthesis phase policy must instruct
// the model to produce only the artifact (with an optional single
// trailing LIMITATION: line), and must not contain the old
// "change summary and known limitations" preamble that the quality
// probe found on every artifact.
func TestAssemble_SynthesisPhase_HasNoPreambleInstruction(t *testing.T) {
	in := sampleInput()
	in.Phase = llm.PhaseSynthesizing
	res, err := Assemble(in)
	if err != nil {
		t.Fatalf("Assemble(synthesizing) err = %v", err)
	}
	policy := sectionText(res, SectionRuntimePolicy)
	if policy == "" {
		t.Fatal("synthesizing policy is empty")
	}
	if strings.Contains(policy, "change summary") || strings.Contains(policy, "known limitations") {
		t.Errorf("synthesis policy still requests preamble; got %q", policy)
	}
	if !strings.Contains(policy, "No preamble") {
		t.Errorf("synthesis policy missing no-preamble instruction; got %q", policy)
	}
	if !strings.Contains(policy, "LIMITATION:") {
		t.Errorf("synthesis policy missing single-line LIMITATION: allowance; got %q", policy)
	}
}
