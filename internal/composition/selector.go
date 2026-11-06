package composition

import (
	"context"

	"github.com/tcs76321/athanor/internal/engine"
)

// Selector adapts a Pool to the engine's ModelSelector seam. It lives here so
// the engine depends only on its own interface, not on this package.
type Selector struct {
	Pool *Pool
}

// Choose implements engine.ModelSelector.
func (s *Selector) Choose(ctx context.Context, req engine.ModelRequest) (engine.ModelChoice, bool) {
	if s == nil || s.Pool == nil {
		return engine.ModelChoice{}, false
	}
	c, ok := s.Pool.Choose(Request{
		Phase: req.Phase, Capability: Capability(req.Capability), Stance: Stance(req.Stance),
		MinContext: req.MinContext, AvoidFamily: req.AvoidFamily, Scope: req.Scope,
		ProjectID: req.ProjectID, JobID: req.JobID, Explore: req.Explore,
	})
	if !ok {
		return engine.ModelChoice{}, false
	}
	return toEngineChoice(c, req.MinContext), true
}

// ChooseMany implements engine.ModelSelector.
func (s *Selector) ChooseMany(ctx context.Context, req engine.ModelRequest, n int) []engine.ModelChoice {
	if s == nil || s.Pool == nil {
		return nil
	}
	cs := s.Pool.ChooseMany(Request{
		Phase: req.Phase, Capability: Capability(req.Capability), Stance: Stance(req.Stance),
		MinContext: req.MinContext, AvoidFamily: req.AvoidFamily, Scope: req.Scope,
		ProjectID: req.ProjectID, JobID: req.JobID, Explore: req.Explore,
	}, n)
	out := make([]engine.ModelChoice, 0, len(cs))
	for _, c := range cs {
		out = append(out, toEngineChoice(c, req.MinContext))
	}
	return out
}

// Persona implements engine.ModelSelector for synthetic `pool:<model>` roles.
func (s *Selector) Persona(model string) (engine.ModelChoice, bool) {
	if s == nil || s.Pool == nil {
		return engine.ModelChoice{}, false
	}
	prof, ok := s.Pool.Profile(model)
	if !ok {
		return engine.ModelChoice{}, false
	}
	return engine.ModelChoice{
		Model:         prof.Name,
		Family:        prof.Family,
		ContextTarget: prof.ContextTarget,
		Temperature:   prof.Temperature,
		Reason:        "pinned by candidate roster",
	}, true
}

// Observe implements engine.ModelSelector.
func (s *Selector) Observe(ctx context.Context, jobID string, success bool) {
	if s == nil || s.Pool == nil {
		return
	}
	s.Pool.Observe(ctx, jobID, success)
}

// PreferPanel implements engine.ModelSelector.
func (s *Selector) PreferPanel() bool {
	if s == nil || s.Pool == nil {
		return false
	}
	return s.Pool.PreferPanel()
}

func toEngineChoice(c Choice, minContext int) engine.ModelChoice {
	ctxTarget := c.ContextTarget
	if ctxTarget == 0 {
		ctxTarget = minContext
	}
	return engine.ModelChoice{
		Model: c.Model, Family: c.Family, ContextTarget: ctxTarget,
		Temperature: c.Temperature, Score: c.Score, Reason: c.Reason,
	}
}
