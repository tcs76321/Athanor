package notebook

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func openNotebook(t *testing.T) *Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "notebook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	return New(st)
}

func TestAppendAndRecent(t *testing.T) {
	nb := openNotebook(t)
	ctx := context.Background()
	for _, e := range []Entry{
		{JobID: "job-1", ProjectID: "p-1", Kind: KindPlan, Phase: "planning", Content: "first"},
		{JobID: "job-1", ProjectID: "p-1", Kind: KindResult, Phase: "evaluating", Content: "second"},
		{JobID: "job-1", ProjectID: "p-1", Kind: KindDecision, Phase: "comparing", Content: "third"},
	} {
		if _, err := nb.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := nb.Recent(ctx, "job-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Content != "first" || got[2].Content != "third" {
		t.Fatalf("order = %v", []string{got[0].Content, got[1].Content, got[2].Content})
	}
	if got[0].ID == "" || got[0].Kind != KindPlan {
		t.Fatalf("entry = %+v", got[0])
	}
}

func TestRecentLimitKeepsNewest(t *testing.T) {
	nb := openNotebook(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := nb.Append(ctx, Entry{JobID: "j", ProjectID: "p", Kind: KindObservation, Content: strings.Repeat("x", i+1)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := nb.Recent(ctx, "j", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if len(got[0].Content) != 4 || len(got[1].Content) != 5 {
		t.Fatalf("kept = %d,%d, want the newest two", len(got[0].Content), len(got[1].Content))
	}
}

func TestAppendTrimsContent(t *testing.T) {
	nb := openNotebook(t)
	ctx := context.Background()
	long := strings.Repeat("a", MaxEntryBytes+100)
	e, err := nb.Append(ctx, Entry{JobID: "j", ProjectID: "p", Kind: KindObservation, Content: long})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Content) > MaxEntryBytes {
		t.Fatalf("content = %d bytes, want <= %d", len(e.Content), MaxEntryBytes)
	}
	if !strings.HasSuffix(e.Content, "…") {
		t.Fatalf("trim marker missing: %q", e.Content[len(e.Content)-8:])
	}
}

func TestAppendPrunesBeyondLimit(t *testing.T) {
	nb := openNotebook(t)
	ctx := context.Background()
	for i := 0; i < MaxEntriesPerJob+5; i++ {
		if _, err := nb.Append(ctx, Entry{JobID: "j", ProjectID: "p", Kind: KindResult, Content: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := nb.Recent(ctx, "j", MaxEntriesPerJob+10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxEntriesPerJob {
		t.Fatalf("len = %d, want %d", len(got), MaxEntriesPerJob)
	}
}

func TestAppendValidation(t *testing.T) {
	nb := openNotebook(t)
	ctx := context.Background()
	cases := []Entry{
		{ProjectID: "p", Kind: KindPlan, Content: "x"},
		{JobID: "j", Kind: KindPlan, Content: "x"},
		{JobID: "j", ProjectID: "p", Content: "x"},
		{JobID: "j", ProjectID: "p", Kind: KindPlan, Content: "  "},
	}
	for _, c := range cases {
		if _, err := nb.Append(ctx, c); err == nil {
			t.Errorf("Append(%+v) succeeded, want error", c)
		}
	}
}

func TestNilStoreIsSafe(t *testing.T) {
	var nb *Store
	if _, err := nb.Recent(context.Background(), "j", 1); err != nil {
		t.Fatalf("nil Recent: %v", err)
	}
	if _, err := nb.Append(context.Background(), Entry{}); err == nil {
		t.Fatal("nil Append succeeded")
	}
}
