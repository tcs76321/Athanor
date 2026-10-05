package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrDirtyWorktree is returned when the project repository has uncommitted
// changes. Git-as-undo refuses to run rather than disturb work it did not
// create; the caller audits the skip and leaves the artifact accepted in
// SQLite.
var ErrDirtyWorktree = errors.New("git: worktree has uncommitted changes")

// gitClient records an accepted artifact to a project repository on an
// agent-created branch (§14; ADR-0030) and, on an approved HITL request,
// pushes the agent branch (M6-T5; ADR-0036). It is the only place in the
// binary that shells out to `git`; Gate G1 allowlists this file for
// os/exec, the same named-file exception the production Podman client uses.
//
// The methods are deliberately mechanical: the engine decides the branch and
// the managed-namespace path, this adapter runs the porcelain. It never
// pushes without an approval and never rewrites history.
type gitClient struct {
	// push is the command runner used by Push; nil selects gitRun. It is
	// injectable so a test can assert the push argv without a real remote.
	push func(ctx context.Context, repo string, args ...string) error
}

// Push pushes branch to remote (M6-T5, ADR-0036). It is only ever called
// from an approved HITL request; rejection and expiry never reach it.
func (g gitClient) Push(ctx context.Context, repoPath, remote, branch string) error {
	if repoPath == "" || remote == "" || branch == "" {
		return errors.New("git: repoPath, remote, and branch are required")
	}
	run := g.push
	if run == nil {
		run = gitRun
	}
	return run(ctx, repoPath, "push", remote, branch)
}

// Commit writes content to relPath on branch, commits it, and returns the
// commit SHA. It creates the branch when absent. The worktree must be clean.
func (gitClient) Commit(ctx context.Context, repoPath, branch, relPath string, content []byte, message string) (string, error) {
	if repoPath == "" {
		return "", errors.New("git: empty repository path")
	}
	if branch == "" || relPath == "" {
		return "", errors.New("git: branch and relPath are required")
	}
	if err := gitRun(ctx, repoPath, "rev-parse", "--is-inside-work-tree"); err != nil {
		return "", fmt.Errorf("git: %s is not a worktree: %w", repoPath, err)
	}
	status, err := gitOutput(ctx, repoPath, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(status) != "" {
		return "", ErrDirtyWorktree
	}

	// Create the agent branch if it does not exist, else check it out.
	if err := gitRun(ctx, repoPath, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		if err := gitRun(ctx, repoPath, "checkout", "-b", branch); err != nil {
			return "", err
		}
	} else if err := gitRun(ctx, repoPath, "checkout", branch); err != nil {
		return "", err
	}

	dst := filepath.Join(repoPath, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", fmt.Errorf("git: creating artifact dir: %w", err)
	}
	if err := os.WriteFile(dst, content, 0o600); err != nil {
		return "", fmt.Errorf("git: writing artifact: %w", err)
	}
	if err := gitRun(ctx, repoPath, "add", "--", relPath); err != nil {
		return "", err
	}
	if err := gitRun(ctx, repoPath, "commit", "--no-gpg-sign", "-m", message); err != nil {
		return "", err
	}
	sha, err := gitOutput(ctx, repoPath, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(sha), nil
}

// gitRun runs a git subcommand in repo and discards stdout. Stderr is
// captured for the error message.
func gitRun(ctx context.Context, repo string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	cmd.Stdout = io.Discard
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// gitOutput runs a git subcommand in repo and returns its stdout.
func gitOutput(ctx context.Context, repo string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
