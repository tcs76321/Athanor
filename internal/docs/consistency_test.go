package docs

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/toolenvelope"
	"gopkg.in/yaml.v3"
)

// This file is the documentation-consistency gate (ADR-0124-0128). The
// architecture is large and superseded wording can survive a rename; these
// tests make the load-bearing claims fail the build instead of rotting.

var adrRefRe = regexp.MustCompile(`ADR-(\d{4})`)

// TestADRReferencesResolve fails when a Markdown file names an ADR that does
// not exist. Bare references (not links) are invisible to the link checker.
func TestADRReferencesResolve(t *testing.T) {
	root, err := findModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	missing := map[string][]string{}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "bin", "state", "workspace", "backups", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, path)
		for _, m := range adrRefRe.FindAllStringSubmatch(string(raw), -1) {
			num := m[1]
			matches, _ := filepath.Glob(filepath.Join(root, "docs", "adr", num+"-*.md"))
			if len(matches) == 0 {
				missing[num] = append(missing[num], rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		var lines []string
		for num, files := range missing {
			lines = append(lines, "ADR-"+num+" referenced by "+strings.Join(files, ", "))
		}
		sort.Strings(lines)
		t.Fatalf("ADR references without a file:\n  %s", strings.Join(lines, "\n  "))
	}
}

// supersededPhrases are wording that a later ADR explicitly replaced. The
// registry is intentionally small and explicit: a new phrase needs a
// superseding decision, not a broad heuristic.
var supersededPhrases = []struct {
	phrase string
	why    string
}{
	{"context_swap` suggestion to LLM", "ADR-0080: the model cannot invoke tools; the engine evicts"},
	{"Planned: `read_file`", "ADR-0113: read_file is live as a bounded code-repair observation"},
	{"Archetype constraints", "ADR-0093: projects have inferred capabilities, not archetypes"},
	{"ten today, spanning state", "the ratified dependency set is sixteen"},
	{"ten direct deps", "the ratified dependency set is sixteen"},
}

// TestSupersededPhrases scans current docs (not ADR history, probes, or the
// changelog) for wording a later ADR replaced.
func TestSupersededPhrases(t *testing.T) {
	root, err := findModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	scan := []string{"README.md", "ARCHITECTURE.md", "AGENTS.md", "ROADMAP.md", "DEVELOPMENT.md"}
	var hits []string
	for _, rel := range scan {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, s := range supersededPhrases {
			if strings.Contains(text, s.phrase) {
				hits = append(hits, rel+": "+s.phrase+" — "+s.why)
			}
		}
	}
	if len(hits) > 0 {
		t.Fatalf("superseded wording survives:\n  %s", strings.Join(hits, "\n  "))
	}
}

// TestCurrentBenchmarkIsConsistent pins the release line: eval/bench/CURRENT
// names the instrument 0.0.0 is gated on, its protocol exists, and its
// status/cut pair is coherent. A frozen cut cannot still say "candidate", and a
// pending cut cannot claim to be frozen.
func TestCurrentBenchmarkIsConsistent(t *testing.T) {
	root, err := findModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "eval", "bench", "CURRENT"))
	if err != nil {
		t.Fatalf("eval/bench/CURRENT: %v", err)
	}
	current := strings.TrimSpace(string(raw))
	if current == "" {
		t.Fatal("eval/bench/CURRENT is empty")
	}
	protoPath := filepath.Join(root, "eval", "bench", current, "protocol.yaml")
	data, err := os.ReadFile(protoPath)
	if err != nil {
		t.Fatalf("current benchmark %q has no protocol: %v", current, err)
	}
	var p struct {
		Version   int    `yaml:"version"`
		Benchmark string `yaml:"benchmark"`
		Cut       string `yaml:"cut"`
		Status    string `yaml:"status"`
	}
	if err := yaml.Unmarshal(data, &p); err != nil {
		t.Fatalf("parse %s: %v", protoPath, err)
	}
	if p.Benchmark != current {
		t.Errorf("protocol benchmark %q != CURRENT %q", p.Benchmark, current)
	}
	if p.Cut == "pending" && p.Status != "candidate" {
		t.Errorf("cut=pending requires status=candidate, got %q", p.Status)
	}
	if p.Cut != "pending" && p.Status != "frozen" {
		t.Errorf("cut=%q requires status=frozen, got %q", p.Cut, p.Status)
	}
}

// TestRoadmapAndChangelogTrackCurrentBenchmark keeps the release-line docs
// honest. The ROADMAP must name the current instrument, the changelog must
// mention it, and the top-most changelog section must carry the latest ADR.
// After a release cut, the top section becomes the release section; the test
// follows it rather than assuming "Unreleased" forever.
func TestRoadmapAndChangelogTrackCurrentBenchmark(t *testing.T) {
	root, err := findModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "eval", "bench", "CURRENT"))
	if err != nil {
		t.Fatal(err)
	}
	current := strings.TrimSpace(string(raw))

	roadmap, err := os.ReadFile(filepath.Join(root, "ROADMAP.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(roadmap), current) {
		t.Errorf("ROADMAP.md does not mention the current benchmark %q", current)
	}

	changelog, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(changelog), current) {
		t.Errorf("CHANGELOG.md does not mention the current benchmark %q", current)
	}

	latest := latestADRNumber(t, root)
	top := topChangelogSection(string(changelog))
	if top == "" {
		t.Fatal("CHANGELOG.md has no top-level section")
	}
	if !strings.Contains(top, "ADR-"+latest) {
		t.Errorf("the top CHANGELOG section does not mention the latest ADR-%s", latest)
	}
}

