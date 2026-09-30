package mce

import (
	"context"
	"errors"
	"fmt"
)

// Memory consolidation (ARCHITECTURE §17.1; ROADMAP M5-T6; ADR-0025).
//
// This is the MCE-side driver for the §17.1 "Memory Consolidation" daydream
// action: it pulls episodic/archival items from a source and compacts each at
// Temp 0.0, accumulating the counters the §17.3 daydream log records. The
// source (which memory to consolidate) and the schedule (when to daydream) are
// the caller's concern — the production sources and the power/idle-gated loop
// live in cmd/athanor/daydream.go (ADR-0025 §6).

// MemorySource yields episodic/archival memory items that have not yet been
// consolidated. Next returns at most limit items; an empty slice means the
// source is caught up.
type MemorySource interface {
	Next(ctx context.Context, limit int) ([]MemoryItem, error)
}

// ConsolidationResult counts one consolidation pass (§17.3 `memory_compacted`:
// chunks_processed / tokens_saved have byte analogues here).
type ConsolidationResult struct {
	// Processed is the number of items the source returned.
	Processed int
	// Compacted is items that reached the model (a cache miss).
	Compacted int
	// Cached is items whose content-address hit (no model call).
	Cached int
	// Skipped is items the §10.3 matrix classified as full fidelity — they must
	// be divided, not compacted, so the pass leaves them alone.
	Skipped int
	// TooLarge is items that cannot fit the persona's context window; they are
	// refused rather than silently truncated (§10.3) and retried only if a
	// larger-window persona is configured.
	TooLarge int
	// Failed is items whose compaction errored; they stay unconsolidated and
	// are retried by a later pass.
	Failed int
	// BytesBefore and BytesAfter are the summed source/compacted sizes.
	BytesBefore int
	BytesAfter  int
}

// Consolidator runs one bounded consolidation pass over a MemorySource.
type Consolidator struct {
	store *CompactStore
	comp  Compactor
	src   MemorySource
}

// NewConsolidator wires a consolidator. store, comp, and src must all be
// non-nil.
func NewConsolidator(store *CompactStore, comp Compactor, src MemorySource) *Consolidator {
	return &Consolidator{store: store, comp: comp, src: src}
}

// RunOnce pulls up to limit items and compacts each. It is bounded and
// fault-tolerant: a non-compactable item is skipped, a compactor error is
// counted (never fatal), and a cache hit makes no model call. Only a source or
// storage failure returns an error.
func (c *Consolidator) RunOnce(ctx context.Context, limit int) (ConsolidationResult, error) {
	if c.comp == nil {
		return ConsolidationResult{}, ErrCompactorNeeded
	}
	if c.src == nil {
		return ConsolidationResult{}, errors.New("mce: consolidation requires a MemorySource")
	}
	if limit <= 0 {
		return ConsolidationResult{}, errors.New("mce: RunOnce requires limit > 0")
	}
	items, err := c.src.Next(ctx, limit)
	if err != nil {
		return ConsolidationResult{}, fmt.Errorf("mce: consolidation source: %w", err)
	}
	var res ConsolidationResult
	for _, item := range items {
		res.Processed++
		r, err := c.store.CompactMemory(ctx, item, c.comp)
		switch {
		case errors.Is(err, ErrNotCompactable):
			res.Skipped++
			continue
		case errors.Is(err, ErrSourceTooLarge):
			res.TooLarge++
			continue
		case err != nil:
			res.Failed++
			continue
		}
		res.BytesBefore += r.SourceBytes
		res.BytesAfter += r.CompactedBytes
		if r.Cached {
			res.Cached++
		} else {
			res.Compacted++
		}
	}
	return res, nil
}
