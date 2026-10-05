// M3-T2 (commit 2.2) per-archetype evaluation rubric (§19.1).
//
// The rubric is the structured checklist the security persona is
// told to apply when scoring a candidate. Each item is a
// phrase the persona may echo into one of the verdict's
// `missing_criteria` / `security_issues` / `style_issues`
// arrays, so the §19.3 deterministic guard can read the
// verdict and act on the same data the persona was told to
// look for.
//
// The rubric is per archetype because the §19.1 checks
// differ: a `text` artifact doesn't need pytest; a `code`
// artifact doesn't need paragraph structure. The closed set
// is the five §6.2 archetypes; `data` and `media` are
// deferred (ROADMAP §7) and return empty, which causes the
// prompt's section-add helper to drop the rubric section
// entirely.
package engine

import (
	"sort"
	"strings"

	"github.com/tcs76321/athanor/internal/project"
)

// rubricFor returns the per-archetype rubric text. The
// returned string is appended to the security-persona prompt
// as a `## RUBRIC` block. Empty return means "no rubric for
// this archetype" — the prompt assembler drops the section.
func rubricFor(archetype string) string {
	switch archetype {
	case project.ArchetypeText, project.ArchetypeDocument:
		return textDocumentRubric()
	case project.ArchetypeCode:
		return codeRubric()
	case project.ArchetypeData, project.ArchetypeMedia:
		// Deferred per ROADMAP §7. M4/M5 may add rubrics.
		return ""
	default:
		// Unknown archetype: no rubric, but the persona
		// still sees the acceptance-criteria block, so the
		// §19.3 guard is not weaker than M3-T1.
		return ""
	}
}

// textDocumentRubric is the §19.1 check list for `text` and
// `document` archetypes. Both are prose; the checks apply to
// the artifact's structure and tone, not to executable
// semantics.
func textDocumentRubric() string {
	return strings.Join([]string{
		"STRUCTURE: does the artifact have a clear opening, body, and close?",
		"TONE: is the register consistent (no shifts between formal and casual without intent)?",
		"ACCEPTANCE COVERAGE: does every acceptance criterion appear in the artifact, in a way a reader can identify?",
		"CONCISENESS: are there sentences that repeat a point already made in the artifact?",
		"NO PLACEHOLDERS: are there any TODO, FIXME, lorem ipsum, or ellipses-as-content?",
	}, "\n")
}

// codeRubric is the §19.1 check list for `code` archetypes.
// The `linter clean` item is a hint to the persona, not a
// hard rule: M3-T2 commit 2.3 adds the actual `lint` route;
// until then the persona is told that the item is advisory
// (it may skip it if no linter is wired). The same caveat
// applies to `tests_run` — if `tests_pass` is unknown because
// the runner was not wired (M1 dev mode), the persona echoes
// a `runner_not_wired` reason in the verdict's summary.
func codeRubric() string {
	return strings.Join([]string{
		"IMPORTS COMPILE: do all top-level imports resolve to a known module? (Audit by reading; the Job Pod's execute_code is the runtime check.)",
		"TESTS PASS: did the test command (pytest -q) exit 0 in the Job Pod? If `tests_pass` is unknown because the runner was not wired, the verdict's `summary` should mention `runner_not_wired`.",
		"DOCSTRINGS: does every public function have a docstring? (Use the §11 prompt's `pure stdlib; docstrings on every public function; a usage example` for code-archetype goals as the spec.)",
		"NO PLACEHOLDERS: are there any TODO, FIXME, or `pass`-as-implementation?",
		"LINTER CLEAN: did `ruff check .` (or the configured linter) exit 0 in the Job Pod? (A job whose envelope does not grant `lint` skips this item.)",
		"ACCEPTANCE COVERAGE: does every acceptance criterion appear in the artifact, in a way a reader can identify?",
	}, "\n")
}

// rubricItems returns the rubric's individual items as a
// sorted slice. Used by tests to assert rubric membership
// without re-parsing the multi-line string. Sorted so the
// output is stable for assertions; the prompt's rendering
// uses the original order.
func rubricItems(archetype string) []string {
	text := rubricFor(archetype)
	if text == "" {
		return nil
	}
	raw := strings.Split(text, "\n")
	out := make([]string, 0, len(raw))
	out = append(out, raw...)
	sort.Strings(out)
	return out
}
