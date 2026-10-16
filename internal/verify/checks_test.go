package verify

import (
	"testing"

	"github.com/tcs76321/athanor/internal/project"
)

// TestChecksVerifier_NoChecksIsUnapplied: a nil or zero declared set carries no
// signal, so acceptance falls through as before.
func TestChecksVerifier_NoChecksIsUnapplied(t *testing.T) {
	r := Default()
	if res := r.Run(Input{Archetype: project.ArchetypeDocument, Content: "anything"}); res.Applied != 0 {
		t.Errorf("Applied = %d with no declared checks, want 0", res.Applied)
	}
	empty := &project.ExecutionChecks{}
	if res := r.Run(Input{Archetype: project.ArchetypeDocument, Content: "x", Checks: empty}); res.Applied != 0 {
		t.Errorf("Applied = %d with an empty declared set, want 0", res.Applied)
	}
}

// TestChecksVerifier_PassAndFail exercises each declared check kind in a single
// document that satisfies all of them, then breaks them one at a time.
func TestChecksVerifier_PassAndFail(t *testing.T) {
	good := "A short brief.\n\n" +
		"## Context\nHere.\n\n" +
		"## Decision\nWe choose X.\n\n" +
		"## Consequences\nRisks:\n- power\n- clock drift\n"
	checks := &project.ExecutionChecks{
		RequiredSections:   []string{"Context", "Decision", "Consequences"},
		StrictSectionOrder: []string{"Context", "Decision", "Consequences"},
		MustContain:        []string{"choose"},
		ForbiddenPhrases:   []string{"click here"},
		MinItems:           []project.ItemCount{{Noun: "risks", Min: 2}},
	}
	r := Default()
	res := r.Run(Input{Archetype: project.ArchetypeDocument, Content: good, Checks: checks})
	if !res.HardDecisive() || !res.HardPassed {
		t.Fatalf("good doc: HardDecisive=%v HardPassed=%v reasons=%v", res.HardDecisive(), res.HardPassed, res.Reasons)
	}

	// Missing a required section → hard fail.
	bad := "## Context\nonly context\n"
	res = r.Run(Input{Archetype: project.ArchetypeDocument, Content: bad, Checks: checks})
	if !res.HardDecisive() || res.HardPassed {
		t.Fatalf("missing section: HardDecisive=%v HardPassed=%v (want a hard fail)", res.HardDecisive(), res.HardPassed)
	}
}

func TestChecksVerifier_ParagraphsWords(t *testing.T) {
	r := Default()
	// Exactly four paragraphs.
	para4 := "one\n\ntwo\n\nthree\n\nfour\n"
	cs := &project.ExecutionChecks{ExactParagraphs: 4}
	if res := r.Run(Input{Archetype: project.ArchetypeText, Content: para4, Checks: cs}); !res.HardPassed {
		t.Errorf("exact_paragraphs: HardPassed=false, want true (reasons=%v)", res.Reasons)
	}
	if res := r.Run(Input{Archetype: project.ArchetypeText, Content: "one\n\ntwo\n", Checks: cs}); res.HardPassed {
		t.Errorf("exact_paragraphs: 2 paragraphs must fail")
	}

	words := &project.ExecutionChecks{MaxWords: 3}
	if res := r.Run(Input{Archetype: project.ArchetypeText, Content: "a b c", Checks: words}); !res.HardPassed {
		t.Errorf("max_words boundary: 3 words <= 3 must pass")
	}
	if res := r.Run(Input{Archetype: project.ArchetypeText, Content: "a b c d", Checks: words}); res.HardPassed {
		t.Errorf("max_words: 4 words > 3 must fail")
	}
	minw := &project.ExecutionChecks{MinWords: 3}
	if res := r.Run(Input{Archetype: project.ArchetypeText, Content: "a b", Checks: minw}); res.HardPassed {
		t.Errorf("min_words: 2 words < 3 must fail")
	}
}

func TestChecksVerifier_PhrasesAndHeadings(t *testing.T) {
	r := Default()
	forb := &project.ExecutionChecks{ForbiddenPhrases: []string{"click here"}}
	if res := r.Run(Input{Archetype: project.ArchetypeText, Content: "Please CLICK HERE now", Checks: forb}); res.HardPassed {
		t.Errorf("forbidden_phrases must be case-insensitive and fail")
	}
	if res := r.Run(Input{Archetype: project.ArchetypeText, Content: "welcome aboard", Checks: forb}); !res.HardPassed {
		t.Errorf("no forbidden phrase must pass")
	}
	must := &project.ExecutionChecks{MustContain: []string{"S1", "S2"}}
	if res := r.Run(Input{Archetype: project.ArchetypeDocument, Content: "see s1 only", Checks: must}); res.HardPassed {
		t.Errorf("must_contain missing S2 must fail")
	}
	nh := &project.ExecutionChecks{NoHeadings: true}
	if res := r.Run(Input{Archetype: project.ArchetypeText, Content: "# Title\nbody", Checks: nh}); res.HardPassed {
		t.Errorf("no_headings with a heading must fail")
	}
	if res := r.Run(Input{Archetype: project.ArchetypeText, Content: "just body", Checks: nh}); !res.HardPassed {
		t.Errorf("no_headings without a heading must pass")
	}
	npl := &project.ExecutionChecks{NoPlaceholders: true}
	if res := r.Run(Input{Archetype: project.ArchetypeText, Content: "TODO: finish", Checks: npl}); res.HardPassed {
		t.Errorf("no_placeholders with a TODO must fail")
	}
}

// TestChecksVerifier_MinItemsFallsBackToBody: a count whose section heading is
// absent is counted over the whole body (the corpus's risks-inside-Consequences
// case).
func TestChecksVerifier_MinItemsFallsBackToBody(t *testing.T) {
	r := Default()
	body := "## Consequences\n- power\n- drift\n- thermal\n"
	cs := &project.ExecutionChecks{MinItems: []project.ItemCount{{Noun: "risks", Min: 2}}}
	if res := r.Run(Input{Archetype: project.ArchetypeDocument, Content: body, Checks: cs}); !res.HardPassed {
		t.Errorf("min_items body fallback: want pass, reasons=%v", res.Reasons)
	}
}

// TestChecksVerifier_ScopedToTextAndDocument: code/data candidates are not
// gated by declared checks (they have their own verifiers).
func TestChecksVerifier_ScopedToTextAndDocument(t *testing.T) {
	r := Default()
	cs := &project.ExecutionChecks{MaxWords: 1}
	if res := r.Run(Input{Archetype: project.ArchetypeCode, Content: "many words here", Checks: cs}); res.Applied != 0 {
		t.Errorf("code archetype: checks verifier applied = %d, want 0", res.Applied)
	}
}
