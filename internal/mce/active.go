package mce

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tcs76321/athanor/internal/store"
)

// SwapResult reports one active/dormant swap (§10.1).
type SwapResult struct {
	// Flushed is the chunk that was active before the swap, if any. Its
	// stored bytes are returned intact (unchanged and hash-verified).
	Flushed *ChunkRecord
	// Loaded is the chunk that is now active; its bytes are hash-verified.
	Loaded ChunkRecord
	// NoOp is true when the requested chunk was already active.
	NoOp bool
}

// Active returns the chunk currently active for a scope. ok is false when no
// chunk is active (a fresh scope).
func (c *ChunkStore) Active(ctx context.Context, scope string) (ChunkRecord, bool, error) {
	id, ok, err := c.activeChunkID(ctx, scope)
	if err != nil || !ok {
		return ChunkRecord{}, false, err
	}
	rec, err := c.Get(ctx, id)
	if err != nil {
		return ChunkRecord{}, false, err
	}
	return rec, true, nil
}

// activeChunkID reads the active chunk id for a scope. ok is false when the
// scope has no row.
func (c *ChunkStore) activeChunkID(ctx context.Context, scope string) (string, bool, error) {
	var id string
	err := c.db.DB().QueryRowContext(ctx,
		`SELECT chunk_id FROM context_active WHERE scope_id = ?`, scope).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("mce: read active chunk for scope %q: %w", scope, err)
	}
	return id, true, nil
}

// Swap makes targetChunkID the active chunk for scope (§10.1; M5-T3).
//
// The loaded chunk is returned byte-exact (its stored bytes are hash-verified
// before it is activated), and the chunk it replaced is returned intact —
// still hash-verified, still persisted — so a flush never loses the dormant
// copy. The mapping is persisted in `context_active`, so a crash between the
// swap and the next phase resumes with the same active chunk (§23.6).
//
// Swapping to the already-active chunk is a no-op. An unknown target, or a
// chunk whose stored bytes have drifted from their hash, is an error and
// leaves the active mapping unchanged.
func (c *ChunkStore) Swap(ctx context.Context, scope, targetChunkID string) (SwapResult, error) {
	if scope == "" {
		return SwapResult{}, errors.New("mce: Swap requires a scope")
	}
	if targetChunkID == "" {
		return SwapResult{}, errors.New("mce: Swap requires a target chunk id")
	}
	target, err := c.Get(ctx, targetChunkID)
	if err != nil {
		return SwapResult{}, err
	}
	if got := hashBytes(target.Content); got != target.ContentHash {
		return SwapResult{}, &ContentMismatchError{ID: target.ID, WantHash: target.ContentHash, GotHash: got}
	}

	prevID, hasPrev, err := c.activeChunkID(ctx, scope)
	if err != nil {
		return SwapResult{}, err
	}
	if hasPrev && prevID == targetChunkID {
		return SwapResult{Loaded: target, NoOp: true}, nil
	}

	var flushed *ChunkRecord
	if hasPrev {
		prev, err := c.Get(ctx, prevID)
		if err != nil {
			return SwapResult{}, err
		}
		if got := hashBytes(prev.Content); got != prev.ContentHash {
			return SwapResult{}, &ContentMismatchError{ID: prev.ID, WantHash: prev.ContentHash, GotHash: got}
		}
		flushed = &prev
	}

	if _, err := c.db.DB().ExecContext(ctx, `
		INSERT INTO context_active (scope_id, chunk_id) VALUES (?, ?)
		ON CONFLICT(scope_id) DO UPDATE SET chunk_id = excluded.chunk_id`,
		scope, targetChunkID); err != nil {
		return SwapResult{}, fmt.Errorf("mce: persist active chunk for scope %q: %w", scope, err)
	}

	flushedID := ""
	if flushed != nil {
		flushedID = flushed.ID
	}
	if _, err := c.db.AppendEvent(ctx, store.Event{
		Category: "context",
		Data: map[string]any{
			"event": "chunk_swapped", "scope": scope, "flushed": flushedID, "loaded": targetChunkID,
		},
	}); err != nil {
		return SwapResult{}, fmt.Errorf("mce: audit swap: %w", err)
	}
	return SwapResult{Flushed: flushed, Loaded: target}, nil
}
