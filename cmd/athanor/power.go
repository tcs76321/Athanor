package main

import (
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/power"
)

// powerConfigFrom maps config.Power + config.Agent onto the pure §24 policy.
// Idle detection prefers agent.idle_threshold and falls back to
// power.idle_resume_after; both default to 5m.
func powerConfigFrom(cfg *config.Config) power.Config {
	idle := cfg.Agent.IdleThreshold.D()
	if idle <= 0 {
		idle = cfg.Power.IdleResumeAfter.D()
	}
	return power.Config{
		RequireACForDeepWork:         config.Val(cfg.Power.RequireACForDeepWork, true),
		BatteryPauseThresholdPercent: cfg.Power.BatteryPauseThresholdPercent,
		AllowBatteryOverride:         cfg.Power.AllowBatteryOverride,
		PauseOnSleep:                 config.Val(cfg.Power.PauseOnSleep, true),
		ResumeOnWake:                 config.Val(cfg.Power.ResumeOnWake, true),
		DaydreamOnIdle:               config.Val(cfg.Power.DaydreamOnIdle, true),
		ActiveHours:                  cfg.Agent.ActiveHours,
		IdleThreshold:                idle,
	}
}
