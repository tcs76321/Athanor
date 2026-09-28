// M5-T3.3: the context_swap adapter. internalapi owns the ContextSwapper
// interface; the MCE owns the swap implementation; this file is the only place
// the two packages meet (the ADR-0019 §2 inversion pattern — internalapi must
// not import internal/mce, and the MCE must not import internalapi). The
// adapter translates the MCE's typed errors into the internalapi sentinels the
// handler maps to statuses.
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/tcs76321/athanor/internal/internalapi"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// contextSwapAdapter adapts an *mce.ChunkStore to internalapi.ContextSwapper.
//
// `enabled` mirrors context_engine.enable_lossless_swapping (ADR-0021 §9):
// when false, every swap refuses with internalapi.ErrSwapDisabled (501) before
// touching the store.
type contextSwapAdapter struct {
	store   *mce.ChunkStore
	enabled bool
}

// Compile-time proof that the adapter satisfies the API seam.
var _ internalapi.ContextSwapper = (*contextSwapAdapter)(nil)

func newContextSwapAdapter(store *mce.ChunkStore, enabled bool) *contextSwapAdapter {
	return &contextSwapAdapter{store: store, enabled: enabled}
}

// SwapContext rotates the working set and returns the loaded chunk. An unknown
// target chunk surfaces as internalapi.ErrChunkNotFound (404) so the caller can
// distinguish "no such chunk" from a storage failure.
func (a *contextSwapAdapter) SwapContext(ctx context.Context, req toolenvelope.ContextSwapRequest) (*toolenvelope.ContextSwapResponse, error) {
	if !a.enabled {
		return nil, internalapi.ErrSwapDisabled
	}
	if a.store == nil {
		return nil, errors.New("context swap: chunk store not configured")
	}
	res, err := a.store.Swap(ctx, req.Scope, req.TargetChunkID)
	if err != nil {
		if errors.Is(err, mce.ErrNotFound) {
			return nil, fmt.Errorf("%w: %s", internalapi.ErrChunkNotFound, req.TargetChunkID)
		}
		return nil, err
	}
	out := &toolenvelope.ContextSwapResponse{
		Scope:         req.Scope,
		LoadedChunkID: res.Loaded.ID,
		LoadedBytes:   len(res.Loaded.Content),
		LoadedContent: string(res.Loaded.Content),
		NoOp:          res.NoOp,
	}
	if res.Flushed != nil {
		out.FlushedChunkID = res.Flushed.ID
	}
	return out, nil
}
