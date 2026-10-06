package mce

import (
	"context"
	"testing"
)

// TestIndexForProject covers the F5 (ADR-0059) project-scoped Dormant Index
// read: job-attributed chunks are excluded, an empty project yields nothing,
// the limit caps the rows, and a full-text query ranks by summary.
func TestIndexForProject(t *testing.T) {
	cs, st := newStore(t)
	ctx := context.Background()

	projectChunks := divide(t, "widget.go", "go", []byte("package widget\n\nfunc W() {}\n\nfunc X() {}\n"))
	jobChunks := divide(t, "job.go", "go", []byte("package job\n\nfunc J() {}\n"))
	if _, err := cs.PutSource(ctx, SourceRef{RelPath: "widget.go", ProjectID: "p1"}, projectChunks); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.PutSource(ctx, SourceRef{RelPath: "job.go", ProjectID: "p1", JobID: "j1"}, jobChunks); err != nil {
		t.Fatal(err)
	}

	// No query: recent project rows, job rows excluded.
	got, err := cs.IndexForProject(ctx, "p1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(projectChunks) {
		t.Fatalf("project rows = %d, want %d (job rows must be excluded)", len(got), len(projectChunks))
	}
	for _, e := range got {
		if e.SourceRelPath != "widget.go" {
			t.Errorf("row path = %q, want widget.go", e.SourceRelPath)
		}
	}

	// Empty project yields nothing rather than every project's rows.
	if empty, err := cs.IndexForProject(ctx, "", "", 10); err != nil || empty != nil {
		t.Errorf("empty project = (%v, %v), want (nil, nil)", empty, err)
	}

	// Limit caps the number of rows.
	if limited, err := cs.IndexForProject(ctx, "p1", "", 1); err != nil || len(limited) != 1 {
		t.Errorf("limited rows = (%d, %v), want 1", len(limited), err)
	}

	// Full-text ranking: a summary match sorts first. The external-content
	// FTS index syncs on UPDATE of dormant_index.
	target := projectChunks[len(projectChunks)-1].ID
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE dormant_index SET summary = ?, summary_status = 'ready' WHERE chunk_id = ?`,
		"needle widget chunk", target); err != nil {
		t.Fatal(err)
	}
	ranked, err := cs.IndexForProject(ctx, "p1", "needle", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) == 0 || ranked[0].ChunkID != target {
		t.Fatalf("ranked rows = %+v, want the needle chunk %s first", ranked, target)
	}
}
