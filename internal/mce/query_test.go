package mce

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
)

// seedMemo inserts one compacted_memory row (a §10.3 memo) with an optional
// project/job scope.
func seedMemo(t *testing.T, st *store.Store, id, content, project, job string) {
	t.Helper()
	if _, err := st.DB().Exec(`
		INSERT INTO compacted_memory
		    (id, kind, profile_type, profile_state, input_hash, template_version,
		     persona, temperature, source_bytes, compacted_bytes, content,
		     project_id, job_id)
		VALUES (?, 'semantic', 'documentation', 'archival', ?, 'v1',
		        'security', 0.0, ?, ?, ?, ?, ?)`,
		id, "ih-"+id, len(content), len(content), content,
		nullIfEmpty(project), nullIfEmpty(job)); err != nil {
		t.Fatalf("seed memo %s: %v", id, err)
	}
}

// seedChunk inserts one context_chunks row plus its dormant_index summary.
func seedChunk(t *testing.T, st *store.Store, id, relpath, summary, project, job string) {
	t.Helper()
	db := st.DB()
	if _, err := db.Exec(`
		INSERT INTO context_chunks
		    (id, source_relpath, source_hash, lang, kind, byte_start, byte_end,
		     line_start, line_end, content_hash, content, project_id, job_id)
		VALUES (?, ?, ?, 'go', 'ast', 0, 3, 1, 2, ?, x'616263', ?, ?)`,
		id, relpath, "sh-"+id, "ch-"+id,
		nullIfEmpty(project), nullIfEmpty(job)); err != nil {
		t.Fatalf("seed chunk %s: %v", id, err)
	}
	if _, err := db.Exec(
		`INSERT INTO dormant_index (chunk_id, summary, summary_status) VALUES (?, ?, 'ready')`,
		id, summary); err != nil {
		t.Fatalf("seed index %s: %v", id, err)
	}
}

// fakeEmbedder is a deterministic Embedder for tests: known texts map to
// fixed vectors, unknown texts to a zero vector of the configured width.
type fakeEmbedder struct {
	model string
	dim   int
	vecs  map[string][]float32
}

func (f *fakeEmbedder) Model() string { return f.model }

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, s := range texts {
		if v, ok := f.vecs[s]; ok {
			out[i] = v
			continue
		}
		out[i] = make([]float32, f.dim)
	}
	return out, nil
}

// TestFuseRRF pins reciprocal-rank fusion: an id ranked by both signals
// outranks an id ranked first by one signal only, and every score is
// positive. This is the acceptance criterion's "combined BM25 + vector
// results returned with scores" in its purest form.
func TestFuseRRF(t *testing.T) {
	bm25 := []string{"a", "b", "c"}
	vec := []string{"b", "d"}
	scores := FuseRRF(bm25, vec)

	// b is 2nd in bm25 and 1st in vec; a is 1st in bm25 only.
	if scores["b"] <= scores["a"] {
		t.Errorf("b (both signals) = %v, want > a (one signal) = %v", scores["b"], scores["a"])
	}
	for id, s := range scores {
		if s <= 0 {
			t.Errorf("score(%s) = %v, want > 0", id, s)
		}
	}
	if _, ok := scores["c"]; !ok {
		t.Error("c is missing; every ranked id must appear in the fused map")
	}
	if _, ok := scores["d"]; !ok {
		t.Error("d is missing; vector-only ids must appear in the fused map")
	}
	// A list of length zero contributes nothing.
	if got := FuseRRF(nil, nil); len(got) != 0 {
		t.Errorf("FuseRRF(nil, nil) = %v, want empty", got)
	}
}

