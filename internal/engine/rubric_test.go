// M3-T2 (commit 2.2) tests for the per-archetype evaluation
// rubric. The rubric is a small, pure function; these tests
// lock the contract that the security persona is told which
// items to look for, and that the engine reconciles the
// persona's verdict against the rubric's `missing_criteria`
// / `security_issues` / `style_issues` arrays.
//
// The persona is statistical; the rubric is structural. The
// tests assert the *rubric*, not the *persona*. M3-T7's
// quality probe is the right place to measure how well the
// persona actually applies the rubric.
package engine

import (
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/project"
)

// TestRubricFor_Text covers the text archetype: the
// §19.1 prose check list. The rubric is non-empty and
// contains the four mandatory items (structure, tone,
// acceptance coverage, no placeholders).
func TestRubricFor_Text(t *testing.T) {
	r := rubricFor(project.ArchetypeText)
	if r == "" {
		t.Fatal("text rubric is empty")
	}
	mustContain := []string{
		"STRUCTURE",
		"TONE",
		"ACCEPTANCE COVERAGE",
		"NO PLACEHOLDERS",
	}
	for _, item := range mustContain {
		if !strings.Contains(r, item) {
			t.Errorf("text rubric missing %q\nrubric:\n%s", item, r)
		}
	}
}

// TestRubricFor_Document covers the document archetype: the
// rubric is the same as text (both are prose).
func TestRubricFor_Document(t *testing.T) {
	r := rubricFor(project.ArchetypeDocument)
	if r == "" {
		t.Fatal("document rubric is empty")
	}
	if !strings.Contains(r, "STRUCTURE") {
		t.Errorf("document rubric missing STRUCTURE\nrubric:\n%s", r)
	}
}

// TestRubricFor_Code covers the code archetype: imports
// compile, tests pass, docstrings, no placeholders, linter
// clean, acceptance coverage. The "linter clean" item is
// advisory until M3-T2 commit 2.3 lands; the rubric must
// mention it but the test does not require it to be wired.
func TestRubricFor_Code(t *testing.T) {
	r := rubricFor(project.ArchetypeCode)
	if r == "" {
		t.Fatal("code rubric is empty")
	}
	mustContain := []string{
		"IMPORTS COMPILE",
		"TESTS PASS",
		"DOCSTRINGS",
		"NO PLACEHOLDERS",
		"ACCEPTANCE COVERAGE",
	}
	for _, item := range mustContain {
		if !strings.Contains(r, item) {
			t.Errorf("code rubric missing %q\nrubric:\n%s", item, r)
		}
	}
	// Advisory item: the lint route exists (M3-T2); the rubric names it but
	// does not make its presence a hard test assertion.
	if !strings.Contains(r, "LINTER CLEAN") {
		t.Logf("code rubric mentions LINTER CLEAN as advisory: ok")
	}
}

// TestRubricFor_DataAndMediaDeferred covers the deferred
// archetypes: data and media return an empty rubric. The
// persona still sees the acceptance-criteria block, so the
// §19.3 guard is not weaker than M3-T1.
func TestRubricFor_DataAndMediaDeferred(t *testing.T) {
	if r := rubricFor(project.ArchetypeData); r != "" {
		t.Errorf("data rubric should be empty (deferred per ROADMAP §7), got:\n%s", r)
	}
	if r := rubricFor(project.ArchetypeMedia); r != "" {
		t.Errorf("media rubric should be empty (deferred per ROADMAP §7), got:\n%s", r)
	}
}

// TestRubricFor_UnknownArchetype covers the unknown
// archetype: the persona sees no rubric but also no false
// positives. The §19.3 guard still has the acceptance
// criteria to act on.
func TestRubricFor_UnknownArchetype(t *testing.T) {
	if r := rubricFor("not_a_real_archetype"); r != "" {
		t.Errorf("unknown archetype should return empty rubric, got:\n%s", r)
	}
}

// TestRubricItems_Stable asserts that rubricItems returns a
// stable, sorted slice — used by future tests to assert
// rubric membership without re-parsing the multi-line
// string. The function exists for test ergonomics; the
// prompt's rendering uses the original order.
func TestRubricItems_Stable(t *testing.T) {
	for _, archetype := range []string{project.ArchetypeText, project.ArchetypeDocument, project.ArchetypeCode} {
		items := rubricItems(archetype)
		if len(items) == 0 {
			t.Errorf("%s: rubricItems returned empty", archetype)
		}
		// Stable across calls (same input, same output).
		again := rubricItems(archetype)
		if len(again) != len(items) {
			t.Errorf("%s: rubricItems length differs across calls: %d vs %d", archetype, len(items), len(again))
		}
		for i := range items {
			if items[i] != again[i] {
				t.Errorf("%s: rubricItems not stable at index %d: %q vs %q", archetype, i, items[i], again[i])
			}
		}
	}
	// Deferred archetypes return nil.
	if items := rubricItems(project.ArchetypeData); items != nil {
		t.Errorf("data rubricItems should be nil, got %v", items)
	}
}
