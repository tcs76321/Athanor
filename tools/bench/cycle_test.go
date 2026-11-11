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
	if !applyTaskChecks(&g, "New", []string{"story-001", "story-004"}) {
		t.Fatal("applyTaskChecks did not find the synthesize block")
	}
	checks, _ := g.Blocks[1].Config["checks"].(map[string]any)
	rules, _ := checks["forbidden_ids_in_sections"].([]map[string]any)
	if len(rules) != 1 || rules[0]["section"] != "New" {
		t.Fatalf("checks = %#v", checks)
	}
	if ids, _ := rules[0]["ids"].([]string); len(ids) != 2 {
		t.Fatalf("ids = %#v", rules[0]["ids"])
	}
	empty := workflow.Graph{Blocks: []workflow.Block{{ID: "research", Kind: workflow.KindResearch}}}
	if applyTaskChecks(&empty, "New", []string{"story-001"}) {
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
