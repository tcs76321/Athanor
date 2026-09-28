package mce

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/tcs76321/athanor/internal/mce/division"
)

// fakeSummarizer is the injected Summarizer used by the Enrich tests.
type fakeSummarizer struct {
	summary string
	err     error
	calls   int
}

func (f *fakeSummarizer) Summarize(_ context.Context, _ division.Chunk) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.summary, nil
}

func TestEnrichFillsSummariesAndIsIdempotent(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()
	chunks := divide(t, "demo.go", "go", []byte(goSource))
	if _, err := cs.PutSource(ctx, SourceRef{RelPath: "demo.go"}, chunks); err != nil {
		t.Fatal(err)
	}
	hash := chunks[0].SourceHash

	f := &fakeSummarizer{summary: "declares a function"}
	res, err := cs.Enrich(ctx, hash, f)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if res.Enriched != len(chunks) || res.Failed != 0 || res.Skipped != 0 {
		t.Fatalf("first pass = %+v, want all enriched", res)
	}
	entries, err := cs.IndexForSource(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Summary != "declares a function" || e.SummaryStatus != "ready" {
			t.Errorf("entry %s not enriched: %+v", e.ChunkID, e)
		}
	}

	before := f.calls
	res2, err := cs.Enrich(ctx, hash, f)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Skipped != len(chunks) || res2.Enriched != 0 {
		t.Fatalf("second pass = %+v, want all skipped", res2)
	}
	if f.calls != before {
		t.Errorf("second pass re-summarized %d chunk(s); enrichment is not idempotent", f.calls-before)
	}
}

func TestEnrichDegradesOnSummarizerError(t *testing.T) {
	cs, _ := newStore(t)
	ctx := context.Background()
	chunks := divide(t, "demo.go", "go", []byte(goSource))
	if _, err := cs.PutSource(ctx, SourceRef{RelPath: "demo.go"}, chunks); err != nil {
		t.Fatal(err)
	}
	hash := chunks[0].SourceHash

	f := &fakeSummarizer{err: errors.New("ollama unreachable")}
	res, err := cs.Enrich(ctx, hash, f)
	if err != nil {
		t.Fatalf("Enrich must not fail on a summarizer error: %v", err)
	}
	if res.Failed != len(chunks) || res.Enriched != 0 {
		t.Fatalf("result = %+v, want all failed", res)
	}
	entries, err := cs.IndexForSource(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.SummaryStatus != "pending" {
			t.Errorf("status = %q, want pending (retryable)", e.SummaryStatus)
		}
	}
	got, err := cs.Reassemble(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte(goSource)) {
		t.Fatal("chunks lost after a summarizer failure")
	}
}

func TestEnrichRequiresSummarizer(t *testing.T) {
	cs, _ := newStore(t)
	if _, err := cs.Enrich(context.Background(), "whatever", nil); err == nil {
		t.Error("Enrich(nil summarizer) returned a nil error")
	}
}
