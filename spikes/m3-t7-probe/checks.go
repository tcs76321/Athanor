// Deterministic check engine for the bench corpus (M8-T18, ADR-0061). The
// non-code tasks (document/text/data/adversarial) declare a `checks:` map;
// this file turns each check into a pass/fail where a parser can decide, and
// marks the rest advisory (reported, never gating) rather than pretending.
//
// It lives in the probe because only the probe consumes it: the daemon's
// acceptance path is the engine's verifiers (internal/verify); this is the
// benchmark's independent, deterministic scorer.
package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// checkResult is one check outcome. Advisory results are reported but never
// gate acceptance (the corpus README's "some checks are advisory").
type checkResult struct {
	Kind     string
	Pass     bool
	Advisory bool
	Detail   string
}

// advisoryChecks are the check kinds that are semantic and cannot be decided
// by a parser. They are surfaced (not silently dropped) so the report is
// honest, but they do not gate a deterministic pass.
var advisoryChecks = []string{
	"json_schema", "required_substrings_source", "preserve_entities_from_fixture",
	"every_item_once", "must_flag_contradiction", "must_not_fabricate",
	"no_fabricated_commands", "max_new_claims", "exactly_one_cta",
	"required_citations_from_fixture",
}

// runChecks applies a task's `checks` map to artifact content. It returns
// whether every *deterministic* check passed, and the per-check results in a
// stable order. Pure.
func runChecks(checks map[string]any, content string) (bool, []checkResult) {
	if len(checks) == 0 {
		return true, nil
	}
	var results []checkResult
	add := func(kind string, pass bool, advisory bool, detail string) {
		results = append(results, checkResult{Kind: kind, Pass: pass, Advisory: advisory, Detail: detail})
	}

	if v, ok := checks["required_sections"]; ok {
		have := sectionNames(content)
		var missing []string
		for _, n := range toStrings(v) {
			if !have[normalizeName(n)] {
				missing = append(missing, n)
			}
		}
		add("required_sections", len(missing) == 0, false, "missing: "+strings.Join(missing, ", "))
	}
	if v, ok := checks["strict_section_order"]; ok {
		want := toStrings(v)
		got := orderedSections(content)
		add("strict_section_order", isSubsequence(want, got), false, fmt.Sprintf("order=%v want=%v", got, want))
	}
	if v, ok := checks["exact_paragraphs"]; ok {
		n := toInt(v)
		got := countParagraphs(content)
		add("exact_paragraphs", got == n, false, fmt.Sprintf("paragraphs=%d want=%d", got, n))
	}
	if v, ok := checks["max_words"]; ok {
		n := toInt(v)
		got := len(strings.Fields(content))
		add("max_words", got <= n, false, fmt.Sprintf("words=%d max=%d", got, n))
	}
	if v, ok := checks["forbidden_phrases"]; ok {
		low := strings.ToLower(content)
		var hit []string
		for _, p := range toStrings(v) {
			if strings.Contains(low, strings.ToLower(p)) {
				hit = append(hit, p)
			}
		}
		add("forbidden_phrases", len(hit) == 0, false, "found: "+strings.Join(hit, ", "))
	}
	if v, ok := checks["no_headings"]; ok && toBool(v) {
		add("no_headings", !hasHeading(content), false, "")
	}
	if v, ok := checks["count_alternatives_min"]; ok {
		add("count_alternatives_min", countBulletsInSection(content, "alternatives") >= toInt(v), false, "")
	}
	if v, ok := checks["count_risks_min"]; ok {
		add("count_risks_min", countBulletsInSection(content, "risks") >= toInt(v), false, "")
	}
	if v, ok := checks["min_distinct_arguments"]; ok {
		add("min_distinct_arguments", countParagraphs(content) >= toInt(v), false, "")
	}

	for _, key := range advisoryChecks {
		if _, ok := checks[key]; ok {
			add(key, false, true, "advisory: not deterministically checked")
		}
	}

	gating := true
	for _, r := range results {
		if !r.Advisory && !r.Pass {
			gating = false
		}
	}
	return gating, results
}

var benchHeadingRe = regexp.MustCompile(`(?m)^[ \t]*#{1,6}[ \t]+(.+?)[ \t]*$`)

func sectionNames(content string) map[string]bool {
	out := map[string]bool{}
	for _, m := range benchHeadingRe.FindAllStringSubmatch(content, -1) {
		out[normalizeName(m[1])] = true
	}
	return out
}

func orderedSections(content string) []string {
	var out []string
	for _, m := range benchHeadingRe.FindAllStringSubmatch(content, -1) {
		out = append(out, normalizeName(m[1]))
	}
	return out
}

func hasHeading(content string) bool { return benchHeadingRe.MatchString(content) }

func normalizeName(s string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(s), "#*:`\".,"))
}

// isSubsequence reports whether every wanted section appears in order within
// the document's headings.
func isSubsequence(want, have []string) bool {
	i := 0
	for _, h := range have {
		if i < len(want) && h == normalizeName(want[i]) {
			i++
		}
	}
	return i == len(want)
}

// countParagraphs counts non-empty prose blocks separated by blank lines; a
// heading-only or fence-only block is not a paragraph.
func countParagraphs(content string) int {
	blocks := regexp.MustCompile(`\n[ \t]*\n`).Split(content, -1)
	n := 0
	for _, b := range blocks {
		for _, ln := range strings.Split(b, "\n") {
			t := strings.TrimSpace(ln)
			if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "```") {
				continue
			}
			n++
			break
		}
	}
	return n
}

// countBulletsInSection counts list items under the first heading whose
// normalized name contains `name`, up to the next heading.
func countBulletsInSection(content, name string) int {
	n := 0
	in := false
	for _, ln := range strings.Split(content, "\n") {
		if m := benchHeadingRe.FindStringSubmatch(ln); m != nil {
			in = strings.Contains(normalizeName(m[1]), name)
			continue
		}
		if !in {
			continue
		}
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
			n++
			continue
		}
		if i := strings.IndexByte(t, '.'); i > 0 && i <= 3 {
			if _, err := strconv.Atoi(t[:i]); err == nil {
				n++
			}
		}
	}
	return n
}

func toStrings(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	default:
		return nil
	}
}

func toInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	default:
		return -1
	}
}

func toBool(v any) bool {
	b, _ := v.(bool)
	return b
}
