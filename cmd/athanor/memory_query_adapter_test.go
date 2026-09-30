package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/internalapi"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// seedMemoryMemo inserts one compacted_memory row (a §10.3 memo) so the
// retriever's full-text half has something to find.
func seedMemoryMemo(t *testing.T, st *store.Store, id, content, project, job string) {
	t.Helper()
	var pid, jid any
	if project != "" {
		pid = project
	}
	if job != "" {
		jid = job
	}
	if _, err := st.DB().Exec(`
		INSERT INTO compacted_memory
		    (id, kind, profile_type, profile_state, input_hash, template_version,
		     persona, temperature, source_bytes, compacted_bytes, content,
		     project_id, job_id)
		VALUES (?, 'semantic', 'documentation', 'archival', ?, 'v1',
		        'security', 0.0, ?, ?, ?, ?, ?)`,
		id, "ih-"+id, len(content), len(content), content, pid, jid); err != nil {
		t.Fatalf("seed memo %s: %v", id, err)
	}
}

func TestMemoryQuerier_FullTextRetrieval(t *testing.T) {
	st := migratedStore(t)
	seedMemoryMemo(t, st, "cm-1", "the config parser walks the dag", "p1", "")

	q := newMemoryQuerier(st, llm.NewClient("http://127.0.0.1:1", nil), config.ContextEngine{
		MemorySearchTopK: 8,
	})
	res, err := q.QueryMemory(context.Background(), toolenvelope.QueryMemoryRequest{
		Query: "config", ProjectID: "p1",
	})
	if err != nil {
		t.Fatalf("QueryMemory: %v", err)
	}
	if res.VectorEnabled {
		t.Error("VectorEnabled = true, want false (no embedding model configured)")
	}
	if len(res.Hits) != 1 {
		t.Fatalf("hits = %d, want 1 (%+v)", len(res.Hits), res.Hits)
	}
	h := res.Hits[0]
	if h.ID != "cm-1" || h.Kind != "memo" || h.BM25Rank != 1 || h.Score <= 0 {
		t.Errorf("hit = %+v, want cm-1/memo/bm25rank=1/score>0", h)
	}
	if !strings.Contains(h.Content, "config") {
		t.Errorf("content = %q, want the memo text", h.Content)
	}
}

func TestMemoryQuerier_ScopeTranslation(t *testing.T) {
	st := migratedStore(t)
	seedMemoryMemo(t, st, "cm-job", "shared token alpha", "", "job-1")
	seedMemoryMemo(t, st, "cm-proj", "shared token alpha", "p1", "")

	q := newMemoryQuerier(st, llm.NewClient("http://127.0.0.1:1", nil), config.ContextEngine{MemorySearchTopK: 8})

	// Scope names the job when no project is given.
	byJob, err := q.QueryMemory(context.Background(), toolenvelope.QueryMemoryRequest{
		Query: "alpha", Scope: "job-1",
	})
	if err != nil {
		t.Fatalf("QueryMemory(job scope): %v", err)
	}
	if len(byJob.Hits) != 1 || byJob.Hits[0].ID != "cm-job" {
		t.Errorf("job-scoped hits = %+v, want exactly cm-job", byJob.Hits)
	}

	// An explicit ProjectID wins over Scope.
	byProj, err := q.QueryMemory(context.Background(), toolenvelope.QueryMemoryRequest{
		Query: "alpha", Scope: "job-1", ProjectID: "p1",
	})
	if err != nil {
		t.Fatalf("QueryMemory(project scope): %v", err)
	}
	if len(byProj.Hits) != 1 || byProj.Hits[0].ID != "cm-proj" {
		t.Errorf("project-scoped hits = %+v, want exactly cm-proj", byProj.Hits)
	}
}

func TestMemoryQuerier_RequiresScope(t *testing.T) {
	st := migratedStore(t)
	q := newMemoryQuerier(st, llm.NewClient("http://127.0.0.1:1", nil), config.ContextEngine{MemorySearchTopK: 8})

	_, err := q.QueryMemory(context.Background(), toolenvelope.QueryMemoryRequest{Query: "anything"})
	if !errors.Is(err, internalapi.ErrMemoryScopeRequired) {
		t.Fatalf("err = %v, want ErrMemoryScopeRequired", err)
	}
}

func TestMemoryQuerier_VectorEnabledWhenModelConfigured(t *testing.T) {
	st := migratedStore(t)
	client := llm.NewClient("http://127.0.0.1:1", nil)

	fullText := newMemoryQuerier(st, client, config.ContextEngine{MemorySearchTopK: 4})
	if fullText.retriever.VectorEnabled() {
		t.Error("VectorEnabled = true with no embedding model, want false (the shipped default)")
	}

	withVector := newMemoryQuerier(st, client, config.ContextEngine{
		MemoryEmbeddingModel: "nomic-embed-text",
		MemorySearchTopK:     4,
	})
	// Configuration only: the vector half reports enabled without any
	// embeddings existing yet (the indexing producer is M5-T8), and without
	// contacting Ollama.
	if !withVector.retriever.VectorEnabled() {
		t.Error("VectorEnabled = false with a model configured, want true")
	}
}
