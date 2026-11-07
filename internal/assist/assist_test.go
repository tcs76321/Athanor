package assist

import (
	"context"
	"strings"
	"testing"
)

type fakeGen struct {
	system string
	user   string
	out    string
	outs   []string
	err    error
	called bool
}

func (f *fakeGen) Generate(ctx context.Context, system, user string) (string, error) {
	f.called = true
	f.system, f.user = system, user
	if len(f.outs) > 0 {
		out := f.outs[0]
		f.outs = f.outs[1:]
		return out, f.err
	}
	return f.out, f.err
}

func TestRefineParsesStructuredProposal(t *testing.T) {
	gen := &fakeGen{out: `{"text":"Build a tested CSV parser that handles empty rows.","notes":["made the outcome explicit"],"added_assumptions":["empty rows are in scope"]}`}
	s := New(gen)
	res, err := s.Refine(context.Background(), "project.goal", "  make a csv parser  ", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "tested CSV parser") {
		t.Fatalf("text = %q", res.Text)
	}
	if len(res.Notes) != 1 || len(res.AddedAssumptions) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if res.Field != "project.goal" || res.Mode != "improve" {
		t.Fatalf("metadata = %+v", res)
	}
	if !strings.Contains(gen.user, "PURPOSE:") || !strings.Contains(gen.user, "make a csv parser") {
		t.Fatalf("prompt = %q", gen.user)
	}
	if !strings.Contains(gen.system, "Never invent facts") {
		t.Fatalf("system prompt = %q", gen.system)
	}
	if !strings.Contains(gen.system, "generic adjectives") || !strings.Contains(gen.system, "Improvement is the default") {
		t.Fatalf("system prompt lost the editing principles: %q", gen.system)
	}
}

func TestRefineAcceptsStringNotesAndAssumptions(t *testing.T) {
	// Small local models often return notes as one string rather than an array.
	gen := &fakeGen{out: `{"text":"The project goal is to continuously search for news, translate it, and produce daily English reports.","notes":"Restructured around the outcome and scope.","added_assumptions":["The audience is not stated."],"unchanged":false,"needs_clarification":false,"questions":[]}`}
	res, err := New(gen).Refine(context.Background(), "project.goal", "search news, translate, report", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.TrimSpace(res.Text), "{") {
		t.Fatalf("JSON leaked into the field text: %q", res.Text)
	}
	if !strings.Contains(res.Text, "daily English reports") {
		t.Fatalf("text = %q", res.Text)
	}
	if len(res.Notes) != 1 || len(res.AddedAssumptions) != 1 {
		t.Fatalf("result = %+v", res)
	}
}

