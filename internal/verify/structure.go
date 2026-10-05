package verify

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/tcs76321/athanor/internal/project"
)

// structureVerifier applies the acceptance criteria that can be checked
// structurally: word limits, exact paragraph counts, required section
// names, placeholder absence, and a minimum list count in a named section.
// It applies only when at least one criterion parses into such a check;
// prose that resists parsing yields an unapplied verdict and the LLM judge
// handles it.
type structureVerifier struct{}

func (structureVerifier) Archetypes() []string {
	return []string{project.ArchetypeText, project.ArchetypeDocument}
}
func (structureVerifier) Name() string { return "structure" }

func (structureVerifier) Verify(in Input) Verdict {
	cs := parseConstraints(in.Criteria)
	if len(cs) == 0 {
		return Verdict{Verifier: "structure", Applied: false}
	}
	v := Verdict{Verifier: "structure", Applied: true}
	passed := 0
	allPass := true
	for _, c := range cs {
		ok, reason := c.check(in.Content)
		if ok {
			passed++
			continue
		}
		allPass = false
		v.Reasons = append(v.Reasons, reason)
	}
	v.Pass = allPass
	v.Score = float64(passed) / float64(len(cs))
	if allPass {
		v.Reasons = []string{"all structural constraints met"}
	}
	return v
}

// constraint is one parsed structural check.
type constraint struct {
	kind string // word_limit | paragraph_count | required_sections | no_placeholders | min_items
	// word_limit
	limit  int
	strict bool
	// paragraph_count
	count int
	// required_sections
	names []string
	// min_items
	noun string
	min  int
}

var (
	numberWords = map[string]int{
		"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
		"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	}
	wordLimitRE    = regexp.MustCompile(`(?i)\b(under|at most|no more than|fewer than|less than)\s+(\d+)\s+words?\b`)
	paraCountRE    = regexp.MustCompile(`(?i)\bexactly\s+(one|two|three|four|five|six|seven|eight|nine|ten|\d+)\s+paragraphs?\b`)
	sectionNamesRE = regexp.MustCompile(`(?i)\bsections?\s+named\s+([^.;]+)`)
	minItemsRE     = regexp.MustCompile(`(?i)\bat least\s+(one|two|three|four|five|six|seven|eight|nine|ten|\d+)\s+([a-z]+)`)
	placeholderRE  = regexp.MustCompile(`(?i)\bno\s+(todo|fixme|placeholders?)\b`)
)

// parseConstraints extracts every structural check the criteria state.
func parseConstraints(criteria []string) []constraint {
	text := strings.Join(criteria, "; ")
	var out []constraint
	for _, m := range wordLimitRE.FindAllStringSubmatch(text, -1) {
		n, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		cmp := strings.ToLower(m[1])
		out = append(out, constraint{
			kind: "word_limit", limit: n,
			strict: cmp == "under" || cmp == "fewer than" || cmp == "less than",
		})
	}
	for _, m := range paraCountRE.FindAllStringSubmatch(text, -1) {
		if n, ok := wordNumber(m[1]); ok {
			out = append(out, constraint{kind: "paragraph_count", count: n})
		}
	}
	for _, m := range sectionNamesRE.FindAllStringSubmatch(text, -1) {
		if names := splitNames(m[1]); len(names) > 0 {
			out = append(out, constraint{kind: "required_sections", names: names})
		}
	}
	if placeholderRE.MatchString(text) {
		out = append(out, constraint{kind: "no_placeholders"})
	}
	for _, m := range minItemsRE.FindAllStringSubmatch(text, -1) {
		n, ok := wordNumber(m[1])
		if !ok {
			continue
		}
		out = append(out, constraint{kind: "min_items", min: n, noun: strings.ToLower(m[2])})
	}
	return out
}

func wordNumber(s string) (int, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if n, ok := numberWords[s]; ok {
		return n, true
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// splitNames parses "Install, Usage, and License" into its parts.
func splitNames(s string) []string {
	s = strings.ReplaceAll(s, " and ", ", ")
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, "`\"")
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (c constraint) check(content string) (bool, string) {
	switch c.kind {
	case "word_limit":
		n := len(strings.Fields(content))
		if c.strict {
			if n < c.limit {
				return true, ""
			}
			return false, "word count " + strconv.Itoa(n) + " is not under " + strconv.Itoa(c.limit)
		}
		if n <= c.limit {
			return true, ""
		}
		return false, "word count " + strconv.Itoa(n) + " exceeds " + strconv.Itoa(c.limit)
	case "paragraph_count":
		n := countParagraphs(content)
		if n == c.count {
			return true, ""
		}
		return false, "paragraph count " + strconv.Itoa(n) + " != " + strconv.Itoa(c.count)
	case "required_sections":
		bodies := sectionBodies(content)
		for _, name := range c.names {
			if _, ok := bodies[normalize(name)]; !ok {
				return false, "missing required section " + name
			}
		}
		return true, ""
	case "no_placeholders":
		if found := findPlaceholder(content); found != "" {
			return false, "placeholder present: " + found
		}
		return true, ""
	case "min_items":
		return c.checkMinItems(content)
	}
	return true, ""
}

func (c constraint) checkMinItems(content string) (bool, string) {
	bodies := sectionBodies(content)
	body, ok := bodies[normalize(c.noun)]
	if !ok {
		// Also accept a plural/singular mismatch.
		for name, b := range bodies {
			if strings.Contains(name, c.noun) || strings.Contains(c.noun, name) {
				body, ok = b, true
				break
			}
		}
	}
	target := content
	if ok {
		target = body
	}
	if n := countBullets(target); n >= c.min {
		return true, ""
	}
	return false, "fewer than " + strconv.Itoa(c.min) + " " + c.noun + " items"
}

// countParagraphs counts non-empty blocks separated by blank lines.
func countParagraphs(content string) int {
	blocks := regexp.MustCompile(`\n[ \t]*\n`).Split(content, -1)
	n := 0
	for _, b := range blocks {
		if strings.TrimSpace(b) != "" {
			n++
		}
	}
	return n
}

// countBullets counts markdown list items.
func countBullets(s string) int {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
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

var boldHeadingRE = regexp.MustCompile(`^\*\*(.+?)\*\*:?$`)

// sectionBodies maps a normalized section name to its body text. Headings
// are markdown `#` lines and bold `**Name**` lines.
func sectionBodies(content string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(content, "\n")
	current := ""
	var buf []string
	flush := func() {
		if current != "" {
			out[current] = strings.Join(buf, "\n")
		}
		buf = nil
	}
	for _, ln := range lines {
		if name, ok := headingName(ln); ok {
			flush()
			current = normalize(name)
			continue
		}
		if current != "" {
			buf = append(buf, ln)
		}
	}
	flush()
	return out
}

func headingName(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "#") {
		name := strings.TrimSpace(strings.TrimLeft(t, "#"))
		name = strings.TrimRight(name, ":")
		if name != "" {
			return name, true
		}
		return "", false
	}
	if m := boldHeadingRE.FindStringSubmatch(t); m != nil {
		name := strings.TrimSpace(strings.Trim(m[1], ":"))
		if name != "" {
			return name, true
		}
	}
	return "", false
}

func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(strings.Trim(s, " :`\"*")))
}

// findPlaceholder returns the first placeholder token found, or "".
func findPlaceholder(content string) string {
	lower := strings.ToLower(content)
	for _, p := range []string{"todo", "fixme", "lorem ipsum"} {
		if strings.Contains(lower, p) {
			return p
		}
	}
	return ""
}
