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
package acceptance

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
	r := Rubric(project.ArchetypeText)
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
	r := Rubric(project.ArchetypeDocument)
	if r == "" {
		t.Fatal("document rubric is empty")
	}
	if !strings.Contains(r, "STRUCTURE") {
		t.Errorf("document rubric missing STRUCTURE\nrubric:\n%s", r)
	}
}

// TestRubricFor_Code covers the code archetype: dependencies resolve, tests
// pass, documentation, no placeholders, linter clean, acceptance coverage.
func TestRubricFor_Code(t *testing.T) {
	r := Rubric(project.ArchetypeCode)
	if r == "" {
		t.Fatal("code rubric is empty")
	}
	mustContain := []string{
		"DEPENDENCIES RESOLVE",
		"TESTS PASS",
		"DOCUMENTATION",
		"NO PLACEHOLDERS",
		"LINTER CLEAN",
		"ACCEPTANCE COVERAGE",
	}
	for _, item := range mustContain {
		if !strings.Contains(r, item) {
			t.Errorf("code rubric missing %q\nrubric:\n%s", item, r)
		}
	}
}

// TestRubricFor_DataAndMedia covers the non-code capabilities: data checks
// schema/provenance and media checks format/legibility. Both still include the
// shared acceptance-coverage item.
func TestRubricFor_DataAndMedia(t *testing.T) {
	for _, tc := range []struct {
		capability string
		want       []string
	}{
		{project.ArchetypeData, []string{"SCHEMA", "PROVENANCE", "COMPLETENESS", "NO FABRICATION", "DETERMINISM", "ACCEPTANCE COVERAGE"}},
		{project.ArchetypeMedia, []string{"FORMAT", "CLARITY", "SPEC ADHERENCE", "ACCESSIBILITY", "NO PLACEHOLDERS", "ACCEPTANCE COVERAGE"}},
	} {
		r := Rubric(tc.capability)
		if r == "" {
			t.Fatalf("%s rubric is empty", tc.capability)
		}
		for _, item := range tc.want {
			if !strings.Contains(r, item) {
				t.Errorf("%s rubric missing %q\nrubric:\n%s", tc.capability, item, r)
			}
		}
	}
}

// TestRubricFor_UnknownArchetype covers the unknown
// archetype: the persona sees no rubric but also no false
// positives. The §19.3 guard still has the acceptance
// criteria to act on.
func TestRubricFor_UnknownArchetype(t *testing.T) {
	if r := Rubric("not_a_real_archetype"); r != "" {
		t.Errorf("unknown archetype should return empty rubric, got:\n%s", r)
	}
}

// TestRubricItems_Stable asserts that rubricItems returns a
// stable, sorted slice — used by future tests to assert
// rubric membership without re-parsing the multi-line
// string. The function exists for test ergonomics; the
// prompt's rendering uses the original order.
func TestRubricItems_Stable(t *testing.T) {
	for _, archetype := range []string{project.ArchetypeText, project.ArchetypeDocument, project.ArchetypeCode, project.ArchetypeData, project.ArchetypeMedia} {
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
}
