package main

import (
	"testing"

	"github.com/tcs76321/athanor/internal/config"
)

// TestProbeConfigSoak validates the soak variant: the compressed time-triggered
// schedules (M8-T31) and the lowered strategy_analysis floor (M8-T30) must be
// accepted by the real config loader, so the release run cannot boot on a
// generated config the daemon would reject.
func TestProbeConfigSoak(t *testing.T) {
	raw := probeConfigYAML(probeModels[0], arms[0], "off", "127.0.0.1:7420", false, probePolicy{Soak: true})
	cfg, err := config.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("soak config rejected by internal/config: %v\n%s", err, raw)
	}
	if cfg.StrategyAnalysis.MinCohortSize != 8 {
		t.Errorf("soak min_cohort_size = %d, want 8", cfg.StrategyAnalysis.MinCohortSize)
	}
	if cfg.Backup.Schedule != "*/15 * * * *" {
		t.Errorf("soak backup schedule = %q, want */15 * * * *", cfg.Backup.Schedule)
	}
	if !config.Val(cfg.StrategyAnalysis.AutoActivateTechniques, false) {
		t.Error("soak config should enable auto_activate_techniques")
	}
}
