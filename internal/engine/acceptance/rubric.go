// Per-capability evaluation rubric (§19.1).
//
// The rubric is the structured checklist the security persona is told to apply
// when scoring a candidate. Each item is a phrase the persona may echo into one
// of the verdict's missing_criteria / security_issues / style_issues arrays, so
// the §19.3 deterministic guard can read the verdict and act on the same data
// the persona was told to look for.
//
// The rubric is per capability because the §19.1 checks differ: a text artifact
// doesn't need tests; a code artifact doesn't need paragraph structure.
package acceptance

import (
	"sort"
	"strings"

	"github.com/tcs76321/athanor/internal/project"
)

// Rubric returns the per-capability rubric text. The returned string is
// appended to the security-persona prompt as a `## RUBRIC` block. Empty means
// "no rubric for this capability" — the prompt assembler drops the section.
func Rubric(capability string) string {
	switch capability {
	case project.CapabilityText, project.CapabilityDocument:
		return textDocumentRubric()
	case project.CapabilityCode:
		return codeRubric()
	case project.CapabilityData:
		return dataRubric()
	case project.CapabilityMedia:
		return mediaRubric()
	default:
		// Unknown capability: no rubric, but the persona still sees the
		// acceptance-criteria block, so the §19.3 guard is not weaker.
		return ""
	}
}

// textDocumentRubric is the §19.1 check list for text and document
// capabilities. Both are prose; the checks apply to structure and tone, not
// executable semantics.
func textDocumentRubric() string {
	return strings.Join([]string{
		"STRUCTURE: does the artifact have a clear opening, body, and close?",
		"TONE: is the register consistent (no shifts between formal and casual without intent)?",
		"ACCEPTANCE COVERAGE: does every acceptance criterion appear in the artifact, in a way a reader can identify?",
		"CONCISENESS: are there sentences that repeat a point already made in the artifact?",
		"NO PLACEHOLDERS: are there any TODO, FIXME, lorem ipsum, or ellipses-as-content?",
	}, "\n")
}

// codeRubric is the §19.1 check list for code capabilities. The linter and
// tests items are hints to the persona, not hard rules: the deterministic
// verifier is authoritative where it applies.
func codeRubric() string {
	return strings.Join([]string{
		"DEPENDENCIES RESOLVE: do all imports and references resolve to a known module or package? (Audit by reading; the Job Pod's execute_code is the runtime check.)",
		"TESTS PASS: did the project's configured test command exit 0 in the Job Pod? If `tests_pass` is unknown because the runner was not wired, the verdict's `summary` should mention `runner_not_wired`.",
		"DOCUMENTATION: do public functions, types, and modules have the documentation the project's language convention expects?",
		"NO PLACEHOLDERS: are there any TODO, FIXME, or `pass`-as-implementation?",
		"LINTER CLEAN: did the project's configured linter exit 0 in the Job Pod? (A job whose envelope does not grant `lint` skips this item.)",
		"ACCEPTANCE COVERAGE: does every acceptance criterion appear in the artifact, in a way a reader can identify?",
	}, "\n")
}

// dataRubric applies to data capabilities. It asks about schema, provenance,
// completeness, and determinism rather than prose or code shape.
func dataRubric() string {
	return strings.Join([]string{
		"SCHEMA: does the output validate against the declared schema, or against an explicit and documented shape when no schema is declared?",
		"PROVENANCE: is every value traceable to a declared source, or clearly marked as derived?",
		"COMPLETENESS: are required fields present, and are missing values explicit rather than silently dropped?",
		"NO FABRICATION: are there invented records, totals, timestamps, or citations?",
		"DETERMINISM: can the same input produce the same output?",
		"ACCEPTANCE COVERAGE: does every acceptance criterion appear in the artifact, in a way a reader can identify?",
	}, "\n")
}

// mediaRubric applies to media capabilities. It checks the requested format and
// legibility without assuming a particular medium or toolchain.
func mediaRubric() string {
	return strings.Join([]string{
		"FORMAT: does the artifact match the requested dimensions, duration, encoding, or file type?",
		"CLARITY: is the intended subject or message legible without extra explanation?",
		"SPEC ADHERENCE: does it follow the brief's style, length, and required elements?",
		"ACCESSIBILITY: where applicable, are alt text, captions, contrast, or equivalent affordances present?",
		"NO PLACEHOLDERS: is any part a stand-in, missing asset, or unfinished draft?",
		"ACCEPTANCE COVERAGE: does every acceptance criterion appear in the artifact, in a way a reader can identify?",
	}, "\n")
}

// rubricItems returns the rubric's individual items as a sorted slice. Used by
// tests to assert membership without re-parsing the multi-line string.
func rubricItems(capability string) []string {
	text := Rubric(capability)
	if text == "" {
		return nil
	}
	raw := strings.Split(text, "\n")
	out := make([]string, 0, len(raw))
	out = append(out, raw...)
	sort.Strings(out)
	return out
}
