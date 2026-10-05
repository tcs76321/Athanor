package mce

import (
	"context"
	"testing"

	"github.com/tcs76321/athanor/internal/store"
)

// seedProject inserts the project parent a manifest row references.
func seedProject(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if _, err := st.DB().ExecContext(context.Background(),
		`INSERT INTO projects (id, name, archetype, goal) VALUES (?, ?, 'code', 'build things that last')`,
		id, "name-"+id); err != nil {
		t.Fatalf("seed project %s: %v", id, err)
	}
}

func TestManifestGetMissing(t *testing.T) {
	_, st := newStore(t)
	m := NewIndexManifest(st)
	_, ok, err := m.Get(context.Background(), "p1", "main.go")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatal("Get(missing) ok = true, want false")
	}
}

func TestManifestPutGetRoundTrip(t *testing.T) {
	_, st := newStore(t)
	m := NewIndexManifest(st)
	ctx := context.Background()
	seedProject(t, st, "p1")

	in := SourceRow{
		ProjectID: "p1", RelPath: "internal/a.go", Lang: "go",
		Size: 42, MTimeUnix: 1700000000, SourceHash: "hash-a", Chunks: 3,
		Status: "ok",
	}
	if err := m.Put(ctx, in); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := m.Get(ctx, "p1", "internal/a.go")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got != in {
		t.Fatalf("Get = %+v, want %+v", got, in)
	}
	if got.Status != "ok" {
		t.Errorf("default status = %q, want ok", got.Status)
	}
}

func TestManifestPutUpserts(t *testing.T) {
	_, st := newStore(t)
	m := NewIndexManifest(st)
	ctx := context.Background()
	seedProject(t, st, "p1")

	if err := m.Put(ctx, SourceRow{ProjectID: "p1", RelPath: "a.go", Lang: "go", Size: 1, MTimeUnix: 1, SourceHash: "h1", Chunks: 1}); err != nil {
		t.Fatal(err)
	}
	// Same path, new size/hash/chunks/status.
	if err := m.Put(ctx, SourceRow{ProjectID: "p1", RelPath: "a.go", Lang: "go", Size: 2, MTimeUnix: 2, SourceHash: "h2", Chunks: 5, Status: "failed", Error: "boom"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := m.Get(ctx, "p1", "a.go")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	want := SourceRow{ProjectID: "p1", RelPath: "a.go", Lang: "go", Size: 2, MTimeUnix: 2, SourceHash: "h2", Chunks: 5, Status: "failed", Error: "boom"}
	if got != want {
		t.Fatalf("Get = %+v, want %+v", got, want)
	}
	rows, err := m.List(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("List len = %d, want 1 (upsert, not insert)", len(rows))
	}
}

func TestManifestListScopedAndOrdered(t *testing.T) {
	_, st := newStore(t)
	m := NewIndexManifest(st)
	ctx := context.Background()
	seedProject(t, st, "p1")
	seedProject(t, st, "p2")

	for _, r := range []SourceRow{
		{ProjectID: "p1", RelPath: "z.go", Lang: "go", Size: 1, MTimeUnix: 1, SourceHash: "z"},
		{ProjectID: "p1", RelPath: "a.go", Lang: "go", Size: 1, MTimeUnix: 1, SourceHash: "a"},
		{ProjectID: "p2", RelPath: "other.go", Lang: "go", Size: 1, MTimeUnix: 1, SourceHash: "o"},
	} {
		if err := m.Put(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := m.List(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("List(p1) len = %d, want 2", len(rows))
	}
	if rows[0].RelPath != "a.go" || rows[1].RelPath != "z.go" {
		t.Fatalf("List(p1) order = %q,%q, want a.go,z.go", rows[0].RelPath, rows[1].RelPath)
	}
	empty, err := m.List(ctx, "ghost")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("List(ghost) len = %d, want 0", len(empty))
	}
}

func TestManifestDelete(t *testing.T) {
	_, st := newStore(t)
	m := NewIndexManifest(st)
	ctx := context.Background()
	seedProject(t, st, "p1")

	if err := m.Put(ctx, SourceRow{ProjectID: "p1", RelPath: "a.go", Lang: "go", Size: 1, MTimeUnix: 1, SourceHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "p1", "a.go"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, err := m.Get(ctx, "p1", "a.go"); err != nil || ok {
		t.Fatalf("Get after Delete: ok=%v err=%v, want ok=false", ok, err)
	}
	// Deleting an absent row is a no-op.
	if err := m.Delete(ctx, "p1", "absent.go"); err != nil {
		t.Fatalf("Delete(absent): %v", err)
	}
}

func TestManifestPutValidation(t *testing.T) {
	_, st := newStore(t)
	m := NewIndexManifest(st)
	ctx := context.Background()
	if err := m.Put(ctx, SourceRow{RelPath: "a.go"}); err == nil {
		t.Error("Put without project_id accepted, want error")
	}
	if err := m.Put(ctx, SourceRow{ProjectID: "p1"}); err == nil {
		t.Error("Put without relpath accepted, want error")
	}
}
