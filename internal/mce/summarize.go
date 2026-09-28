package mce

import (
	"context"
	"errors"
	"fmt"

	"github.com/tcs76321/athanor/internal/mce/division"
)

// Summarizer produces the one-line Dormant Index summary for a chunk (§10.1).
//
// The interface lives here, not in cmd/, so the MCE can drive enrichment
// without importing internal/llm (ADR-0021 §2 preserves Gate G2 rule 1's
// intent). The LLM-backed implementation is cmd/athanor/mce_adapter.go; tests
// inject a fake.
type Summarizer interface {
	Summarize(ctx context.Context, chunk division.Chunk) (string, error)
}

// EnrichResult counts the outcomes of one Enrich pass.
type EnrichResult struct {
	// Enriched chunks received a summary this pass.
	Enriched int
	// Skipped chunks already carried a ready summary and were left alone.
	Skipped int
	// Failed chunks whose summarizer call errored; they stay pending and are
	// retried by a later Enrich pass.
	Failed int
}

// Enrich fills dormant_index.summary for every chunk of a source hash.
//
// It is idempotent: a chunk whose summary is already 'ready' is skipped, so a
// second pass over an unchanged source does no work. A summarizer failure is
// counted, not fatal — the row stays 'pending' and the chunk is never lost
// (indexing is the idle path, §17). Only a storage failure returns an error.
//
// The dormant_index CHECK set also admits 'failed'; that status is reserved
// for a future permanent-failure policy and is not written here, so a
// transient summarizer outage is retryable.
func (c *ChunkStore) Enrich(ctx context.Context, sourceHash string, s Summarizer) (EnrichResult, error) {
	if s == nil {
		return EnrichResult{}, errors.New("mce: Enrich requires a Summarizer")
	}
	recs, err := c.ListBySource(ctx, sourceHash)
	if err != nil {
		return EnrichResult{}, err
	}
	var res EnrichResult
	for _, r := range recs {
		status, err := c.indexStatus(ctx, r.ID)
		if err != nil {
			return EnrichResult{}, err
		}
		if status == "ready" {
			res.Skipped++
			continue
		}
		summary, serr := s.Summarize(ctx, chunkFromRecord(r))
		if serr != nil {
			res.Failed++
			continue
		}
		if err := c.setSummary(ctx, r.ID, summary, "ready"); err != nil {
			return EnrichResult{}, err
		}
		res.Enriched++
	}
	return res, nil
}

// indexStatus reads one chunk's summary status.
func (c *ChunkStore) indexStatus(ctx context.Context, chunkID string) (string, error) {
	var status string
	if err := c.db.DB().QueryRowContext(ctx,
		`SELECT summary_status FROM dormant_index WHERE chunk_id = ?`, chunkID,
	).Scan(&status); err != nil {
		return "", fmt.Errorf("mce: read summary status for %s: %w", chunkID, err)
	}
	return status, nil
}

// setSummary writes a chunk's summary and status.
func (c *ChunkStore) setSummary(ctx context.Context, chunkID, summary, status string) error {
	if _, err := c.db.DB().ExecContext(ctx,
		`UPDATE dormant_index SET summary = ?, summary_status = ? WHERE chunk_id = ?`,
		summary, status, chunkID,
	); err != nil {
		return fmt.Errorf("mce: set summary for %s: %w", chunkID, err)
	}
	return nil
}

// chunkFromRecord projects a stored chunk back onto the division engine's
// value type, which is what a Summarizer consumes.
func chunkFromRecord(r ChunkRecord) division.Chunk {
	return division.Chunk{
		ID:         r.ID,
		FilePath:   r.SourceRelPath,
		Lang:       r.Lang,
		Kind:       r.Kind,
		SourceHash: r.SourceHash,
		ByteStart:  r.ByteStart,
		ByteEnd:    r.ByteEnd,
		LineStart:  r.LineStart,
		LineEnd:    r.LineEnd,
		Content:    r.Content,
	}
}
