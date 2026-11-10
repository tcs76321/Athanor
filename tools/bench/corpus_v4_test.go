package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestLoadV4ProtocolAndCorpus is the freeze gate for v4 (ADR-0135): the
// protocol validates, the brief suite declares the two arms, every task names
// a built-in template and at least one source, no unknown check key can become
// a silent pass, and the held-out count matches the protocol.
func TestLoadV4ProtocolAndCorpus(t *testing.T) {
	dir := filepath.Join("..", "..", "eval", "bench", "v4")
	p, err := loadProtocol(filepath.Join(dir, "protocol.yaml"))
	if err != nil {
		t.Fatalf("loadProtocol v4: %v", err)
	}
	if p.Version != 4 || p.Benchmark != "v4" {
		t.Fatalf("protocol = v%d %q", p.Version, p.Benchmark)
	}
	if got := p.Suites["brief"].HeldOut; got != 1 {
		t.Errorf("held_out = %d, want 1", got)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "brief.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var f corpusFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse brief.yaml: %v", err)
	}
	if len(f.Tasks) == 0 {
		t.Fatal("brief corpus is empty")
	}
	held := 0
	ids := map[string]bool{}
	for _, task := range f.Tasks {
		if ids[task.ID] {
			t.Errorf("duplicate task id %q", task.ID)
		}
		ids[task.ID] = true
		if task.Goal == "" || len(task.Goal) > 500 {
			t.Errorf("%s: goal length = %d (project limit 500)", task.ID, len(task.Goal))
		}
		if task.Template == "" {
			t.Errorf("%s: no template", task.ID)
		}
		if len(task.Sources) == 0 {
			t.Errorf("%s: no sources", task.ID)
		}
		if bad := unknownCheckKeys(task.Checks); len(bad) > 0 {
			t.Errorf("%s: unknown checks %v", task.ID, bad)
		}
		if len(task.Checks) == 0 {
			t.Errorf("%s: no checks", task.ID)
		}
		if task.HeldOut {
			held++
		}
		if task.ID == "" {
			t.Error("task without id")
		}
	}
	if held != p.Suites["brief"].HeldOut {
		t.Errorf("held-out tasks = %d, protocol says %d", held, p.Suites["brief"].HeldOut)
	}
}

// TestV4TaskChecksScoreTheContract pins the two new deterministic checks
// against a brief that satisfies and one that violates the v4 contract.
func TestV4TaskChecksScoreTheContract(t *testing.T) {
	const src = "https://www.sqlite.org/whentouse.html"
	checks := map[string]any{
		"required_sections":    []any{"What changed", "Why it matters", "Sources"},
		"allowed_urls":         []any{src},
		"cite_every_paragraph": true,
		"must_contain":         []any{src},
		"forbidden_phrases":    []any{"I cannot", "cannot access"},
	}
	good := "# What changed\n\nSQLite remains a strong fit for embedded and local tools. " + src + " explains the cases.\n\n" +
		"# Why it matters\n\nIt avoids an external service and keeps state local, which simplifies deployment. Source: " + src + ".\n\n" +
		"# Sources\n\n- " + src + "\n"
	if pass, results := runChecks(checks, good); !pass {
		t.Fatalf("contract-satisfying brief failed: %+v", results)
	}
	refusal := "# What changed\n\nI cannot access " + src + " so I cannot write the brief.\n"
	if pass, _ := runChecks(checks, refusal); pass {
		t.Error("refusal passed")
	}
}
