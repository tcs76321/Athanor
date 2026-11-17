package coverage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func fixture(t *testing.T) (*Repo, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := store.Migrate(st.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	p, _, err := project.NewRepo(st).CreateInferred(context.Background(), "p", "daily news", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return NewRepo(st), p.ID
}

func TestRecordRecentAndKeys(t *testing.T) {
	repo, projectID := fixture(t)
	ctx := context.Background()
	if err := repo.Record(ctx, projectID, "news", []Item{
		{Key: "story-001", Title: "First", Sources: []string{"wire/day1/a.md"}},
		{Key: "story-002", Title: "Second"},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	// A second record advances last_seen and updates the title.
	if err := repo.Record(ctx, projectID, "news", []Item{{Key: "story-001", Title: "First (updated)"}}); err != nil {
		t.Fatal(err)
	}
	items, err := repo.Recent(ctx, projectID, "news", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("recent = %+v", items)
	}
	keys, err := repo.Keys(ctx, projectID, "news", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != "story-001" {
		t.Errorf("keys = %v (story-001 should be newest)", keys)
	}
	if items[0].Title != "First (updated)" {
		t.Errorf("title not updated: %+v", items[0])
	}
}

func TestNamespaceIsolation(t *testing.T) {
	repo, projectID := fixture(t)
	ctx := context.Background()
	if err := repo.Record(ctx, projectID, "news", []Item{{Key: "a"}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Record(ctx, projectID, "brief", []Item{{Key: "b"}}); err != nil {
		t.Fatal(err)
	}
	news, _ := repo.Keys(ctx, projectID, "news", 10)
	brief, _ := repo.Keys(ctx, projectID, "brief", 10)
	if len(news) != 1 || news[0] != "a" || len(brief) != 1 || brief[0] != "b" {
		t.Errorf("news=%v brief=%v", news, brief)
	}
}

func TestParseItems(t *testing.T) {
	text := "# Covered\n\nstory-001 was new.\n\n" + FormatItems([]Item{
		{Key: "story-001", Title: "First"},
		{Key: "story-002", Title: "Second"},
	})
	items := ParseItems(text)
	if len(items) != 2 || items[0].Key != "story-001" {
		t.Fatalf("items = %+v", items)
	}
	if got := ParseItems("no fence here"); got != nil {
		t.Errorf("no fence = %+v", got)
	}
	if got := ParseItems("```coverage\nnot json\n```"); got != nil {
		t.Errorf("bad json = %+v", got)
	}
	if got := ParseItems("```coverage\n{\"items\":[{\"id\":\"a\"},{\"id\":\"a\"},{\"key\":\"b\"}]}\n```"); len(got) != 2 {
		t.Errorf("dedupe = %+v", got)
	}
}
