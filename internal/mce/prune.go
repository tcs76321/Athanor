package mce

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Source pruning (ROADMAP M5-T8; ADR-0028 §5).
//
// A changed or deleted repository file leaves its previous revision's chunks
// behind (migration 0009 keeps them addressable until pruned). PruneSource
// removes one source revision and everything derived from it, in the order
// the schema's foreign keys require:
//
//  1. context_active pointers (FK to context_chunks, no cascade) are cleared;
//  2. memory_embeddings rows keyed by the chunk ids;
//  3. dormant_index rows (the external-content FTS triggers fire);
//  4. context_chunks rows.
//
// It never touches other source hashes.

// PruneSource deletes every chunk of sourceHash and its derived rows. It
// returns the number of chunks removed. An empty hash is a caller bug.
func (c *ChunkStore) PruneSource(ctx context.Context, sourceHash string) (int, error) {
	if sourceHash == "" {
		return 0, errors.New("mce: PruneSource requires a source hash")
	}
	tx, err := c.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("mce: begin prune source: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range []string{
		`DELETE FROM context_active   WHERE chunk_id IN (SELECT id FROM context_chunks WHERE source_hash = ?)`,
		`DELETE FROM memory_embeddings WHERE owner_id IN (SELECT id FROM context_chunks WHERE source_hash = ?)`,
		`DELETE FROM dormant_index    WHERE chunk_id IN (SELECT id FROM context_chunks WHERE source_hash = ?)`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, sourceHash); err != nil {
			return 0, fmt.Errorf("mce: prune source %s: %w", sourceHash, err)
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM context_chunks WHERE source_hash = ?`, sourceHash)
	if err != nil {
		return 0, fmt.Errorf("mce: prune chunks %s: %w", sourceHash, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("mce: prune chunks %s: %w", sourceHash, err)
	}

	data, err := json.Marshal(map[string]any{
		"event": "source_pruned", "hash": sourceHash, "chunks": n,
	})
	if err != nil {
		return 0, fmt.Errorf("mce: marshal prune event: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO events (category, level, data_json) VALUES ('context', 'info', ?)`,
		string(data),
	); err != nil {
		return 0, fmt.Errorf("mce: append prune event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("mce: commit prune source: %w", err)
	}
	return int(n), nil
}