func TestRefineUnwrapsNestedJSONText(t *testing.T) {
	gen := &fakeGen{out: `{"text":"{\"text\":\"A clear goal.\",\"notes\":[\"restructured\"]}","notes":[],"added_assumptions":[],"unchanged":false}`}
	res, err := New(gen).Refine(context.Background(), "project.goal", "rough goal", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "A clear goal." {
		t.Fatalf("nested text = %q", res.Text)
	}
	if len(res.Notes) != 1 || res.Notes[0] != "restructured" {
		t.Fatalf("nested notes = %+v", res.Notes)
	}
}

func TestRefineAcceptsPlainTextFallback(t *testing.T) {
	s := New(&fakeGen{out: "A clearer goal."})
	res, err := s.Refine(context.Background(), "project.goal", "rough goal", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "A clearer goal." || len(res.Notes) == 0 {
		t.Fatalf("fallback = %+v", res)
	}
}

func TestRefineMarksIdealTextUnchanged(t *testing.T) {
	text := "Build a tested CSV parser that handles empty rows."
	gen := &fakeGen{out: `{"text":"Build a tested CSV parser that handles empty rows.","notes":["already complete"],"unchanged":true}`}
	res, err := New(gen).Refine(context.Background(), "project.goal", text, "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unchanged {
		t.Fatalf("result = %+v, want unchanged", res)
	}
	if res.Text != text {
		t.Fatalf("unchanged text = %q, want the original", res.Text)
	}
	if !strings.Contains(gen.system, "Improvement is the default") {
		t.Fatalf("system prompt does not make improvement the default: %q", gen.system)
	}
}

func TestRefineDetectsIdenticalTextWithoutFlag(t *testing.T) {
	text := "Build a tested CSV parser."
	res, err := New(&fakeGen{out: `{"text":"Build a tested CSV parser.","unchanged":false}`}).Refine(context.Background(), "project.goal", text, "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unchanged {
		t.Fatalf("identical text was not marked unchanged: %+v", res)
	}
}

func TestRefineAsksForClarificationWhenEmpty(t *testing.T) {
	gen := &fakeGen{out: `{"text":"should be ignored"}`}
	res, err := New(gen).Refine(context.Background(), "project.goal", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.NeedsClarification || res.Text != "" || len(res.Questions) < 2 {
		t.Fatalf("clarification = %+v", res)
	}
	if gen.called {
		t.Fatal("blank field should not spend a model call")
	}
}

func TestRefineAsksForClarificationWhenVague(t *testing.T) {
	gen := &fakeGen{out: `{"needs_clarification":true}`}
	res, err := New(gen).Refine(context.Background(), "project.goal", "do the thing", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.NeedsClarification || len(res.Questions) < 2 {
		t.Fatalf("clarification = %+v", res)
	}
}

func TestRefineFallsBackToQuestionsWhenModelReturnsNothing(t *testing.T) {
	res, err := New(&fakeGen{out: `{"text":""}`}).Refine(context.Background(), "project.goal", "rough goal", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.NeedsClarification || len(res.Questions) < 2 {
		t.Fatalf("fallback = %+v", res)
	}
}

func TestRefineRetriesOverEagerClarification(t *testing.T) {
	gen := &fakeGen{outs: []string{
		`{"needs_clarification":true,"questions":["What is the outcome?"]}`,
		`{"text":"Build a small todo app with a tested add-and-remove flow.","notes":["made the outcome explicit"]}`,
	}}
	res, err := New(gen).Refine(context.Background(), "project.goal", "Build a small todo app", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.NeedsClarification || !strings.Contains(res.Text, "tested add-and-remove") {
		t.Fatalf("retry result = %+v", res)
	}
	if !strings.Contains(gen.user, "Do not set needs_clarification") {
		t.Fatalf("retry prompt did not forbid clarification: %q", gen.user)
	}
}

func TestRefineLeavesUsableTextUnchangedWhenRetryStillClarifies(t *testing.T) {
	gen := &fakeGen{outs: []string{
		`{"needs_clarification":true}`,
		`{"needs_clarification":true}`,
	}}
	const text = "Build a small todo app"
	res, err := New(gen).Refine(context.Background(), "project.goal", text, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.NeedsClarification || !res.Unchanged || res.Text != text {
		t.Fatalf("usable text was replaced by questions: %+v", res)
	}
	if len(res.Notes) == 0 || !strings.Contains(res.Notes[0], "unchanged") {
		t.Fatalf("missing unchanged note: %+v", res)
	}
}

func TestRefineRejectsUnknownField(t *testing.T) {
	s := New(&fakeGen{out: "x"})
	if _, err := s.Refine(context.Background(), "not.a.field", "text", ""); err == nil {
		t.Fatal("unknown field was accepted")
	}
	if _, err := s.Refine(context.Background(), "project.goal", strings.Repeat("x", 20001), ""); err == nil {
		t.Fatal("oversize field was accepted")
	}
}

func TestProofreadModeHasStrictContract(t *testing.T) {
	gen := &fakeGen{out: `{"text":"A clearer goal."}`}
	s := New(gen)
	if _, err := s.Refine(context.Background(), "project.goal", "rough goal", "proofread"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gen.user, "Fix grammar, spelling, and punctuation only") {
		t.Fatalf("proofread prompt = %q", gen.user)
	}
}

func TestRefineForcesImprovementWhenFirstPassEchoes(t *testing.T) {
	const text = "Build a small todo app"
	gen := &fakeGen{outs: []string{
		`{"text":"Build a small todo app","unchanged":true}`,
		`{"text":"Build a small local todo app with add, complete, and remove flows, and a passing test for each.","notes":["made the outcome and scope concrete"],"added_assumptions":["the first version is local and single-user"]}`,
	}}
	res, err := New(gen).Refine(context.Background(), "project.goal", text, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Unchanged || !strings.Contains(res.Text, "add, complete, and remove") {
		t.Fatalf("forced improvement = %+v", res)
	}
	if !strings.Contains(gen.user, "OPERATOR ACTION") {
		t.Fatalf("forced prompt missing operator action: %q", gen.user)
	}
}

func TestRefineContextReachesPrompt(t *testing.T) {
	gen := &fakeGen{out: `{"text":"Build a tested CSV parser for the importer.","notes":["used the repository context"]}`}
	res, err := New(gen).RefineContext(context.Background(), "project.goal", "build a csv parser", "", Context{
		ProjectName:    "importer",
		RepositoryPath: "/Users/me/athanor/repos/importer",
		Runtime:        "Core Pod",
		Version:        "0.0.0-dev",
		Fields:         map[string]string{"criteria": "pytest passes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text == "" {
		t.Fatal("empty result")
	}
	for _, want := range []string{"ATHANOR CONTEXT", "project name: importer", "form field criteria: pytest passes", "runtime: Core Pod"} {
		if !strings.Contains(gen.user, want) {
			t.Errorf("prompt missing %q: %q", want, gen.user)
		}
	}
}

func TestRefineBlankExplainsWhyItNeedsInput(t *testing.T) {
	gen := &fakeGen{out: `{"text":"ignored"}`}
	res, err := New(gen).Refine(context.Background(), "project.goal", "   ", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.NeedsClarification || len(res.Questions) == 0 || len(res.Notes) == 0 {
		t.Fatalf("blank result = %+v", res)
	}
	if gen.called {
		t.Fatal("blank field spent a model call")
	}
}
func TestRefineRejectsRunawayOutput(t *testing.T) {
	s := New(&fakeGen{out: strings.Repeat("long ", 6000)})
	if _, err := s.Refine(context.Background(), "project.goal", "short", ""); err == nil {
		t.Fatal("runaway output was accepted")
	}
}
