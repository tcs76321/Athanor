package verify

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/tcs76321/athanor/internal/project"
)

// checksVerifier applies the operator-declared ExecutionChecks to a text or
// document candidate (M9, ADR-0070). It is the deterministic counterpart to
// the free-text criteria that structureVerifier parses by heuristic: the
// operator states the constraint explicitly and the verifier decides it, so
// acceptance no longer depends on whether the model happened to phrase a
// criterion in a way the parser recognizes. It is a HARD verifier — a declared
// check is authoritative, like a code test run or a JSON Schema.
//
// The vocabulary and matching rules mirror eval/bench's `checks:` block
// (tools/bench/checks.go), which was the benchmark's offline scorer.
// Promoting it here lets the engine gate at runtime instead of scoring after
// the fact. The probe keeps its own copy as the *independent* benchmark
// scorer, so the benchmark never trusts the engine's own verifier.
type checksVerifier struct{}

func (checksVerifier) Archetypes() []string {
	return []string{project.ArchetypeText, project.ArchetypeDocument}
}
func (checksVerifier) Name() string { return "checks" }

func (checksVerifier) Verify(in Input) Verdict {
	if in.Checks == nil || in.Checks.IsZero() {
		return Verdict{Verifier: "checks", Applied: false, Hard: true}
	}
	c := in.Checks
	v := Verdict{Verifier: "checks", Applied: true, Hard: true}
	passed, total := 0, 0
	check := func(ok bool, reason string) {
		total++
		if ok {
			passed++
		} else {
			v.Reasons = append(v.Reasons, reason)
		}
	}

	if len(c.RequiredSections) > 0 {
		have := checkSectionNames(in.Content)
		var missing []string
		for _, n := range c.RequiredSections {
			if !have[normalizeCheckName(n)] {
				missing = append(missing, n)
			}
		}
		check(len(missing) == 0, "missing required section: "+strings.Join(missing, ", "))
	}
	if len(c.StrictSectionOrder) > 0 {
		ok := isSubsequence(c.StrictSectionOrder, checkOrderedSections(in.Content))
		check(ok, "sections not in the required order: "+strings.Join(c.StrictSectionOrder, ", "))
	}
	if c.ExactParagraphs > 0 {
		got := checkParagraphCount(in.Content)
		check(got == c.ExactParagraphs,
			"paragraph count "+strconv.Itoa(got)+" != "+strconv.Itoa(c.ExactParagraphs))
	}
	if c.MinWords > 0 {
		got := len(strings.Fields(in.Content))
		check(got >= c.MinWords, "word count "+strconv.Itoa(got)+" is below "+strconv.Itoa(c.MinWords))
	}
	if c.MaxWords > 0 {
		got := len(strings.Fields(in.Content))
		check(got <= c.MaxWords, "word count "+strconv.Itoa(got)+" exceeds "+strconv.Itoa(c.MaxWords))
	}
	if len(c.ForbiddenPhrases) > 0 {
		low := strings.ToLower(in.Content)
		var hit []string
		for _, p := range c.ForbiddenPhrases {
			if strings.Contains(low, strings.ToLower(p)) {
				hit = append(hit, p)
			}
		}
		check(len(hit) == 0, "forbidden phrase present: "+strings.Join(hit, ", "))
	}
	if len(c.MustContain) > 0 {
		low := strings.ToLower(in.Content)
		var missing []string
		for _, p := range c.MustContain {
			if !strings.Contains(low, strings.ToLower(p)) {
				missing = append(missing, p)
			}
		}
		check(len(missing) == 0, "required phrase missing: "+strings.Join(missing, ", "))
	}
	if c.NoHeadings {
		check(!checkHasHeading(in.Content), "document contains a heading")
	}
	if c.NoPlaceholders {
		if p := findPlaceholder(in.Content); p != "" {
			check(false, "placeholder present: "+p)
		} else {
			check(true, "")
		}
	}
	for _, it := range c.MinItems {
		if it.Min <= 0 {
			continue
		}
		got := checkBulletsInSection(in.Content, strings.ToLower(it.Noun))
		check(got >= it.Min, "fewer than "+strconv.Itoa(it.Min)+" "+it.Noun+" items")
	}

	if total == 0 {
		// The declared set held only no-op entries (e.g. a Min item with
		// Min <= 0): nothing was checked, so carry no signal.
		return Verdict{Verifier: "checks", Applied: false, Hard: true}
	}
	v.Pass = passed == total
	v.Score = float64(passed) / float64(total)
	if v.Pass {
		v.Reasons = []string{"all declared checks passed"}
	}
	return v
}

// checksHeadingRE matches an ATX markdown heading, matching the benchmark's
// independent scorer exactly (tools/bench/checks.go).
var checksHeadingRE = regexp.MustCompile(`(?m)^[ \t]*#{1,6}[ \t]+(.+?)[ \t]*$`)

// normalizeCheckName lowercases and trims a heading to its comparable form.
// The trim set matches the benchmark scorer.
func normalizeCheckName(s string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(s), "#*:`\".,"))
}

// checkSectionNames returns the normalized set of the document's headings.
func checkSectionNames(content string) map[string]bool {
	out := map[string]bool{}
	for _, m := range checksHeadingRE.FindAllStringSubmatch(content, -1) {
		out[normalizeCheckName(m[1])] = true
	}
	return out
}

// checkOrderedSections returns the document's headings in order.
func checkOrderedSections(content string) []string {
	var out []string
	for _, m := range checksHeadingRE.FindAllStringSubmatch(content, -1) {
		out = append(out, normalizeCheckName(m[1]))
	}
	return out
}

func checkHasHeading(content string) bool { return checksHeadingRE.MatchString(content) }

// isSubsequence reports whether every wanted section appears in order within
// the document's headings.
func isSubsequence(want, have []string) bool {
	i := 0
	for _, h := range have {
		if i < len(want) && h == normalizeCheckName(want[i]) {
			i++
		}
	}
	return i == len(want)
}

// checkParagraphCount counts non-empty prose blocks separated by blank lines; a
// heading-only or fence-only block is not a paragraph. Matches the benchmark
// scorer (structureVerifier's countParagraphs additionally treats bold headings
// as non-prose, a difference kept for parity with the corpus).
func checkParagraphCount(content string) int {
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

// checkBulletsInSection counts list items under the first heading whose
// normalized name contains name, up to the next heading. When no such heading
// exists it counts bullets in the whole document (the corpus's fallback for a
// count that lives inside another section, e.g. risks inside Consequences).
func checkBulletsInSection(content, name string) int {
	if name == "" {
		return 0
	}
	n, in, found := 0, false, false
	for _, ln := range strings.Split(content, "\n") {
		if m := checksHeadingRE.FindStringSubmatch(ln); m != nil {
			in = strings.Contains(normalizeCheckName(m[1]), name)
			if in {
				found = true
			}
			continue
		}
		if !in {
			continue
		}
		if isBulletLine(ln) {
			n++
		}
	}
	if found {
		return n
	}
	total := 0
	for _, ln := range strings.Split(content, "\n") {
		if isBulletLine(ln) {
			total++
		}
	}
	return total
}

// isBulletLine reports whether a line is a markdown list item.
func isBulletLine(line string) bool {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
		return true
	}
	if i := strings.IndexByte(t, '.'); i > 0 && i <= 3 {
		if _, err := strconv.Atoi(t[:i]); err == nil {
			return true
		}
	}
	return false
}
