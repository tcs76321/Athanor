package mce

import (
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
)

// TestAssessThresholds is the M5-T4 acceptance criterion (ROADMAP §M5-T4,
// §10.4): simulated pressure tests trigger the correct action at each
// threshold; a floor breach never resolves to a sendable outcome
// (ADR-0022 §1–2). Boundary rows pin the strictly-`>` trigger semantics
// and the `>=` floor breach.
func TestAssessThresholds(t *testing.T) {
	defaults := config.ContextEngine{KVCacheWarningThresh: 0.85, KVCacheCriticalThresh: 0.95}

	tests := []struct {
		name         string
		active       int
		max          int
		ce           config.ContextEngine
		wantAction   Action
		wantPressure float64
		wantRec      bool // recommendation must be non-empty
	}{
		{name: "well below warning", active: 5000, max: 10000, ce: defaults,
			wantAction: ActionNone, wantPressure: 0.5},
		{name: "just below warning", active: 8499, max: 10000, ce: defaults,
			wantAction: ActionNone, wantPressure: 0.8499},
		// Exact-boundary rows: > is strict, so exactly-at-threshold is
		// the action below (§10.4 "active_tokens > 85% of max_context").
		{name: "exactly at warning 0.85 → none", active: 8500, max: 10000, ce: defaults,
			wantAction: ActionNone, wantPressure: 0.85},
		{name: "one token past warning → warn", active: 8501, max: 10000, ce: defaults,
			wantAction: ActionWarn, wantPressure: 0.8501, wantRec: true},
		{name: "exactly at critical 0.95 → warn", active: 9500, max: 10000, ce: defaults,
			wantAction: ActionWarn, wantPressure: 0.95, wantRec: true},
		{name: "one token past critical → critical", active: 9501, max: 10000, ce: defaults,
			wantAction: ActionCritical, wantPressure: 0.9501, wantRec: true},
		{name: "near-full but fitting → critical", active: 9999, max: 10000, ce: defaults,
			wantAction: ActionCritical, wantPressure: 0.9999, wantRec: true},
		// Floor breach: >= maxContext can never be a sendable outcome.
		{name: "exactly full window → floor breach", active: 10000, max: 10000, ce: defaults,
			wantAction: ActionFloorBreach, wantPressure: 1.0, wantRec: true},
		{name: "overfull window → floor breach", active: 12000, max: 10000, ce: defaults,
			wantAction: ActionFloorBreach, wantPressure: 1.2, wantRec: true},
		// Fail closed: a nonsensical window is a config error (ADR-0022 §1).
		{name: "zero window → floor breach", active: 10, max: 0, ce: defaults,
			wantAction: ActionFloorBreach, wantPressure: 0, wantRec: true},
		{name: "negative window → floor breach", active: 10, max: -1, ce: defaults,
			wantAction: ActionFloorBreach, wantPressure: 0, wantRec: true},
		// Operator-tunable thresholds move the boundaries (config.T rules).
		{name: "custom thresholds: at custom warning → none", active: 5000, max: 10000,
			ce:         config.ContextEngine{KVCacheWarningThresh: 0.5, KVCacheCriticalThresh: 0.75},
			wantAction: ActionNone, wantPressure: 0.5},
		{name: "custom thresholds: between → warn", active: 6000, max: 10000,
			ce:         config.ContextEngine{KVCacheWarningThresh: 0.5, KVCacheCriticalThresh: 0.75},
			wantAction: ActionWarn, wantPressure: 0.6, wantRec: true},
		{name: "custom thresholds: past custom critical → critical", active: 8000, max: 10000,
			ce:         config.ContextEngine{KVCacheWarningThresh: 0.5, KVCacheCriticalThresh: 0.75},
			wantAction: ActionCritical, wantPressure: 0.8, wantRec: true},
		{name: "zero thresholds still floor-breaches at full window", active: 10000, max: 10000,
			ce:         config.ContextEngine{},
			wantAction: ActionFloorBreach, wantPressure: 1.0, wantRec: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Assess(tt.active, tt.max, tt.ce)
			if got.Action != tt.wantAction {
				t.Errorf("Action = %q, want %q", got.Action, tt.wantAction)
			}
			if got.Pressure != tt.wantPressure {
				t.Errorf("Pressure = %v, want %v", got.Pressure, tt.wantPressure)
			}
			if tt.wantRec && got.Recommendation == "" {
				t.Errorf("Recommendation empty, want non-empty (Action %q)", got.Action)
			}
			if !tt.wantRec && got.Recommendation != "" {
				t.Errorf("Recommendation = %q, want empty (Action %q)", got.Recommendation, got.Action)
			}
		})
	}
}

// TestAssessFloorBreachNeverSendable pins the "floor breach never
// silently truncates" acceptance half: every floor-breach assessment
// carries the §12.3 escalation path naming the two remedies and HITL.
func TestAssessFloorBreachNeverSendable(t *testing.T) {
	for _, tc := range []struct {
		name         string
		active       int
		max          int
		wantFragment []string
	}{
		{name: "exactly full", active: 32768, max: 32768, wantFragment: []string{
			"smaller model with larger context", "reduce task scope", "HITL",
			"never silently truncates",
		}},
		{name: "overfull", active: 40000, max: 32768, wantFragment: []string{
			"smaller model with larger context", "reduce task scope", "HITL",
			"never silently truncates",
		}},
		{name: "zero window", active: 100, max: 0, wantFragment: []string{
			"nonsensical", "never silently truncates",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Assess(tc.active, tc.max, config.ContextEngine{
				KVCacheWarningThresh: 0.85, KVCacheCriticalThresh: 0.95,
			})
			if got.Action != ActionFloorBreach {
				t.Fatalf("Action = %q, want %q", got.Action, ActionFloorBreach)
			}
			for _, fragment := range tc.wantFragment {
				if !strings.Contains(got.Recommendation, fragment) {
					t.Errorf("Recommendation %q missing %q", got.Recommendation, fragment)
				}
			}
		})
	}
}