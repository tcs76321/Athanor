package engine

import (
	"strings"

	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/project"
)

// ContextRequest is the planner's view of one phase. Pinned/Working/Output are
// token estimates; ModelMax is the currently selected model's configured
// window (the engine treats it as an upper bound).
type ContextRequest struct {
	Phase      string
	Capability string
	Demand     string
	Model      string
	ModelMax   int
	Pinned     int
	Working    int
	Output     int
}

// ContextDecision is the planner's chosen window. Floor is the minimum the
// planner enforced; Target is the bucketed num_ctx.
type ContextDecision struct {
	Target  int
	Bucket  int
	Floor   int
	Demand  string
	Binding string
}

// ContextPlanner is the ADR-0124 seam. A nil planner preserves the legacy
// fixed-floor behavior (context_policy: archetype).
type ContextPlanner interface {
	PlanContext(req ContextRequest) (ContextDecision, error)
}

// FloorPlanner supplies the model-independent floor before model selection, so
// a short code task can select a small-context model. A nil floor planner uses
// the legacy archetype floors.
type FloorPlanner interface {
	ContextFloor(phase, capability, demand string) (int, bool)
}

// SetContextPlanner wires the planner. Call before the engine serves work.
func (e *Engine) SetContextPlanner(p ContextPlanner) { e.contextPlanner = p }

// SetFloorPlanner wires the floor seam.
func (e *Engine) SetFloorPlanner(p FloorPlanner) { e.floorPlanner = p }

// minContextFor resolves the selection floor, preferring the adaptive planner
// and falling back to the legacy archetype floor.
func (e *Engine) minContextFor(phase, capability, demand string) int {
	if e.floorPlanner != nil {
		if f, ok := e.floorPlanner.ContextFloor(phase, capability, demand); ok {
			return f
		}
	}
	if e.cfg != nil {
		if floor, ok := llm.FloorFor(capability, e.cfg.ContextEngine); ok {
			return floor
		}
	}
	return 0
}

// contextDemandFor is the deterministic pre-planning demand signal. A planner
// hint may raise it later; this never guesses beyond repository/length facts.
func contextDemandFor(p project.Project, t project.Task) string {
	if p.RepositoryPath != "" && capabilityFor(p, t) == project.ArchetypeCode {
		return "repo"
	}
	chars := len([]rune(t.Description)) + len([]rune(strings.Join(t.Criteria, "\n")))
	if chars > 1200 {
		return "long"
	}
	return "short"
}