// latestADRNumber returns the highest four-digit ADR number on disk.
func latestADRNumber(t *testing.T, root string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "docs", "adr", "*.md"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("no ADR files: %v", err)
	}
	re := regexp.MustCompile(`^(\d{4})-`)
	max := ""
	for _, m := range matches {
		if sub := re.FindStringSubmatch(filepath.Base(m)); len(sub) == 2 && sub[1] > max {
			max = sub[1]
		}
	}
	if max == "" {
		t.Fatal("no numbered ADR file")
	}
	return max
}

// topChangelogSection returns the first `## ` section after the H1 title.
func topChangelogSection(changelog string) string {
	lines := strings.Split(changelog, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "## ") {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

var backtickRe = regexp.MustCompile("`([a-z_]+)`") // TestClosedToolSetMatchesArchitecture compares the nine-tool paragraph in
// ARCHITECTURE §25 with the executable closed set. Adding a tool to the code
// without the table (or vice versa) fails here.
func TestClosedToolSetMatchesArchitecture(t *testing.T) {
	root, err := findModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "ARCHITECTURE.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	idx := strings.Index(text, "nine-tool closed set")
	if idx < 0 {
		t.Fatal("ARCHITECTURE.md no longer contains the 'nine-tool closed set' paragraph")
	}
	end := strings.Index(text[idx:], "\n\n")
	if end < 0 {
		end = len(text) - idx
	}
	para := text[idx : idx+end]
	open, close := strings.Index(para, "("), strings.Index(para, ")")
	if open < 0 || close <= open {
		t.Fatalf("closed-set paragraph has no parenthesized tool list: %q", para)
	}
	want := map[string]bool{}
	for _, m := range backtickRe.FindAllStringSubmatch(para[open+1:close], -1) {
		want[m[1]] = true
	}
	got := map[string]bool{}
	for _, tool := range toolenvelope.AllTools() {
		got[string(tool)] = true
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("ARCHITECTURE closed set = %v, toolenvelope.AllTools = %v", keys(want), keys(got))
	}
}

// TestConfigExampleTopLevel verifies every top-level key in
// config.example.yaml exists on config.Config. A renaming in the struct cannot
// silently leave the example lying.
func TestConfigExampleTopLevel(t *testing.T) {
	root, err := findModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var example map[string]any
	if err := yaml.Unmarshal(raw, &example); err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	typ := reflect.TypeOf(config.Config{})
	for i := 0; i < typ.NumField(); i++ {
		if tag := typ.Field(i).Tag.Get("yaml"); tag != "" {
			known[strings.Split(tag, ",")[0]] = true
		}
	}
	for key := range example {
		if !known[key] {
			t.Errorf("config.example.yaml key %q is not a config.Config YAML field", key)
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var testNameRe = regexp.MustCompile(`^Test[A-Za-z0-9_]+$`)

// TestClaimsProofReferencesResolve keeps docs/claims.md honest: a backticked
// path must exist, and a backticked Test* name must appear as a Go test
// function. It deliberately ignores commands and prose.
func TestClaimsProofReferencesResolve(t *testing.T) {
	root, err := findModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "docs", "claims.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Index every Go test function name once.
	testFuncs := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "bin", "state", "workspace", "backups", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, m := range regexp.MustCompile(`func (Test[A-Za-z0-9_]+)\(`).FindAllStringSubmatch(string(b), -1) {
			testFuncs[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	pathPrefixes := []string{"internal/", "cmd/", "tools/", "docs/", "scripts/", "eval/", "migrations/", "spikes/"}
	var problems []string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		for _, m := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(line, -1) {
			token := m[1]
			if strings.ContainsAny(token, " *") || strings.HasPrefix(token, "-") {
				continue // commands and flags, not paths/names
			}
			if testNameRe.MatchString(token) {
				if !testFuncs[token] {
					problems = append(problems, "missing test function "+token)
				}
				continue
			}
			isPath := false
			for _, p := range pathPrefixes {
				if strings.HasPrefix(token, p) {
					isPath = true
					break
				}
			}
			if !isPath {
				continue
			}
			// A proof may name a package and symbol (`internal/discovery.Parse`).
			// Check the path portion, not the symbol; keep real file extensions.
			if i := strings.LastIndex(token, "."); i > strings.LastIndex(token, "/") {
				switch filepath.Ext(token) {
				case ".md", ".go", ".yaml", ".yml", ".sh", ".json", ".txt":
					// keep the full path
				default:
					token = token[:i]
				}
			}
			if _, err := os.Stat(filepath.Join(root, token)); err != nil {
				problems = append(problems, "missing path "+token)
			}
		}
	}
	if len(problems) > 0 {
		t.Fatalf("claims.md proof references do not resolve:\n  %s", strings.Join(problems, "\n  "))
	}
}
