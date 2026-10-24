package main

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// maxRepoSnapshotBytes bounds one staged repository snapshot (ADR-0082).
// A larger repository is reported rather than silently truncated; the operator
// can index it and work on a narrower task.
const maxRepoSnapshotBytes = 256 << 20

// ErrDirtyWorktree is returned when the project repository has uncommitted
// changes. Git-as-undo refuses to run rather than disturb work it did not
// create; the caller audits the skip and leaves the artifact accepted in
// SQLite.
var ErrDirtyWorktree = errors.New("git: worktree has uncommitted changes")

// ErrNotRepoRoot is returned when repository_path is not itself the root of a
// git worktree — e.g. it is a subdirectory of a larger repository (or a plain
// directory inside one). Git walks up to the nearest .git, so operating there
// would branch/commit the *parent* repository — mutating a repo the operator
// never pointed us at. Git-as-undo refuses instead; the caller audits the skip.
var ErrNotRepoRoot = errors.New("git: repository path is not a git worktree root")

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

// commitFile is one repository-relative file to write into the ephemeral
// worktree for a commit.
type commitFile struct {
	rel     string
	content []byte
}

// Commit writes content to relPath on branch, commits it, and returns the
// commit SHA. It creates the branch when absent. The project worktree must be
// clean; the commit lands in an ephemeral worktree (ADR-0071 §4), so the
// project's own HEAD and working tree are never touched.
func (gitClient) Commit(ctx context.Context, repoPath, branch, relPath string, content []byte, message string) (string, error) {
	if repoPath == "" {
		return "", errors.New("git: empty repository path")
	}
	if branch == "" || relPath == "" {
		return "", errors.New("git: branch and relPath are required")
	}
	return gitCommitOnBranch(ctx, repoPath, branch, []commitFile{{rel: relPath, content: content}}, message)
}

// Snapshot stages the tracked files at HEAD into dest for a Job Pod's
// read-only repository mount (ADR-0082). It is not a working-tree copy: git
// archive HEAD is deterministic, excludes untracked/ignored files, and never
// carries .git. Secret-shaped files are skipped, symlinks/devices are skipped
// (a repository symlink must not become a host escape), and the archive is
// size-capped. The caller owns dest and removes it at terminal state.
func (gitClient) Snapshot(ctx context.Context, repoPath, dest string) error {
	if repoPath == "" || dest == "" {
		return errors.New("git snapshot: repoPath and dest are required")
	}
	if err := validateRepoRoot(ctx, repoPath); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("git snapshot: creating %s: %w", dest, err)
	}
	parent, err := os.MkdirTemp("", "athanor-snapshot-")
	if err != nil {
		return fmt.Errorf("git snapshot: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(parent) }()
	tarPath := filepath.Join(parent, "repo.tar")
	if err := gitRun(ctx, repoPath, "archive", "--format=tar", "-o", tarPath, "HEAD"); err != nil {
		return fmt.Errorf("git snapshot: archiving HEAD: %w", err)
	}
	fi, err := os.Stat(tarPath)
	if err != nil {
		return fmt.Errorf("git snapshot: stat archive: %w", err)
	}
	if fi.Size() > maxRepoSnapshotBytes {
		return fmt.Errorf("git snapshot: repository archive is %d bytes, over the %d-byte cap (ADR-0082)", fi.Size(), int64(maxRepoSnapshotBytes))
	}
	if err := extractSnapshot(tarPath, dest); err != nil {
		_ = os.RemoveAll(dest)
		return err
	}
	return nil
}

// validateRepoRoot requires repoPath to be the root of a git worktree (the
// same root rule git-as-undo uses). A snapshot is taken from HEAD, so an
// uncommitted working tree is not an error.
func validateRepoRoot(ctx context.Context, repoPath string) error {
	toplevel, err := gitOutput(ctx, repoPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("git: %s is not a worktree: %w", repoPath, err)
	}
	if !samePath(strings.TrimSpace(toplevel), repoPath) {
		return fmt.Errorf("%w: %s (top-level is %s)", ErrNotRepoRoot, repoPath, strings.TrimSpace(toplevel))
	}
	return nil
}

// extractSnapshot unpacks a git archive into dest with path containment and
// the ADR-0082 exclusions. Only regular files and directories are written.
func extractSnapshot(tarPath, dest string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("git snapshot: opening archive: %w", err)
	}
	defer func() { _ = f.Close() }()
	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("git snapshot: reading archive: %w", err)
		}
		name := filepath.Clean(hdr.Name)
		if !filepath.IsLocal(name) || name == "." {
			return fmt.Errorf("git snapshot: unsafe archive path %q", hdr.Name)
		}
		if skipSnapshotPath(name) {
			continue
		}
		target := filepath.Join(dest, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("git snapshot: mkdir %s: %w", name, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("git snapshot: mkdir for %s: %w", name, err)
			}
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
			if err != nil {
				return fmt.Errorf("git snapshot: create %s: %w", name, err)
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return fmt.Errorf("git snapshot: write %s: %w", name, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("git snapshot: close %s: %w", name, closeErr)
			}
		default:
			// Symlinks, devices, FIFOs, and git metadata entries are not
			// materialized. A repository cannot turn a snapshot into a host
			// escape or a special-file read.
		}
	}
}

