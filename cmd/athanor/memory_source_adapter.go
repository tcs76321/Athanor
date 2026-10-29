package main

import (
	"context"

	"github.com/tcs76321/athanor/internal/engine"
	"github.com/tcs76321/athanor/internal/mce"
)

// mceMemorySource adapts the MCE retriever to the engine's §10.2 seam. The
// engine must not import internal/mce's store; this file is the boundary the
// same way context_provider.go is for the working set.
type mceMemorySource struct {
	retriever *mce.Retriever
}

var _ engine.MemorySource = (*mceMemorySource)(nil)

func newMCEMemorySource(r *mce.Retriever) *mceMemorySource {
	return &mceMemorySource{retriever: r}
}

// RelevantMemory queries compacted memory and dormant chunks for one project.
// The engine renders memo hits only; chunk hits are already surfaced by the
// Dormant Index and the working-set pack.
func (m *mceMemorySource) RelevantMemory(ctx context.Context, projectID, query string, limit int) ([]mce.MemoryHit, error) {
	if m == nil || m.retriever == nil || projectID == "" {
		return nil, nil
	}
	return m.retriever.Query(ctx, mce.QueryOptions{
		Query: query,
		Scope: mce.Scope{ProjectID: projectID},
		TopK:  limit,
	})
}
