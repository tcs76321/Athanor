package verify

import (
	"testing"

	"github.com/tcs76321/athanor/internal/project"
)

func TestCodeVerifiers(t *testing.T) {
	r := Default()
	pass := r.Run(Input{Archetype: project.ArchetypeCode, TestRan: true, TestsPassed: true, TestCommand: "pytest -q"})
	if !pass.Decisive() || !pass.Passed || pass.Score != 1 {
		t.Fatalf("passing tests: %+v", pass)
	}
	fail := r.Run(Input{Archetype: project.ArchetypeCode, TestRan: true, TestsPassed: false, TestCommand: "pytest -q"})
	if !fail.Decisive() || fail.Passed {
		t.Fatalf("failing tests should not pass: %+v", fail)
	}
	// No runner → not decisive → LLM judge.
	none := r.Run(Input{Archetype: project.ArchetypeCode})
	if none.Decisive() {
		t.Fatalf("no test run should be non-decisive: %+v", none)
	}
}

func TestLintVerifier(t *testing.T) {
	r := Default()
	got := r.Run(Input{Archetype: project.ArchetypeCode, LintRan: true, LintPassed: false})
	if !got.Decisive() || got.Passed {
		t.Fatalf("failing lint should not pass: %+v", got)
	}
}

func TestStructureWordLimit(t *testing.T) {
	r := Default()
	in := Input{Archetype: project.ArchetypeText,
		Criteria: []string{"under 5 words"},
		Content:  "too many words in this short bit"}
	got := r.Run(in)
	if !got.Decisive() || got.Passed {
		t.Fatalf("word limit should fail: %+v", got)
	}
	in.Content = "four short words here"
	if got := r.Run(in); !got.Passed {
		t.Fatalf("word limit should pass: %+v", got)
	}
}

func TestStructureParagraphsAndSections(t *testing.T) {
	r := Default()
	in := Input{Archetype: project.ArchetypeDocument,
		Criteria: []string{"exactly three paragraphs", "sections named Install, Usage, and License"},
		Content:  "# Doc\n\nInstall here.\n\n## Usage\n\nRun it.\n\n## License\n\nMIT",
	}
	got := r.Run(in)
	// Two paragraphs of body + heading-separated content; the section check
	// must pass and the paragraph count is informational.
	if len(got.Reasons) == 0 {
		t.Fatalf("expected at least a structural reason: %+v", got)
	}
	// Missing a section fails.
	in.Content = "# Doc\n\n## Install\n\nx\n\n## Usage\n\ny"
	got = r.Run(in)
	if got.Passed {
		t.Fatalf("missing License section should fail: %+v", got)
	}
}

func TestStructureNoConstraintIsNotDecisive(t *testing.T) {
	r := Default()
	got := r.Run(Input{Archetype: project.ArchetypeText,
		Criteria: []string{"argue persuasively"}, Content: "anything"})
	if got.Decisive() {
		t.Fatalf("unparseable criteria should be non-decisive: %+v", got)
	}
}

func TestStructureMinItems(t *testing.T) {
	r := Default()
	in := Input{Archetype: project.ArchetypeDocument,
		Criteria: []string{"at least two risks named"},
		Content:  "## Risks\n\n- one risk\n- two risk",
	}
	if got := r.Run(in); !got.Passed {
		t.Fatalf("two bullet risks should pass: %+v", got)
	}
	in.Content = "## Risks\n\n- only one"
	if got := r.Run(in); got.Passed {
		t.Fatalf("one risk should fail: %+v", got)
	}
}

func TestFindPlaceholder(t *testing.T) {
	if findPlaceholder("all clean") != "" {
		t.Fatal("false positive")
	}
	if findPlaceholder("TODO: finish") != "todo" {
		t.Fatal("missed TODO")
	}
}

// TestHardVsAdvisory pins the F4-T4 correction from the micro run: objective
// code verifiers are hard (may decide), while the free-text structural
// parser is advisory (informs but never overrides the LLM judge).
func TestHardVsAdvisory(t *testing.T) {
	r := Default()
	code := r.Run(Input{Archetype: project.ArchetypeCode, TestRan: true, TestsPassed: false})
	if !code.HardDecisive() || code.HardPassed {
		t.Fatalf("failing code tests should be hard-decisive: %+v", code)
	}
	text := r.Run(Input{Archetype: project.ArchetypeText,
		Criteria: []string{"exactly three paragraphs"}, Content: "a\n\nb"})
	if text.HardDecisive() {
		t.Fatalf("structure should be advisory, got %+v", text)
	}
	if !text.Decisive() {
		t.Fatalf("structure should still be decisive (advisory): %+v", text)
	}
}

// TestCountParagraphsIgnoresHeadings proves a document title is not counted
// as a paragraph (the micro run's false-negative source).
func TestCountParagraphsIgnoresHeadings(t *testing.T) {
	if n := countParagraphs("# Title\n\nOne.\n\nTwo.\n\nThree."); n != 3 {
		t.Errorf("countParagraphs = %d, want 3 (title is not a paragraph)", n)
	}
	if n := countParagraphs("**Title**\n\nOne.\n\nTwo."); n != 2 {
		t.Errorf("countParagraphs = %d, want 2 (bold heading is not a paragraph)", n)
	}
}