// skipSnapshotPath excludes secret-shaped files from a snapshot. It is a
// conservative default, not a substitute for not committing secrets.
func skipSnapshotPath(name string) bool {
	base := path.Base(name)
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return true
	}
	switch {
	case strings.HasSuffix(base, ".pem"), strings.HasSuffix(base, ".key"),
		strings.HasSuffix(base, ".p12"), strings.HasSuffix(base, ".pfx"):
		return true
	}
	return base == "id_rsa" || base == "id_ed25519" || strings.HasSuffix(base, ".id_rsa")
}

// CommitTree writes every file (sanitized repository-relative paths) into the
// repository and makes one commit on branch (ADR-0065 §5). It is the
// multi-file form of Commit: accepted source lands in the repo at its real
// paths rather than in a managed blob namespace. The project worktree must be
// clean; the commit lands in an ephemeral worktree (ADR-0071 §4).
func (gitClient) CommitTree(ctx context.Context, repoPath, branch string, files []toolenvelope.File, message string) (string, error) {
	if repoPath == "" {
		return "", errors.New("git: empty repository path")
	}
	if branch == "" {
		return "", errors.New("git: branch is required")
	}
	if len(files) == 0 {
		return "", errors.New("git: no files to commit")
	}
	checked := make([]commitFile, 0, len(files))
	for _, f := range files {
		rel, err := toolenvelope.SanitizeRelPath(f.Path)
		if err != nil {
			return "", fmt.Errorf("git: unsafe path: %w", err)
		}
		checked = append(checked, commitFile{rel: rel, content: []byte(f.Content)})
	}
	return gitCommitOnBranch(ctx, repoPath, branch, checked, message)
}

