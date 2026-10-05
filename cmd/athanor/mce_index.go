// M5-T8.7: the repository indexing adapter. internal/mce owns the pipeline;
// this file is where it meets the LLM seams (the wide-persona summarizer and
// the embedding client) and the external API's IndexRunner seam, exactly like
// context_provider.go and memory_query_adapter.go. internal/mce stays free of
// internal/llm (ADR-0021 §2, ADR-0026 §3).
package main

import (
	"context"

	"github.com/tcs76321/athanor/internal/api"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/store"
)

// mceIndexer adapts mce.Indexer to api.IndexRunner (ADR-0028 §6).
type mceIndexer struct {
	indexer *mce.Indexer
	cfg     config.ContextEngine
}

// Compile-time proof that the adapter satisfies the API seam.
var _ api.IndexRunner = (*mceIndexer)(nil)

// newMCEIndexer builds the indexer over the daemon's MCE runtime. An empty
// memory_embedding_model leaves the vector half inert: indexing still divides
// and summarizes, and retrieval stays full-text (ADR-0026 §3).
func newMCEIndexer(rt *mceRuntime, st *store.Store, client *llm.Client, ce config.ContextEngine) *mceIndexer {
	var embed mce.Embedder
	var vectors mce.VectorIndex
	if ce.MemoryEmbeddingModel != "" {
		embed = &llmEmbedder{client: client, model: ce.MemoryEmbeddingModel}
		vectors = mce.NewSQLVectorIndex(st)
	}
	ix := mce.NewIndexer(rt.Store, mce.NewIndexManifest(st), rt.Summarizer, embed, vectors)
	return &mceIndexer{indexer: ix, cfg: ce}
}

// IndexProject indexes a project's repository to completion (RunAll) and
// maps the counts onto the API response.
func (m *mceIndexer) IndexProject(ctx context.Context, projectID, path string) (api.IndexSummary, error) {
	res, err := m.indexer.RunAll(ctx, m.opts(projectID, path))
	if err != nil {
		return api.IndexSummary{}, err
	}
	return api.IndexSummary{
		Discovered: res.Discovered,
		Indexed:    res.Indexed,
		Skipped:    res.Skipped,
		Pruned:     res.Pruned,
		Chunks:     res.Chunks,
		Summarized: res.Summarized,
		Embedded:   res.Embedded,
		Failed:     res.Failed,
	}, nil
}

// opts maps the context_engine configuration onto one pass's bounds.
func (m *mceIndexer) opts(projectID, root string) mce.IndexOptions {
	ce := m.cfg
	return mce.IndexOptions{
		Root:             root,
		ProjectID:        projectID,
		MaxFiles:         ce.IndexBatchFiles,
		MaxChunks:        ce.IndexBatchChunks,
		MaxSourceBytes:   ce.DivisionMaxSourceBytes,
		MaxChunkBytes:    ce.DivisionMaxChunkBytes,
		FallbackLines:    ce.DivisionFallbackLines,
		EmbedBytes:       ce.IndexEmbedBytesValue(),
		ExcludeDirs:      ce.IndexIgnoreDirs,
		LosslessSwapping: ce.LosslessSwapping(),
	}
}
