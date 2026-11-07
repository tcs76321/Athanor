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

var backtickRe = regexp.MustCompile("`([a-z_]+)`")

// TestClosedToolSetMatchesArchitecture compares the nine-tool paragraph in
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
