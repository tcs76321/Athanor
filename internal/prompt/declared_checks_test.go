package prompt

import (
	"strings"
	"testing"
)

// TestDeclaredChecksArePinned proves the model sees the exact parser-enforced
// limits, and that the §10.5 ladder can never evict them: they live in the
// same tier as the acceptance criteria.
func TestDeclaredChecksArePinned(t *testing.T) {
	in := Input{
		Phase:    "synthesizing",
		Project:  Project{Name: "demo", Archetype: "document", Goal: "write a brief"},
		Task:     Task{Title: "brief"},
		Criteria: []string{"under 110 words"},
		Checks:   []string{"word count must be at most 110"},
		Ceiling:  1,
	}
	res, err := Assemble(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "DECLARED CHECKS") {
		t.Fatalf("declared checks section missing:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "word count must be at most 110") {
		t.Fatalf("declared check text missing:\n%s", res.Text)
	}
	found := false
	for _, s := range res.Sections {
		if s.Name == SectionDeclaredChecks {
			found = true
		}
	}
	if !found {
		t.Fatalf("section %q missing from accounting: %+v", SectionDeclaredChecks, res.Sections)
	}
}

func TestNotebookIsPinnedAndRendered(t *testing.T) {
	in := Input{
		Phase:    "diverging",
		Project:  Project{Name: "demo", Archetype: "text", Goal: "write"},
		Task:     Task{Title: "draft"},
		Notebook: []string{"[plan/planning] write an outline", "[result/evaluating] candidate 1 failed: too long"},
		Ceiling:  1,
	}
	res, err := Assemble(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "WORKING NOTES") ||
		!strings.Contains(res.Text, "write an outline") ||
		!strings.Contains(res.Text, "candidate 1 failed") {
		t.Fatalf("notebook missing from prompt:\n%s", res.Text)
	}
	found := false
	for _, s := range res.Sections {
		if s.Name == SectionNotebook {
			found = true
		}
	}
	if !found {
		t.Fatalf("notebook section missing: %+v", res.Sections)
	}
}

func TestDeclaredChecksOmittedWhenEmpty(t *testing.T) {
	in := Input{
		Phase:   "synthesizing",
		Project: Project{Name: "demo", Archetype: "document", Goal: "write a brief"},
		Task:    Task{Title: "brief"},
	}
	res, err := Assemble(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "DECLARED CHECKS") {
		t.Fatalf("empty check set rendered a section:\n%s", res.Text)
	}
}
