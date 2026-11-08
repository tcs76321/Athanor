package engine

import (
	"context"

	"github.com/tcs76321/athanor/internal/engine/contextasm"
)

// ctxHost is the unexported bridge between the engine and the context
// assembler. Every getter reads the current field, so a seam wired after
// engine.New (corrections, memory, insights, notebook, interruptions, planner)
// takes effect on the next call. The type is unexported; only the consumer
// interfaces are visible outside the root.
type ctxHost struct{ e *Engine }

var _ contextasm.Runtime = ctxHost{}

func (h ctxHost) Chunks() contextasm.Provider         { return h.e.ctxProvider }
func (h ctxHost) Evictor() contextasm.Evictor         { return h.e.evictor }
func (h ctxHost) Corrections() contextasm.Corrections { return h.e.correctionSource }
func (h ctxHost) Memory() contextasm.Memory           { return h.e.memorySource }
func (h ctxHost) Insights() contextasm.Insights       { return h.e.insights }
func (h ctxHost) Interruptions() contextasm.Interruptions {
	return h.e.interruptions
}
func (h ctxHost) Notebook() contextasm.Notebook { return h.e.notebook }
func (h ctxHost) Planner() contextasm.Planner   { return h.e.contextPlanner }

func (h ctxHost) AuditCat(ctx context.Context, jobID, category string, data map[string]any) {
	h.e.auditCat(ctx, jobID, category, data)
}
