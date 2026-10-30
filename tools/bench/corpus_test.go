package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadCorpus proves the M8-T16 bridge: eval/bench/tasks.yaml loads into
// probe goals with ids, tiers, and per-task test commands intact.
func TestLoadCorpus(t *testing.T) {
	goals, err := loadCorpus("../../eval/bench/tasks.yaml")
	if err != nil {
		t.Fatalf("loadCorpus: %v", err)
	}
	if len(goals) != 70 {
		t.Errorf("corpus tasks = %d, want 70", len(goals))
	}
	byTier := map[string]int{}
	var heldOut, tagged int
	for i, g := range goals {
		if g.Name == "" || g.Goal == "" {
			t.Errorf("task %d has empty id/goal: %+v", i+1, g)
		}
		if g.Number != i+1 {
			t.Errorf("task %s Number = %d, want %d", g.Name, g.Number, i+1)
		}
		byTier[g.Source]++
		if g.HeldOut {
			heldOut++
		}
		if g.ContextDemand != "" {
			tagged++
		}
	}
	if byTier["bench:E"] == 0 || byTier["bench:M"] == 0 || byTier["bench:H"] == 0 {
		t.Errorf("tier counts = %v, want some bench:E, bench:M, and bench:H", byTier)
	}
	if heldOut == 0 {
		t.Error("no held-out tasks carried into the probe goals")
	}
	if tagged != len(goals) {
		t.Errorf("context_demand carried on %d/%d goals", tagged, len(goals))
	}
	var code int
	for _, g := range goals {
		if g.Archetype == "code" {
			code++
			if g.TestCommand == "" {
				t.Errorf("code task %s has no test_command", g.Name)
			}
		}
	}
	if code == 0 {
		t.Error("no code tasks loaded")
	}
}

// TestVersionLocalPath pins the freeze-boundary resolver: a sibling-version
// path rewrites to the version-local copy only when that copy exists; an
// already-local path, an absolute path, or a missing local copy is unchanged.
func TestVersionLocalPath(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "fixtures", "data", "schema.json")
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := versionLocalPath(root, "eval/bench/fixtures/data/schema.json"); got != local {
		t.Errorf("sibling path = %q, want %q", got, local)
	}
	alreadyLocal := "eval/bench/v2/fixtures/data-json-normalize/schema.json"
	if got := versionLocalPath("eval/bench/v2", alreadyLocal); got != alreadyLocal {
		t.Errorf("already-local path changed to %q", got)
	}
	if got := versionLocalPath(root, "/absolute/schema.json"); got != "/absolute/schema.json" {
		t.Errorf("absolute path changed to %q", got)
	}
	if got := versionLocalPath(root, "eval/bench/fixtures/missing/schema.json"); got != "eval/bench/fixtures/missing/schema.json" {
		t.Errorf("missing local copy = %q, want the declared path", got)
	}
}

// TestLoadCorpusMissing proves a bad path is a loud error, not an empty set.
func TestLoadCorpusMissing(t *testing.T) {
	if _, err := loadCorpus("../../eval/bench/does-not-exist.yaml"); err == nil {
		t.Error("loadCorpus on a missing file returned nil error")
	}
}
