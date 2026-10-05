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

	rel := ".athanor/artifacts/code/ar1"
	sha, err := (gitClient{}).Commit(ctx, repo, "athanor/p1", rel, []byte("package main"), "athanor: accept code ar1")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(sha) != 40 {
		t.Errorf("sha = %q, want 40-hex", sha)
	}
	if branch := gitTestOutput(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); branch != "athanor/p1" {
		t.Errorf("HEAD branch = %q, want athanor/p1", branch)
	}
	got, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
	if err != nil || string(got) != "package main" {
		t.Errorf("committed content = %q err=%v", got, err)
	}
	if status := gitTestOutput(t, repo, "status", "--porcelain"); status != "" {
		t.Errorf("worktree dirty after commit: %q", status)
	}

	// A second commit on the existing branch succeeds.
	if _, err := (gitClient{}).Commit(ctx, repo, "athanor/p1", ".athanor/artifacts/code/ar2", []byte("v2"), "accept ar2"); err != nil {
		t.Fatalf("second Commit: %v", err)
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

func gitTestOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}