// gitCommitOnBranch validates the repository and commits files on branch
// through an ephemeral worktree, returning the commit SHA. The worktree is
// created from HEAD (or the existing branch) at a temp path, written,
// committed, and removed — so the project's checked-out branch and working tree
// are never disturbed (ADR-0071 §4). On any failure the worktree is removed and
// the error returned.
func gitCommitOnBranch(ctx context.Context, repoPath, branch string, files []commitFile, message string) (string, error) {
	if err := validateRepo(ctx, repoPath); err != nil {
		return "", err
	}
	wt, err := addWorktree(ctx, repoPath, branch)
	if err != nil {
		return "", err
	}
	defer removeWorktree(ctx, repoPath, wt)
	for _, f := range files {
		dst := filepath.Join(wt, filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return "", fmt.Errorf("git: creating dir for %s: %w", f.rel, err)
		}
		if err := os.WriteFile(dst, f.content, 0o600); err != nil {
			return "", fmt.Errorf("git: writing %s: %w", f.rel, err)
		}
		if err := gitRun(ctx, wt, "add", "--", f.rel); err != nil {
			return "", err
		}
	}
	if err := gitRun(ctx, wt, "commit", "--no-gpg-sign", "-m", message); err != nil {
		return "", err
	}
	sha, err := gitOutput(ctx, wt, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(sha), nil
}

// validateRepo requires repoPath to be the root of a clean git worktree.
//
// The root check is load-bearing: git resolves the nearest ancestor .git, so a
// repository_path that is a subdirectory of a larger repo would otherwise cause
// us to create branches and commits in that larger repo (the 0.0.0-soak bug).
// The worktree top level must equal repoPath, canonicalised; a non-root path
// fails closed with ErrNotRepoRoot, a dirty one with ErrDirtyWorktree.
func validateRepo(ctx context.Context, repoPath string) error {
	toplevel, err := gitOutput(ctx, repoPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("git: %s is not a worktree: %w", repoPath, err)
	}
	if !samePath(strings.TrimSpace(toplevel), repoPath) {
		return fmt.Errorf("%w: %s (top-level is %s)", ErrNotRepoRoot, repoPath, strings.TrimSpace(toplevel))
	}
	status, err := gitOutput(ctx, repoPath, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return ErrDirtyWorktree
	}
	return nil
}

// addWorktree creates a temporary worktree checked out on branch (created from
// HEAD when absent) and returns its path. The caller must removeWorktree it.
func addWorktree(ctx context.Context, repoPath, branch string) (string, error) {
	parent, err := os.MkdirTemp("", "athanor-git-")
	if err != nil {
		return "", fmt.Errorf("git: temp dir for worktree: %w", err)
	}
	path := filepath.Join(parent, "wt")
	args := []string{"worktree", "add", "--quiet"}
	if branchExists(ctx, repoPath, branch) {
		args = append(args, path, branch)
	} else {
		args = append(args, "-b", branch, path)
	}
	if err := gitRun(ctx, repoPath, args...); err != nil {
		_ = os.RemoveAll(parent)
		return "", err
	}
	return path, nil
}

// removeWorktree force-removes an ephemeral worktree and its temp parent. It is
// best-effort: the commit has already landed on the branch, so a leftover temp
// dir is harmless. Removing the worktree also drops git's .git/worktrees entry.
func removeWorktree(ctx context.Context, repoPath, wt string) {
	_ = gitRun(ctx, repoPath, "worktree", "remove", "--force", wt)
	_ = os.RemoveAll(filepath.Dir(wt))
}

// branchExists reports whether refs/heads/branch exists in repo.
func branchExists(ctx context.Context, repoPath, branch string) bool {
	return gitRun(ctx, repoPath, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch) == nil
}

// samePath reports whether two paths name the same location after resolving
// symlinks (macOS /var → /private/var) and making both absolute.
func samePath(a, b string) bool {
	canon := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		return p
	}
	return canon(a) == canon(b)
}

// noPromptEnv is the process environment with git's terminal prompt disabled.
// An unattended daemon cannot answer a credential prompt, so a prompt would be
// a hang; failing loudly is the only acceptable outcome (ADR-0071 §3).
func noPromptEnv() []string {
	return append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
}

// gitRun runs a git subcommand in repo and discards stdout. Stderr is
// captured for the error message. Prompts are disabled (ADR-0071 §3).
func gitRun(ctx context.Context, repo string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	cmd.Env = noPromptEnv()
	cmd.Stdout = io.Discard
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// gitOutput runs a git subcommand in repo and returns its stdout. Prompts are
// disabled (ADR-0071 §3).
func gitOutput(ctx context.Context, repo string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	cmd.Env = noPromptEnv()
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
