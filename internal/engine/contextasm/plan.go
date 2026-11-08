package contextasm

import (
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/llm"
)

// Request is the planner's view of one phase. Pinned/Working/Output are token
// estimates; ModelMax is the currently selected model's configured window (the
// engine treats it as an upper bound).
type Request struct {
	Phase      string
	Capability string
	Demand     string
	Model      string
	ModelMax   int
	Pinned     int
	Working    int
	Output     int
}

// Decision is the planner's chosen window. Floor is the minimum the planner
// enforced; Target is the bucketed num_ctx.
type Decision struct {
	Target  int
	Bucket  int
	Floor   int
	Demand  string
	Binding string
}

// Planner is the ADR-0124 per-job context seam. A nil planner preserves the
// legacy fixed-floor behavior (context_policy: archetype).
type Planner interface {
	PlanContext(req Request) (Decision, error)
}

// FloorPlanner supplies the model-independent floor before model selection, so
// a short code task can select a small-context model. A nil floor planner uses
// the legacy archetype floors.
type FloorPlanner interface {
	ContextFloor(phase, capability, demand string) (int, bool)
}

// MinContextFor resolves the selection floor, preferring the adaptive planner
// and falling back to the legacy archetype floor.
func MinContextFor(floor FloorPlanner, ce config.ContextEngine, phase, capability, demand string) int {
	if floor != nil {
		if f, ok := floor.ContextFloor(phase, capability, demand); ok {
			return f
		}
	}
	if f, ok := llm.FloorFor(capability, ce); ok {
		return f
	}
	return 0
}
