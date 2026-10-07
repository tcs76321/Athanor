package engine

import (
	"context"
	"errors"
	"path"
	"testing"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

type fakeGit struct {
	calls   int
	repo    string
	branch  string
	rel     string
	msg     string
	content []byte
	sha     string
	err     error
	// tree form
	treeCalls int
	files     []toolenvelope.File
}

func (f *fakeGit) Commit(_ context.Context, repo, branch, rel string, content []byte, msg string) (string, error) {
	f.calls++
	f.repo, f.branch, f.rel, f.content, f.msg = repo, branch, rel, content, msg
	return f.sha, f.err
}

func (f *fakeGit) CommitTree(_ context.Context, repo, branch string, files []toolenvelope.File, msg string) (string, error) {
	f.treeCalls++
	f.repo, f.branch, f.files, f.msg = repo, branch, files, msg
	return f.sha, f.err
}

// acceptedArtifact creates a real project (the artifacts.project_id FK
// requires it) and promotes a draft to accepted.
func acceptedArtifact(t *testing.T, e *testEnv) (project.Project, artifact.Artifact) {
	t.Helper()
	ctx := context.Background()
	p, _, err := e.projects.Create(ctx, "git-"+t.Name(), "code",
		"build a thing that lasts a very long time", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	art, err := e.artifacts.CreateDraft(ctx, p.ID, artifact.KindCode, []byte("package main"))
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []artifact.Status{artifact.StatusCandidate, artifact.StatusAccepted} {
		if err := e.artifacts.SetStatus(ctx, art.ID, st); err != nil {
			t.Fatalf("promoting artifact to %s: %v", st, err)
		}
	}
	return p, art
}

func TestRecordGitCommitRecordsSHA(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p, art := acceptedArtifact(t, e)

	f := &fakeGit{sha: "deadbeef"}
	e.eng.SetGitCommitter(f)
	e.eng.recordGitCommit(ctx, project.Project{ID: p.ID, RepositoryPath: "/repo"}, art)

	if f.calls != 1 {
		t.Fatalf("git calls = %d, want 1", f.calls)
	}
	if f.repo != "/repo" || f.branch != "athanor/"+p.ID {
		t.Errorf("git got repo=%q branch=%q, want /repo athanor/%s", f.repo, f.branch, p.ID)
	}
	if want := path.Join(".athanor", "artifacts", "code", art.ID); f.rel != want {
		t.Errorf("git relPath = %q, want %q", f.rel, want)
	}
	if string(f.content) != "package main" {
		t.Errorf("git content = %q, want package main", f.content)
	}
	got, err := e.artifacts.Get(ctx, art.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.GitCommit != "deadbeef" {
		t.Errorf("artifact git_commit = %q, want deadbeef", got.GitCommit)
	}
}

// TestRecordGitCommitTreeForMultiFile proves ADR-0065 §5: a multi-file code
// artifact commits as a tree at its relative paths, not a managed blob.
func TestRecordGitCommitTreeForMultiFile(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p, _, err := e.projects.Create(ctx, "git-tree-"+t.Name(), "code",
		"build a multi-file thing that lasts", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	art, err := e.artifacts.CreateDraft(ctx, p.ID, artifact.KindCode,
		[]byte("=== FILE: a.py ===\nx\n=== FILE: b.py ===\ny\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []artifact.Status{artifact.StatusCandidate, artifact.StatusAccepted} {
		if err := e.artifacts.SetStatus(ctx, art.ID, st); err != nil {
			t.Fatal(err)
		}
	}

	f := &fakeGit{sha: "treeface"}
	e.eng.SetGitCommitter(f)
	e.eng.recordGitCommit(ctx, project.Project{ID: p.ID, RepositoryPath: "/repo"}, art)

	if f.treeCalls != 1 || f.calls != 0 {
		t.Fatalf("treeCalls=%d calls=%d, want the tree path once", f.treeCalls, f.calls)
	}
	if len(f.files) != 2 || f.files[0].Path != "a.py" || f.files[1].Path != "b.py" {
		t.Errorf("committed files = %+v, want a.py then b.py", f.files)
	}
	got, _ := e.artifacts.Get(ctx, art.ID)
	if got.GitCommit != "treeface" {
		t.Errorf("git_commit = %q, want treeface", got.GitCommit)
	}
}

func TestRecordGitCommitSkipsWithoutRepository(t *testing.T) {
	e := newEnv(t)
	p, art := acceptedArtifact(t, e)
	f := &fakeGit{sha: "x"}
	e.eng.SetGitCommitter(f)
	e.eng.recordGitCommit(context.Background(), project.Project{ID: p.ID}, art)

	if f.calls != 0 {
		t.Errorf("git called %d times with no repository_path, want 0", f.calls)
	}
	got, _ := e.artifacts.Get(context.Background(), art.ID)
	if got.GitCommit != "" {
		t.Errorf("git_commit = %q, want empty on skip", got.GitCommit)
	}
}

func TestRecordGitCommitFailureIsNonFatal(t *testing.T) {
	e := newEnv(t)
	p, art := acceptedArtifact(t, e)
	e.eng.SetGitCommitter(&fakeGit{err: errors.New("git exploded")})
	e.eng.recordGitCommit(context.Background(),
		project.Project{ID: p.ID, RepositoryPath: "/repo"}, art)

	// The artifact stays accepted and simply has no commit recorded.
	got, err := e.artifacts.Get(context.Background(), art.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != artifact.StatusAccepted {
		t.Errorf("status = %s, want accepted (git failure is non-fatal)", got.Status)
	}
	if got.GitCommit != "" {
		t.Errorf("git_commit = %q, want empty after git failure", got.GitCommit)
	}
}
