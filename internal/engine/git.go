package engine

import (
	"context"
	"fmt"
	"path"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// recordGitCommit is §14 Git-as-undo (F3-T5, ADR-0030): after an artifact
// is accepted, record its content to the project repository on an
// agent-created branch and store the commit SHA on the artifact.
//
// It is best-effort. A project with no repository, a nil git seam, a dirty
// worktree, or a git failure is audited and the acceptance still stands —
// the artifact is accepted in SQLite; there is simply no undo point. The
// alternative (failing the job after acceptance) would leave a rejected job
// with an accepted artifact, which is worse.
func (e *Engine) recordGitCommit(ctx context.Context, p project.Project, art artifact.Artifact) {
	if e.git == nil {
		return
	}
	if p.RepositoryPath == "" {
		e.auditCat(ctx, art.JobID, "jobs", map[string]any{
			"event": "git_commit_skipped", "artifact": art.ID, "reason": "no_repository_path",
		})
		return
	}
	content, err := e.artifacts.ReadContent(ctx, art.ID)
	if err != nil {
		e.auditCat(ctx, art.JobID, "jobs", map[string]any{
			"event": "git_commit_skipped", "artifact": art.ID,
			"reason": "read_content", "error": err.Error(),
		})
		return
	}
	// Agent branch (ADR-0030 §Decision).
	branch := "athanor/" + p.ID
	message := fmt.Sprintf("athanor: accept %s %s", art.Kind, art.ID)

	// ADR-0065 §5: a multi-file code candidate commits its files at their
	// repository-relative paths, one atomic commit, so the accepted source
	// really lands in the repo. Anything else (a single file, non-code) keeps
	// the managed-namespace blob commit.
	//
	// A candidate may not use this path to commit changes to the verification
	// harness. Only implementation files are eligible; harness files are
	// filtered and audited, and a harness-only artifact is skipped entirely.
	//
	// Tree interpretation is code-only: a document or dataset that happens to
	// contain file markers must stay in the managed blob namespace rather than
	// writing repository paths.
	if art.Kind == artifact.KindCode {
		if files, ferr := candidateFiles(string(content)); ferr == nil {
			implementation, harnessFiles := toolenvelope.SplitHarnessFiles(files)
			if len(harnessFiles) > 0 {
				for _, f := range harnessFiles {
					e.auditCat(ctx, art.JobID, "jobs", map[string]any{
						"event": "harness_path_ignored", "path": f.Path,
						"reason": "accepted artifact tried to modify the verification harness",
					})
				}
			}
			if len(implementation) == 0 {
				e.auditCat(ctx, art.JobID, "jobs", map[string]any{
					"event": "git_commit_skipped", "artifact": art.ID,
					"reason": "no implementation files outside the verification harness",
				})
				return
			}
			if len(implementation) > 1 || len(harnessFiles) > 0 {
				sha, cerr := e.git.CommitTree(ctx, p.RepositoryPath, branch, implementation, message)
				if cerr != nil {
					e.auditCat(ctx, art.JobID, "jobs", map[string]any{
						"event": "git_commit_failed", "artifact": art.ID, "branch": branch, "error": cerr.Error(),
					})
					return
				}
				if err := e.artifacts.SetGitCommit(ctx, art.ID, sha); err != nil {
					e.auditCat(ctx, art.JobID, "jobs", map[string]any{
						"event": "git_commit_record_failed", "artifact": art.ID, "commit": sha, "error": err.Error(),
					})
					return
				}
				e.auditCat(ctx, art.JobID, "jobs", map[string]any{
					"event": "git_committed", "artifact": art.ID, "commit": sha, "branch": branch, "files": len(implementation),
				})
				return
			}
		}
	}

	// Managed namespace + agent branch. The path is git-relative, so
	// path.Join (not filepath.Join) keeps it portable.
	rel := path.Join(".athanor", "artifacts", string(art.Kind), art.ID)
	sha, err := e.git.Commit(ctx, p.RepositoryPath, branch, rel, content, message)
	if err != nil {
		e.auditCat(ctx, art.JobID, "jobs", map[string]any{
			"event": "git_commit_failed", "artifact": art.ID, "branch": branch, "error": err.Error(),
		})
		return
	}
	if err := e.artifacts.SetGitCommit(ctx, art.ID, sha); err != nil {
		e.auditCat(ctx, art.JobID, "jobs", map[string]any{
			"event": "git_commit_record_failed", "artifact": art.ID, "commit": sha, "error": err.Error(),
		})
		return
	}
	e.auditCat(ctx, art.JobID, "jobs", map[string]any{
		"event": "git_committed", "artifact": art.ID, "commit": sha, "branch": branch, "path": rel,
	})
}
