package mce

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func TestRememberJobSummaryIsRetrievableAndIdempotent(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "remember.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	c := NewCompactStore(st)
	ctx := context.Background()
	memo := "Project demo. Task: write a network brief. Decision: use a token bucket with a per-host limiter."
	inserted, err := c.RememberJobSummary(ctx, "p-1", "j-1", memo)
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatal("first write was not inserted")
	}
	inserted, err = c.RememberJobSummary(ctx, "p-1", "j-1", memo)
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Fatal("second write was not idempotent")
	}
	hits, err := NewRetriever(st).Query(ctx, QueryOptions{
		Query: "token bucket", Scope: Scope{ProjectID: "p-1"}, TopK: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, hit := range hits {
		if hit.Kind == SourceMemo && hit.Content == memo {
			found = true
		}
	}
	if !found {
		t.Fatalf("remembered summary not retrieved: %+v", hits)
	}
	// A different project must not see it.
	hits, err = NewRetriever(st).Query(ctx, QueryOptions{
		Query: "token bucket", Scope: Scope{ProjectID: "p-2"}, TopK: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range hits {
		if hit.Content == memo {
			t.Fatal("memo leaked across project scope")
		}
	}
}
