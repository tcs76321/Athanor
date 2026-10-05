package artifact

import (
	"context"
	"testing"
)

// TestSetGitCommit proves migration 0015 / §9.2: a fresh artifact has no
// git_commit, SetGitCommit records one and round-trips it, and re-recording
// overwrites (an artifact is committed once, but a retry after a transient
// Git failure must be able to write the real SHA).
func TestSetGitCommit(t *testing.T) {
	a, _, _ := openStore(t)
	ctx := context.Background()

	art, err := a.CreateDraft(ctx, "p1", KindCode, []byte("package main"))
	if err != nil {
		t.Fatal(err)
	}
	if art.GitCommit != "" {
		t.Fatalf("fresh artifact git_commit = %q, want empty", art.GitCommit)
	}

	if err := a.SetGitCommit(ctx, art.ID, "abc123"); err != nil {
		t.Fatalf("SetGitCommit: %v", err)
	}
	got, err := a.Get(ctx, art.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.GitCommit != "abc123" {
		t.Errorf("git_commit = %q, want abc123", got.GitCommit)
	}

	// Overwrite (retry path).
	if err := a.SetGitCommit(ctx, art.ID, "def456"); err != nil {
		t.Fatalf("SetGitCommit overwrite: %v", err)
	}
	got, err = a.Get(ctx, art.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.GitCommit != "def456" {
		t.Errorf("git_commit after overwrite = %q, want def456", got.GitCommit)
	}

	// Unknown artifact is ErrNotFound, not a silent no-op.
	if err := a.SetGitCommit(ctx, "ghost", "sha"); err == nil {
		t.Error("SetGitCommit(ghost) succeeded, want ErrNotFound")
	}
}
