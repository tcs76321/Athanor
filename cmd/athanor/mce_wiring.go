// M5-T2.7: MCE boot wiring. The dormant chunk store and the wide-persona
// summarizer adapter are constructed at daemon boot so a future caller
// (M5-T3 context_swap, M5-T8 repository indexing) finds them ready, and so a
// missing migration or a misconfigured swapping flag surfaces at startup
// rather than at first use.
//
// This is the same construct-early pattern the M4-T5 Gateway used
// (cmd/athanor/gateway.go): no engine call site exists yet, and the
// construction itself is the structural proof that the store shares the
// daemon's single SQLite connection (ADR-0003) and that the summarizer runs
// on the `wide` persona at temperature 0.0 (ADR-0021 §7).
package main

import (
	"context"
	"fmt"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/store"
)

// mceRuntime holds the MCE objects for the daemon's lifetime.
type mceRuntime struct {
	// Store is the dormant chunk store (migration 0009). M5-T3 and M5-T8 are
	// its call sites.
	Store *mce.ChunkStore
	// Summarizer produces Dormant Index summaries (ADR-0021 §7).
	Summarizer mce.Summarizer
	// LosslessSwapping mirrors context_engine.enable_lossless_swapping; when
	// false, ingestion and the future swap refuse.
	LosslessSwapping bool
	// SummarizerPersona names the persona used for summaries.
	SummarizerPersona string
	// CompactStore stores derived Temp 0.0 compactions (migration 0012). The
	// M5-T6 daydream driver is its caller (ADR-0025 §4).
	CompactStore *mce.CompactStore
	// Compactor produces compactions on the security persona at temp 0.0
	// (ADR-0025 §5).
	Compactor mce.Compactor
}

// startMCE constructs the MCE runtime and verifies the MCE tables exist, so a
// daemon whose database predates the MCE fails loudly at boot instead of at
// first use.
func startMCE(st *store.Store, registry *llm.Registry, client *llm.Client, cfg config.ContextEngine) (*mceRuntime, error) {
	var probe int
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM context_chunks`).Scan(&probe); err != nil {
		return nil, fmt.Errorf("mce: context_chunks not present (migration 0009): %w", err)
	}
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM compacted_memory`).Scan(&probe); err != nil {
		return nil, fmt.Errorf("mce: compacted_memory not present (migration 0012): %w", err)
	}
	compactor, err := newMCECompactor(registry, client, cfg)
	if err != nil {
		return nil, err
	}
	return &mceRuntime{
		Store:             mce.NewChunkStore(st),
		Summarizer:        newMCESummarizer(registry, client),
		LosslessSwapping:  cfg.LosslessSwapping(),
		SummarizerPersona: llm.RoleWide,
		CompactStore:      mce.NewCompactStore(st),
		Compactor:         compactor,
	}, nil
}
