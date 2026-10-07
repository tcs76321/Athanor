package main

import "testing"

// TestLoadCorpus proves the M8-T16 bridge: eval/bench/tasks.yaml loads into
// probe goals with ids, tiers, and per-task test commands intact.
func TestLoadCorpus(t *testing.T) {
	goals, err := loadCorpus("../../eval/bench/tasks.yaml")
	if err != nil {
		t.Fatalf("loadCorpus: %v", err)
	}
	if len(goals) != 15 {
		t.Errorf("corpus tasks = %d, want 15", len(goals))
	}
	byTier := map[string]int{}
	for i, g := range goals {
		if g.Name == "" || g.Goal == "" {
			t.Errorf("task %d has empty id/goal: %+v", i+1, g)
		}
		if g.Number != i+1 {
			t.Errorf("task %s Number = %d, want %d", g.Name, g.Number, i+1)
		}
		byTier[g.Source]++
	}
	if byTier["bench:H"] == 0 || byTier["bench:M"] == 0 {
		t.Errorf("tier counts = %v, want some bench:H and bench:M", byTier)
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

// TestLoadCorpusMissing proves a bad path is a loud error, not an empty set.
func TestLoadCorpusMissing(t *testing.T) {
	if _, err := loadCorpus("../../eval/bench/does-not-exist.yaml"); err == nil {
		t.Error("loadCorpus on a missing file returned nil error")
	}
}
