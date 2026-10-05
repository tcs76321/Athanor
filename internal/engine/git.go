package engine

import (
	"context"
	"fmt"
	"path"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/project"
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
	// Managed namespace + agent branch (ADR-0030 §Decision). The path is
	// git-relative, so path.Join (not filepath.Join) keeps it portable.
	branch := "athanor/" + p.ID
	rel := path.Join(".athanor", "artifacts", string(art.Kind), art.ID)
	message := fmt.Sprintf("athanor: accept %s %s", art.Kind, art.ID)

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
