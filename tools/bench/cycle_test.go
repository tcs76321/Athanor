package main

import (
	"testing"

	"github.com/tcs76321/athanor/internal/workflow"
)

func TestOldIDsFromChecks(t *testing.T) {
	section, ids := oldIDsFromChecks(map[string]any{
		"new_items_section": map[string]any{
			"section": "New",
			"old_ids": []any{"story-001", "story-004"},
		},
	})
	if section != "New" || len(ids) != 2 || ids[0] != "story-001" || ids[1] != "story-004" {
		t.Fatalf("oldIDsFromChecks = %q %v", section, ids)
	}
	if section, ids := oldIDsFromChecks(nil); section != "" || len(ids) != 0 {
		t.Fatalf("nil checks = %q %v", section, ids)
	}
}

func TestApplyTaskChecks(t *testing.T) {
	g := workflow.Graph{Blocks: []workflow.Block{
		{ID: "research", Kind: workflow.KindResearch},
		{ID: "synthesize", Kind: workflow.KindSynthesize},
	}}
	checks := map[string]any{
		"must_contain": []any{"checkpoint"},
		"max_words":    250,
		"new_items_section": map[string]any{
			"section": "New",
			"old_ids": []any{"story-001", "story-004"},
		},
	}
	if !applyTaskChecks(&g, checks) {
		t.Fatal("applyTaskChecks did not find the synthesize block")
	}
	blockChecks, _ := g.Blocks[1].Config["checks"].(map[string]any)
	if _, ok := blockChecks["must_contain"]; !ok {
		t.Errorf("must_contain was not injected: %#v", blockChecks)
	}
	if got, ok := blockChecks["max_words"].(int); !ok || got != 250 {
		t.Errorf("max_words = %#v", blockChecks["max_words"])
	}
	rules, _ := blockChecks["forbidden_ids_in_sections"].([]map[string]any)
	if len(rules) != 1 || rules[0]["section"] != "New" {
		t.Fatalf("checks = %#v", blockChecks)
	}
	if ids, _ := rules[0]["ids"].([]string); len(ids) != 2 {
		t.Fatalf("ids = %#v", rules[0]["ids"])
	}
	empty := workflow.Graph{Blocks: []workflow.Block{{ID: "research", Kind: workflow.KindResearch}}}
	if applyTaskChecks(&empty, checks) {
		t.Fatal("applyTaskChecks found a synthesize block that does not exist")
	}
}

func TestCycleWaitTerminal(t *testing.T) {
	terminal := []string{"completed", "failed", "cancelled", "awaiting_review"}
	for _, state := range terminal {
		if !cycleWaitTerminal(state) {
			t.Errorf("cycleWaitTerminal(%q) = false, want true", state)
		}
	}
	for _, state := range []string{"running", "paused", "approved", "draft", ""} {
		if cycleWaitTerminal(state) {
			t.Errorf("cycleWaitTerminal(%q) = true, want false", state)
		}
	}
}
