package engine

import (
	"github.com/tcs76321/athanor/internal/engine/contextasm"
)

// SetContextPlanner wires the adaptive context planner (ADR-0124). Call before
// the engine serves work. A nil planner preserves the archetype-floor behavior.
func (e *Engine) SetContextPlanner(p ContextPlanner) { e.contextPlanner = p }

// SetFloorPlanner wires the model-independent floor seam.
func (e *Engine) SetFloorPlanner(p FloorPlanner) { e.floorPlanner = p }

// minContextFor resolves the selection floor, preferring the adaptive planner
// and falling back to the legacy archetype floor.
func (e *Engine) minContextFor(phase, capability, demand string) int {
	if e.cfg == nil {
		return 0
	}
	return contextasm.MinContextFor(e.floorPlanner, e.cfg.ContextEngine, phase, capability, demand)
}
