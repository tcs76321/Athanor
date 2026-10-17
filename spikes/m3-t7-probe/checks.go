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
	"encoding/json"
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
	"required_substrings_source", "preserve_entities_from_fixture",
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
	if v, ok := checks["must_contain"]; ok {
		low := strings.ToLower(content)
		var missing []string
		for _, s := range toStrings(v) {
			if !strings.Contains(low, strings.ToLower(s)) {
				missing = append(missing, s)
			}
		}
		add("must_contain", len(missing) == 0, false, "missing: "+strings.Join(missing, ", "))
	}
	if v, ok := checks["json_schema"]; ok {
		path, _ := v.(string)
		if err := checkJSONSchema(path, content); err != nil {
			add("json_schema", false, false, err.Error())
		} else {
			add("json_schema", true, false, "")
		}
	}

	// JSON-array checks for data tasks: the schema validates shape; these pin
	// count, uniqueness, ordering, and field formats the schema subset cannot.
	if v, ok := checks["array_length"]; ok {
		arr, valid := jsonArray(content)
		add("array_length", valid && len(arr) == toInt(v), false,
			fmt.Sprintf("length=%d want=%d", len(arr), toInt(v)))
	}
	if v, ok := checks["array_unique_by"]; ok {
		key, _ := v.(string)
		arr, valid := jsonArray(content)
		seen, unique := map[string]bool{}, valid
		for _, m := range arr {
			k := fmt.Sprint(m[key])
			if seen[k] {
				unique = false
				break
			}
			seen[k] = true
		}
		add("array_unique_by", valid && unique, false, "key="+key)
	}
	if v, ok := checks["array_sorted_by"]; ok {
		key, _ := v.(string)
		arr, valid := jsonArray(content)
		sorted := valid
		for i := 1; valid && i < len(arr); i++ {
			if !jsonLessOrEqual(arr[i-1][key], arr[i][key]) {
				sorted = false
			}
		}
		add("array_sorted_by", sorted, false, "key="+key)
	}
	if v, ok := checks["json_field_matches"]; ok {
		patterns, _ := v.(map[string]any)
		arr, valid := jsonArray(content)
		matched := valid
		for _, m := range arr {
			for field, pv := range patterns {
				pat, _ := pv.(string)
				re, err := regexp.Compile(pat)
				if err != nil || !re.MatchString(fmt.Sprint(m[field])) {
					matched = false
					break
				}
			}
			if !matched {
				break
			}
		}
		add("json_field_matches", matched, false, "")
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

// checksToExecution translates a bench task's `checks:` map into the engine's
// declared-checks execution field (ADR-0070). It carries only the deterministic
// keys the engine understands; an advisory or fixture-dependent check is left
// to the probe's independent offline scorer (runChecks). count_alternatives_min
// and count_risks_min become a general min_items list. Returns nil when nothing
// carries, so the caller sends no execution block.
func checksToExecution(checks map[string]any) map[string]any {
	if len(checks) == 0 {
		return nil
	}
	out := map[string]any{}
	for _, k := range []string{
		"required_sections", "strict_section_order", "must_contain", "forbidden_phrases",
		"exact_paragraphs", "max_words", "min_words",
	} {
		if v, ok := checks[k]; ok {
			out[k] = v
		}
	}
	if v, ok := checks["no_headings"]; ok && toBool(v) {
		out["no_headings"] = true
	}
	var items []map[string]any
	if v, ok := checks["count_alternatives_min"]; ok {
		items = append(items, map[string]any{"noun": "alternatives", "min": toInt(v)})
	}
	if v, ok := checks["count_risks_min"]; ok {
		items = append(items, map[string]any{"noun": "risks", "min": toInt(v)})
	}
	if len(items) > 0 {
		out["min_items"] = items
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

// jsonArray parses content as a JSON array of objects. ok is false when the
// content is not such an array, so an array check fails rather than panicking.
func jsonArray(content string) ([]map[string]any, bool) {
	var v any
	if json.Unmarshal([]byte(content), &v) != nil {
		return nil, false
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, false
		}
		out = append(out, m)
	}
	return out, true
}

// jsonLessOrEqual compares two JSON scalar values (number or string) for the
// array_sorted_by check. Mixed or unknown types are not comparable.
func jsonLessOrEqual(a, b any) bool {
	switch x := a.(type) {
	case float64:
		y, ok := b.(float64)
		return ok && x <= y
	case string:
		y, ok := b.(string)
		return ok && x <= y
	}
	return false
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
