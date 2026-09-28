package mce

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/tcs76321/athanor/internal/airlock/paths"
	"github.com/tcs76321/athanor/internal/mce/division"
	"github.com/tcs76321/athanor/internal/store"
)

// ErrSwappingDisabled reports that ingestion was refused because
// context_engine.enable_lossless_swapping is false (ADR-0021 §9).
var ErrSwappingDisabled = errors.New("mce: lossless swapping disabled (context_engine.enable_lossless_swapping=false)")

// IngestOptions bounds one-file ingestion (§10.1; ADR-0021 §6, §9). The zero
// value is valid only with LosslessSwapping set; size caps and fallback lines
// then take their documented defaults from the caller's configuration.
type IngestOptions struct {
	// MaxSourceBytes skips a source larger than this, recording a `context`
	// audit row. Zero or negative means unlimited.
	MaxSourceBytes int64
	// MaxChunkBytes caps a single chunk's size (byte-split). Zero or
	// negative means unlimited.
	MaxChunkBytes int
	// FallbackLines is the fallback block size; zero selects the default.
	FallbackLines int
	// LosslessSwapping must be true for ingestion to proceed.
	LosslessSwapping bool
}

// IngestResult reports what ingestion did. Skipped is true (with a nil error)
// when the source exceeded MaxSourceBytes, so a caller can tell "too big" from
// a failure.
type IngestResult struct {
	SourceHash string
	Chunks     int
	Skipped    bool
	Reason     string
}

// IngestFile reads rel under root through §21.3 path containment
// (tail-following is refused at the kernel level), divides it, and persists
// the chunks. A source over MaxSourceBytes is skipped with a `context` audit
// row. When LosslessSwapping is false the call refuses with
// ErrSwappingDisabled and touches nothing.
func (c *ChunkStore) IngestFile(ctx context.Context, root, rel string, opts IngestOptions, ref SourceRef) (IngestResult, error) {
	if !opts.LosslessSwapping {
		return IngestResult{}, ErrSwappingDisabled
	}
	f, err := paths.OpenNoFollow(root, rel)
	if err != nil {
		return IngestResult{}, fmt.Errorf("mce: open %q: %w", rel, err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return IngestResult{}, fmt.Errorf("mce: stat %q: %w", rel, err)
	}
	if opts.MaxSourceBytes > 0 && info.Size() > opts.MaxSourceBytes {
		if err := c.auditSkip(ctx, ref, rel, info.Size()); err != nil {
			return IngestResult{}, err
		}
		return IngestResult{Skipped: true, Reason: "source exceeds division_max_source_bytes"}, nil
	}

	// Read with a hard bound so a file that grows between Stat and read
	// cannot slip past the cap unnoticed.
	reader := io.Reader(f)
	if opts.MaxSourceBytes > 0 {
		reader = io.LimitReader(f, opts.MaxSourceBytes+1)
	}
	src, err := io.ReadAll(reader)
	if err != nil {
		return IngestResult{}, fmt.Errorf("mce: read %q: %w", rel, err)
	}
	if opts.MaxSourceBytes > 0 && int64(len(src)) > opts.MaxSourceBytes {
		if err := c.auditSkip(ctx, ref, rel, int64(len(src))); err != nil {
			return IngestResult{}, err
		}
		return IngestResult{Skipped: true, Reason: "source exceeds division_max_source_bytes"}, nil
	}

	if ref.RelPath == "" {
		ref.RelPath = rel
	}
	div := division.New(division.Options{
		FallbackLines: opts.FallbackLines,
		MaxChunkBytes: opts.MaxChunkBytes,
	})
	chunks := div.Divide(rel, division.LangFor(rel), src)
	n, err := c.PutSource(ctx, ref, chunks)
	if err != nil {
		return IngestResult{}, err
	}
	return IngestResult{SourceHash: chunks[0].SourceHash, Chunks: n}, nil
}

// auditSkip records a `context` event for an oversized source.
func (c *ChunkStore) auditSkip(ctx context.Context, ref SourceRef, rel string, size int64) error {
	if _, err := c.db.AppendEvent(ctx, store.Event{
		Category:  "context",
		Level:     store.EventWarn,
		ProjectID: ref.ProjectID,
		JobID:     ref.JobID,
		Data: map[string]any{
			"event":  "source_skipped",
			"source": rel,
			"size":   size,
			"reason": "exceeds division_max_source_bytes",
		},
	}); err != nil {
		return fmt.Errorf("mce: audit skipped source: %w", err)
	}
	return nil
}
