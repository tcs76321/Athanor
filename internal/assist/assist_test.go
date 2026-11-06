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
	err    error
}

func (f *fakeGen) Generate(ctx context.Context, system, user string) (string, error) {
	f.system, f.user = system, user
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
	if !strings.Contains(gen.system, "generic adjectives") || !strings.Contains(gen.system, "already the ideal version") {
		t.Fatalf("system prompt lost the conservative rules: %q", gen.system)
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
	if !strings.Contains(gen.system, "already the ideal version") {
		t.Fatalf("system prompt does not ask for the ideal check: %q", gen.system)
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

func TestRefineRejectsUnknownFieldAndEmptyText(t *testing.T) {
	s := New(&fakeGen{out: "x"})
	if _, err := s.Refine(context.Background(), "not.a.field", "text", ""); err == nil {
		t.Fatal("unknown field was accepted")
	}
	if _, err := s.Refine(context.Background(), "project.goal", "   ", ""); err == nil {
		t.Fatal("empty field was accepted")
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

func TestRefineRejectsRunawayOutput(t *testing.T) {
	s := New(&fakeGen{out: strings.Repeat("long ", 6000)})
	if _, err := s.Refine(context.Background(), "project.goal", "short", ""); err == nil {
		t.Fatal("runaway output was accepted")
	}
}
