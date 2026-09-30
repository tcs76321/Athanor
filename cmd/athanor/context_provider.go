// M5-T5.6: the ContextProvider adapter. internal/engine defines the seam
// (the ADR-0019 inversion: the MCE must not import the engine); this file is
// where the two meet, exactly like context_swap_adapter.go. The adapter maps
// storage records into the prompt package's tier payloads, so the engine
// never sees an mce type and internal/mce never sees the engine.
package main

import (
	"context"

	"github.com/tcs76321/athanor/internal/engine"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/prompt"
)

// contextProvider adapts the MCE chunk store to engine.ContextProvider.
//
// `enabled` mirrors context_engine.enable_lossless_swapping (ADR-0021 §9):
// when false, ingestion refuses and the working set is unavailable, so the
// provider reports empty tiers rather than a working set the operator
// switched off. That is the same gate the context_swap route uses (501).
type contextProvider struct {
	store   *mce.ChunkStore
	enabled bool
}

// Compile-time proof that the adapter satisfies the engine seam.
var _ engine.ContextProvider = (*contextProvider)(nil)

func newContextProvider(store *mce.ChunkStore, enabled bool) *contextProvider {
	return &contextProvider{store: store, enabled: enabled}
}

// ActiveChunk returns the §10.1 chunk currently active for the job's scope
// (the job ID, matching toolenvelope.ContextSwapRequest.Scope). A missing
// row is "no chunk active", not an error: that is the normal state before
// any context_swap.
func (p *contextProvider) ActiveChunk(ctx context.Context, jobID string) (prompt.ChunkText, bool, error) {
	if !p.enabled || p.store == nil || jobID == "" {
		return prompt.ChunkText{}, false, nil
	}
	rec, ok, err := p.store.Active(ctx, jobID)
	if err != nil || !ok {
		return prompt.ChunkText{}, false, err
	}
	return prompt.ChunkText{
		ID:        rec.ID,
		RelPath:   rec.SourceRelPath,
		LineStart: rec.LineStart,
		LineEnd:   rec.LineEnd,
		Kind:      string(rec.Kind),
		Text:      string(rec.Content),
	}, true, nil
}

// DormantIndex returns the §10.1 Dormant Index for the job (§11.2 §10).
// Summary may be empty while summary_status is not yet 'ready'; the
// prompt's renderer discloses that rather than inventing a summary.
func (p *contextProvider) DormantIndex(ctx context.Context, jobID string) ([]prompt.IndexLine, error) {
	if !p.enabled || p.store == nil || jobID == "" {
		return nil, nil
	}
	entries, err := p.store.IndexForJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	out := make([]prompt.IndexLine, 0, len(entries))
	for _, e := range entries {
		out = append(out, prompt.IndexLine{
			ChunkID:   e.ChunkID,
			Summary:   e.Summary,
			RelPath:   e.SourceRelPath,
			LineStart: e.LineStart,
			LineEnd:   e.LineEnd,
			Kind:      string(e.Kind),
		})
	}
	return out, nil
}