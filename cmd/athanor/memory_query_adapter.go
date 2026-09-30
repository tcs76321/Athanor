package main

import (
	"context"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/internalapi"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// llmEmbedder adapts llm.Client to mce.Embedder (ADR-0026 §3). It is the only
// place an embedding touches a model — internal/mce stays free of
// internal/llm, preserving Gate G2 rule 1's intent.
type llmEmbedder struct {
	client *llm.Client
	model  string
}

// Compile-time proof that the adapter satisfies the MCE seam.
var _ mce.Embedder = (*llmEmbedder)(nil)

// Model names the embedding model, so the vector index keys vectors by model
// and rejects cross-model comparisons.
func (e *llmEmbedder) Model() string { return e.model }

// Embed returns one vector per input text.
func (e *llmEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.client.Embed(ctx, e.model, texts)
}

// memoryQuerier adapts the MCE retriever to internalapi.MemoryQuerier
// (ADR-0026 §6). It owns the scope translation and the
// mce.MemoryHit -> toolenvelope.MemoryHitWire mapping, so internalapi never
// imports internal/mce (the ADR-0019 inversion pattern).
type memoryQuerier struct {
	retriever *mce.Retriever
	topK      int
}

// Compile-time proof that the adapter satisfies the internal API seam.
var _ internalapi.MemoryQuerier = (*memoryQuerier)(nil)

// newMemoryQuerier builds the adapter. When the configured embedding model is
// empty the retriever stays full-text only — the vector half is inert, which
// is the shipped default (ADR-0026 §3).
func newMemoryQuerier(st *store.Store, client *llm.Client, ce config.ContextEngine) *memoryQuerier {
	retriever := mce.NewRetriever(st)
	if ce.MemoryEmbeddingModel != "" {
		retriever.WithVector(
			&llmEmbedder{client: client, model: ce.MemoryEmbeddingModel},
			mce.NewSQLVectorIndex(st),
		)
	}
	return &memoryQuerier{retriever: retriever, topK: ce.MemorySearchTopK}
}

// QueryMemory runs one scoped retrieval and maps the result onto the wire
// shape. An explicit ProjectID wins over Scope; otherwise Scope names the job
// (the handler defaults it to the authenticated job id). Neither present is a
// scope error — an unscoped search would span every project (ADR-0026 §5).
func (q *memoryQuerier) QueryMemory(ctx context.Context, req toolenvelope.QueryMemoryRequest) (*toolenvelope.QueryMemoryResponse, error) {
	var scope mce.Scope
	switch {
	case req.ProjectID != "":
		scope.ProjectID = req.ProjectID
	case req.Scope != "":
		scope.JobID = req.Scope
	default:
		return nil, internalapi.ErrMemoryScopeRequired
	}

	topK := req.TopK
	if topK <= 0 {
		topK = q.topK
	}
	hits, err := q.retriever.Query(ctx, mce.QueryOptions{Query: req.Query, Scope: scope, TopK: topK})
	if err != nil {
		return nil, err
	}

	out := make([]toolenvelope.MemoryHitWire, 0, len(hits))
	for _, h := range hits {
		out = append(out, toolenvelope.MemoryHitWire{
			ID:            h.ID,
			Kind:          string(h.Kind),
			Score:         h.Score,
			BM25Rank:      h.BM25Rank,
			CosineRank:    h.CosineRank,
			SourceRelPath: h.SourceRelPath,
			Summary:       h.Summary,
			LineStart:     h.LineStart,
			LineEnd:       h.LineEnd,
			Content:       h.Content,
		})
	}
	return &toolenvelope.QueryMemoryResponse{
		Query:         req.Query,
		Scope:         req.Scope,
		VectorEnabled: q.retriever.VectorEnabled(),
		Hits:          out,
	}, nil
}
