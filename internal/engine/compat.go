package engine

import "github.com/tcs76321/athanor/internal/engine/contextasm"

// Compatibility aliases for the moved context types. External adapters
// (cmd/athanor, tests) keep using engine.ContextQuery and friends; the
// implementation lives in internal/engine/contextasm.
type (
	ContextQuery       = contextasm.Query
	ContextProvider    = contextasm.Provider
	WorkingSetProvider = contextasm.WorkingSetProvider
	ActiveSeeder       = contextasm.ActiveSeeder
	Eviction           = contextasm.Eviction
	Evictor            = contextasm.Evictor
	ContextRequest     = contextasm.Request
	ContextDecision    = contextasm.Decision
	ContextPlanner     = contextasm.Planner
	FloorPlanner       = contextasm.FloorPlanner
)

// NewLadderEvictor returns the production §10.4 eviction seam.
func NewLadderEvictor() Evictor { return contextasm.NewLadderEvictor() }
