package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/workflow"
	"gopkg.in/yaml.v3"
)

// TestLoadV5ProtocolAndCorpus is the v5 freeze gate: the protocol is the
// local-only 2x2, every task names a known template when it uses a cycle,
// every repo/source exists, no unknown check key can become a silent pass,
// and the held-out count matches the protocol.
func TestLoadV5ProtocolAndCorpus(t *testing.T) {
	dir := filepath.Join("..", "..", "eval", "bench", "v5")
	p, err := loadProtocol(filepath.Join(dir, "protocol.yaml"))
	if err != nil {
		t.Fatalf("loadProtocol v5: %v", err)
	}
	if p.Version != 5 || p.Benchmark != "v5" {
		t.Fatalf("protocol = v%d %q", p.Version, p.Benchmark)
	}
	if len(p.Generators) != 2 || len(p.Judges) != 2 {
		t.Errorf("roster = %v/%v", p.Generators, p.Judges)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "v5.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var f corpusFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse v5.yaml: %v", err)
	}
	if len(f.Tasks) == 0 {
		t.Fatal("v5 corpus is empty")
	}
	seen := map[string]bool{}
	held := map[string]int{}
	for _, task := range f.Tasks {
		if task.ID == "" {
			t.Error("task without id")
			continue
		}
		if seen[task.ID] {
			t.Errorf("duplicate task id %q", task.ID)
		}
		seen[task.ID] = true
		if task.Suite == "" {
			t.Errorf("%s: no suite", task.ID)
		}
		if task.Goal == "" || len(task.Goal) > 500 {
			t.Errorf("%s: goal length = %d (limit 500)", task.ID, len(task.Goal))
		}
		if len(task.Checks) > 0 {
			if bad := unknownCheckKeys(task.Checks); len(bad) > 0 {
				t.Errorf("%s: unknown checks %v", task.ID, bad)
			}
		}
		if task.Template != "" {
			if _, ok := workflow.TemplateByID(task.Template); !ok {
				t.Errorf("%s: unknown template %q", task.ID, task.Template)
			}
		}
		if task.Repo != "" {
			repo := task.Repo
			if !filepath.IsAbs(repo) {
				repo = filepath.Join("..", "..", repo)
			}
			if _, err := os.Stat(repo); err != nil {
				t.Errorf("%s: repo %q: %v", task.ID, task.Repo, err)
			}
			for _, src := range task.Sources {
				if !strings.HasSuffix(src, ".md") {
					continue
				}
				if _, err := os.Stat(filepath.Join(repo, "sources", src)); err != nil {
					t.Errorf("%s: source %q: %v", task.ID, src, err)
				}
			}
		}
		if task.HeldOut {
			held[task.Suite]++
		}
	}
	for suite, want := range map[string]int{
		"foundation": p.Suites["foundation"].HeldOut,
		"agency":     p.Suites["agency"].HeldOut,
		"utility":    p.Suites["utility"].HeldOut,
		"safety":     p.Suites["safety"].HeldOut,
		"quality":    p.Suites["quality"].HeldOut,
	} {
		if held[suite] != want {
			t.Errorf("%s held-out = %d, protocol says %d", suite, held[suite], want)
		}
	}
}

// TestV5LongCitations pins the file-based citation checks that make the local
// brief and news tasks meaningful without the network.
func TestV5FileCitations(t *testing.T) {
	checks := map[string]any{
		"allowed_sources":      []any{"local-first.md"},
		"cite_every_paragraph": true,
		"must_contain":         []any{"local-first.md"},
	}
	good := "# What changed\n\nLocal-first keeps the primary copy on the device and uses a conflict policy. Source: local-first.md.\n\n# Sources\n\n- local-first.md\n"
	if pass, results := runChecks(checks, good); !pass {
		t.Fatalf("cited brief failed: %+v", results)
	}
	bad := "# What changed\n\nLocal-first keeps the primary copy on the device and uses a conflict policy.\n\n# Sources\n\n- local-first.md\n"
	if pass, _ := runChecks(checks, bad); pass {
		t.Error("uncited paragraph passed")
	}
	invented := good + "\nSee sources/other.md for more.\n"
	if pass, _ := runChecks(checks, invented); pass {
		t.Error("invented source passed")
	}
}

// TestV5NewsNewItemsSection pins the run-2 contract: new ids in New, old ids
// only under Previously reported.
func TestV5NewsNewItemsSection(t *testing.T) {
	checks := map[string]any{
		"new_items_section": map[string]any{
			"section":  "New",
			"required": []any{"story-005", "story-006"},
			"old_ids":  []any{"story-001"},
		},
	}
	good := "# New\n\n- story-005 update\n- story-006\n\n# Previously reported\n\n- story-001\n"
	if pass, results := runChecks(checks, good); !pass {
		t.Fatalf("valid report failed: %+v", results)
	}
	repeated := "# New\n\n- story-001 again\n- story-005\n- story-006\n\n# Previously reported\n\n- story-001\n"
	if pass, _ := runChecks(checks, repeated); pass {
		t.Error("old item in New passed")
	}
	missing := "# New\n\n- story-005\n\n# Previously reported\n\n- story-001\n"
	if pass, _ := runChecks(checks, missing); pass {
		t.Error("missing new item passed")
	}
}