// TestFTSQueryNeutralizesOperators proves user text cannot smuggle FTS5
// syntax into MATCH: only word tokens survive, each quoted.
func TestFTSQueryNeutralizesOperators(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"hello world", `"hello" OR "world"`},
		{"  spaced   out  ", `"spaced" OR "out"`},
		{`quote" and * wildcard`, `"quote" OR "and" OR "wildcard"`},
		{`NEAR(a b) -neg`, `"NEAR" OR "a" OR "b" OR "neg"`},
		{`snake_case ident2`, `"snake_case" OR "ident2"`},
		{"!!!", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := ftsQuery(c.in); got != c.want {
			t.Errorf("ftsQuery(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRetrieverFTSMemoAndChunk proves the FTS half returns both a memo and a
// chunk hit for one query, with positive ranks and hydrated display fields.
func TestRetrieverFTSMemoAndChunk(t *testing.T) {
	_, st := newStore(t)
	seedMemo(t, st, "cm-1", "the parser handles config files carefully", "p1", "")
	seedChunk(t, st, "chunk-1", "internal/a/b.go", "walks the config dag", "p1", "")

	r := NewRetriever(st)
	hits, err := r.Query(context.Background(), QueryOptions{
		Query: "config", Scope: Scope{ProjectID: "p1"},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2 (%+v)", len(hits), hits)
	}
	byID := map[string]MemoryHit{}
	for _, h := range hits {
		byID[h.ID] = h
	}
	memo, ok := byID["cm-1"]
	if !ok {
		t.Fatalf("memo cm-1 not returned: %+v", hits)
	}
	if memo.Kind != SourceMemo || memo.BM25Rank != 1 || memo.Score <= 0 {
		t.Errorf("memo hit = %+v, want kind=memo bm25rank=1 score>0", memo)
	}
	if !strings.Contains(memo.Content, "config") {
		t.Errorf("memo content not hydrated: %q", memo.Content)
	}
	chunk, ok := byID["chunk-1"]
	if !ok {
		t.Fatalf("chunk chunk-1 not returned: %+v", hits)
	}
	if chunk.Kind != SourceChunk || chunk.BM25Rank != 1 {
		t.Errorf("chunk hit = %+v, want kind=chunk bm25rank=1", chunk)
	}
	if chunk.SourceRelPath != "internal/a/b.go" || chunk.Summary != "walks the config dag" {
		t.Errorf("chunk not hydrated: %+v", chunk)
	}
	if chunk.LineStart != 1 || chunk.LineEnd != 2 {
		t.Errorf("chunk line range = %d-%d, want 1-2", chunk.LineStart, chunk.LineEnd)
	}
	if chunk.Content != "" {
		t.Errorf("chunk Content = %q, want empty (chunk bytes are reached via context_swap)", chunk.Content)
	}
}

// TestRetrieverScopeIsolation proves retrieval never crosses a scope, and
// that an empty scope returns nothing rather than everything.
func TestRetrieverScopeIsolation(t *testing.T) {
	_, st := newStore(t)
	seedMemo(t, st, "cm-p1", "shared token alpha", "p1", "")
	seedMemo(t, st, "cm-p2", "shared token alpha", "p2", "")
	seedMemo(t, st, "cm-j1", "shared token alpha", "", "job-1")
	r := NewRetriever(st)

	for _, tc := range []struct {
		scope Scope
		want  string
	}{
		{Scope{ProjectID: "p1"}, "cm-p1"},
		{Scope{ProjectID: "p2"}, "cm-p2"},
		{Scope{JobID: "job-1"}, "cm-j1"},
	} {
		hits, err := r.Query(context.Background(), QueryOptions{Query: "alpha", Scope: tc.scope})
		if err != nil {
			t.Fatalf("Query(%+v): %v", tc.scope, err)
		}
		if len(hits) != 1 || hits[0].ID != tc.want {
			t.Errorf("scope %+v returned %+v, want exactly %s", tc.scope, hits, tc.want)
		}
	}

	hits, err := r.Query(context.Background(), QueryOptions{Query: "alpha", Scope: Scope{}})
	if err != nil {
		t.Fatalf("Query(empty scope): %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("empty scope returned %d hits, want 0 (a leak)", len(hits))
	}
}

// TestRetrieverVectorFusion proves the vector half contributes hits the FTS
// half misses, and that the fused hit carries both signal ranks.
func TestRetrieverVectorFusion(t *testing.T) {
	_, st := newStore(t)
	seedMemo(t, st, "cm-far", "unrelated wording entirely", "p1", "")
	seedMemo(t, st, "cm-near", "vector match only", "p1", "")

	idx := NewSQLVectorIndex(st)
	for id, vec := range map[string][]float32{
		"cm-far":  {0, 1},
		"cm-near": {1, 0},
	} {
		if err := idx.Put(context.Background(), EmbeddingRecord{
			OwnerID: id, Kind: SourceMemo, Model: "fake", Vector: vec,
			ContentHash: "ch-" + id, Scope: Scope{ProjectID: "p1"},
		}); err != nil {
			t.Fatalf("Put(%s): %v", id, err)
		}
	}

	embed := &fakeEmbedder{model: "fake", dim: 2, vecs: map[string][]float32{
		"find me": {1, 0},
	}}
	r := NewRetriever(st).WithVector(embed, idx)
	hits, err := r.Query(context.Background(), QueryOptions{
		Query: "find me", Scope: Scope{ProjectID: "p1"},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2 (%+v)", len(hits), hits)
	}
	if hits[0].ID != "cm-near" {
		t.Errorf("top hit = %s, want cm-near (cosine 1.0)", hits[0].ID)
	}
	if hits[0].CosineRank != 1 || hits[0].BM25Rank != 0 {
		t.Errorf("cm-near ranks = bm25:%d cosine:%d, want bm25:0 cosine:1", hits[0].BM25Rank, hits[0].CosineRank)
	}
	if hits[0].Score <= 0 {
		t.Errorf("cm-near score = %v, want > 0", hits[0].Score)
	}
	if hits[1].ID != "cm-far" || hits[1].CosineRank != 2 {
		t.Errorf("second hit = %+v, want cm-far cosine rank 2", hits[1])
	}
}

// TestCosineAndVectorRoundTrip pins the pure vector maths.
func TestCosineAndVectorRoundTrip(t *testing.T) {
	same, _ := Cosine([]float32{1, 2, 3}, []float32{1, 2, 3})
	if math.Abs(same-1) > 1e-9 {
		t.Errorf("Cosine(identical) = %v, want 1", same)
	}
	orth, _ := Cosine([]float32{1, 0}, []float32{0, 1})
	if math.Abs(orth) > 1e-9 {
		t.Errorf("Cosine(orthogonal) = %v, want 0", orth)
	}
	if _, err := Cosine([]float32{1, 0}, []float32{1, 0, 0}); !errors.Is(err, ErrEmbeddingDimMismatch) {
		t.Errorf("Cosine(dim mismatch) err = %v, want ErrEmbeddingDimMismatch", err)
	}

	v := []float32{0.5, -1.25, 3}
	got, err := decodeVector(encodeVector(v), len(v))
	if err != nil {
		t.Fatalf("decodeVector: %v", err)
	}
	for i := range v {
		if got[i] != v[i] {
			t.Errorf("round trip [%d] = %v, want %v", i, got[i], v[i])
		}
	}
}

// TestSQLVectorIndexDimMismatch proves a model's dimension is pinned: a
// second Put with a different length is a typed error, never a silent
// overwrite.
func TestSQLVectorIndexDimMismatch(t *testing.T) {
	_, st := newStore(t)
	idx := NewSQLVectorIndex(st)
	ctx := context.Background()

	if err := idx.Put(ctx, EmbeddingRecord{
		OwnerID: "cm-1", Kind: SourceMemo, Model: "m", Vector: []float32{1, 0, 0},
		ContentHash: "h1", Scope: Scope{ProjectID: "p1"},
	}); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	err := idx.Put(ctx, EmbeddingRecord{
		OwnerID: "cm-2", Kind: SourceMemo, Model: "m", Vector: []float32{1, 0},
		ContentHash: "h2", Scope: Scope{ProjectID: "p1"},
	})
	if !errors.Is(err, ErrEmbeddingDimMismatch) {
		t.Fatalf("second Put err = %v, want ErrEmbeddingDimMismatch", err)
	}

	if _, err := idx.Nearest(ctx, VectorQuery{
		Query: []float32{1, 0}, Model: "m", Kind: SourceMemo, Scope: Scope{ProjectID: "p1"}, K: 5,
	}); !errors.Is(err, ErrEmbeddingDimMismatch) {
		t.Errorf("Nearest err = %v, want ErrEmbeddingDimMismatch", err)
	}

	hits, err := idx.Nearest(ctx, VectorQuery{
		Query: []float32{1, 0, 0}, Model: "m", Kind: SourceMemo, K: 5,
	})
	if err != nil {
		t.Fatalf("Nearest(empty scope): %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("Nearest(empty scope) = %d hits, want 0", len(hits))
	}
}
