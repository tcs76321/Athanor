package main

import "testing"

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
