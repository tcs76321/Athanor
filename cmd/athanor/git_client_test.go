package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Athanor Test")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return dir
}

func TestGitClientCommitCreatesBranchAndCommit(t *testing.T) {
	requireGit(t)
	repo := initTestRepo(t)
	ctx := context.Background()
	before := gitTestOutput(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	rel := ".athanor/artifacts/code/ar1"
	sha, err := (gitClient{}).Commit(ctx, repo, "athanor/p1", rel, []byte("package main"), "athanor: accept code ar1")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(sha) != 40 {
		t.Errorf("sha = %q, want 40-hex", sha)
	}
	// The commit lands on the agent branch, not the checked-out one, and the
	// project's HEAD must not move (ADR-0071 §4).
	if got := gitTestOutput(t, repo, "show", "athanor/p1:"+rel); got != "package main" {
		t.Errorf("branch content = %q, want %q", got, "package main")
	}
	if now := gitTestOutput(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); now != before {
		t.Errorf("HEAD moved %q → %q; git-as-undo must not disturb the checkout", before, now)
	}
	if status := gitTestOutput(t, repo, "status", "--porcelain"); status != "" {
		t.Errorf("worktree dirty after commit: %q", status)
	}

	// A second commit on the existing branch succeeds and advances it without
	// dropping the first commit.
	sha2, err := (gitClient{}).Commit(ctx, repo, "athanor/p1", ".athanor/artifacts/code/ar2", []byte("v2"), "accept ar2")
	if err != nil {
		t.Fatalf("second Commit: %v", err)
	}
	if sha2 == sha {
		t.Errorf("second commit did not advance the branch (sha %s unchanged)", sha)
	}
	if got := gitTestOutput(t, repo, "show", "athanor/p1:"+rel); got != "package main" {
		t.Errorf("first commit lost after second: %q", got)
	}
	// The ephemeral worktree must be gone: only the project's own worktree
	// remains, or a long run would accumulate stale .git/worktrees entries.
	if list := gitTestOutput(t, repo, "worktree", "list"); strings.Contains(list, "\n") {
		t.Errorf("ephemeral worktree left behind:\n%s", list)
	}
}

func TestGitClientRefusesDirtyWorktree(t *testing.T) {
	requireGit(t)
	repo := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := (gitClient{}).Commit(context.Background(), repo, "athanor/p1", "x", []byte("y"), "m")
	if !errors.Is(err, ErrDirtyWorktree) {
		t.Fatalf("err = %v, want ErrDirtyWorktree", err)
	}
}

func TestGitClientRejectsNonRepoAndEmptyPath(t *testing.T) {
	requireGit(t)
	if _, err := (gitClient{}).Commit(context.Background(), "", "b", "r", nil, "m"); err == nil {
		t.Error("empty repoPath accepted")
	}
	if _, err := (gitClient{}).Commit(context.Background(), t.TempDir(), "b", "r", nil, "m"); err == nil {
		t.Error("non-git dir accepted")
	}
}

// TestGitClientRefusesSubdirectoryOfRepo is the containment regression for the
// 0.0.0-soak bug: a repository_path that is a subdirectory of a larger repo
// (git resolves the nearest ancestor .git) must NOT cause Git-as-undo to create
// branches or commits in that parent repo. It fails closed with ErrNotRepoRoot
// and leaves the parent untouched.
func TestGitClientRefusesSubdirectoryOfRepo(t *testing.T) {
	requireGit(t)
	repo := initTestRepo(t)
	before := gitTestOutput(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	sub := filepath.Join(repo, "spikes", "results", "repos", "fixture")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "input.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := (gitClient{}).Commit(context.Background(), sub, "athanor/proj", "out", []byte("y"), "m")
	if !errors.Is(err, ErrNotRepoRoot) {
		t.Fatalf("err = %v, want ErrNotRepoRoot", err)
	}
	if out := gitTestOutput(t, repo, "branch", "--list", "athanor/proj"); out != "" {
		t.Errorf("parent repo gained branch %q — Git-as-undo leaked into the parent", out)
	}
	if after := gitTestOutput(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); after != before {
		t.Errorf("parent HEAD moved %q → %q", before, after)
	}
}

func gitTestOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}
