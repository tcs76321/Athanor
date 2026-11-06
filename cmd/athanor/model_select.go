package main

import (
	"fmt"
	"strings"

	"github.com/tcs76321/athanor/internal/composition"
	"github.com/tcs76321/athanor/internal/config"
)

// pickWorkModel chooses a model for an interactive onboarding call
// (discovery/planning). The measured pool is authoritative: the legacy persona
// names are only a compatibility fallback when no pool is wired. This prevents
// a config that still names an old model (for example qwen2.5-coder:32b) from
// being used after the user has moved to a different installed pool.
func pickWorkModel(pool *composition.Pool, cfg *config.Config, phase string, stance composition.Stance, minContext int) (string, int, error) {
	if pool != nil {
		req := composition.Request{
			Phase: phase, Capability: composition.CapabilityGeneral,
			Stance: stance, MinContext: minContext, PreferQuality: true,
		}
		if c, ok := pool.Choose(req); ok && strings.TrimSpace(c.Model) != "" {
			return c.Model, maxInt(c.ContextTarget, minContext), nil
		}
		// A pool model may report no context target; retry without the floor
		// rather than falling through to a legacy persona that is not installed.
		req.MinContext = 0
		if c, ok := pool.Choose(req); ok && strings.TrimSpace(c.Model) != "" {
			ctx := c.ContextTarget
			if ctx <= 0 {
				ctx = minContext
			}
			return c.Model, ctx, nil
		}
	}
	if cfg != nil {
		for _, role := range []string{"tall", "main"} {
			pc, ok := cfg.Personas.Role(role)
			if ok && strings.TrimSpace(pc.Model) != "" {
				ctx := pc.ContextTarget
				if ctx <= 0 {
					ctx = minContext
				}
				return pc.Model, ctx, nil
			}
		}
	}
	return "", 0, fmt.Errorf("no model is available: add a model to the pool")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
