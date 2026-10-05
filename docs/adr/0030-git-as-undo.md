# ADR 0030 — Git-as-undo on artifact acceptance (F3-T5)

**Status:** Accepted · **Date:** 2026-10-04 · **Refs:** ARCHITECTURE §9.2,
§13.1 Phase 7, §14, §22; ROADMAP F3-T5 and M3-T5; Invariant 6 ("Git as
undo"); ADR-0007 (Podman lifecycle), ADR-0024 (engine pod dispatch), Gate G1.

## Context

Invariant 6 and §14 step 10 require that code changes are atomic commits on
agent-created branches. The roadmap's M3-T5 only widened the closed tool set
with `git_operation`; no call site, no `artifacts.git_commit` column, and no
repository-and-branch convention existed. An accepted artifact was recorded in
SQLite and exported, but nothing made it **undoable in Git**.

Three constraints shape the design.

1. **Job Pods cannot do this.** Pods run `--network=none` and see no host
   filesystem beyond approved mounts; the project repository is not one of
   them. Git is therefore a **Core-side** operation.
2. **`os/exec` is confined.** Gate G1 forbids `os/exec` in `internal/` and
   permits it in `cmd/` only for named files. The production Podman client
   (`cmd/athanor/jobpod_client.go`) is the existing precedent; a git adapter
   takes the same named-file exception, not a new dependency.
3. **The repo's shape is not modeled.** An artifact has content and a kind but
   no "natural" destination path, and the project's `repository_path` (added
   by ADR-0028 for indexing) is the only repository handle. The design cannot
   guess where an artifact "belongs" in an arbitrary user repository.

## Decision

- **Core-side adapter.** `cmd/athanor/git_client.go` implements the engine's
  `GitCommitter` seam. It is the only `git` call site and the second
  Gate-G1-allowlisted `os/exec` file in `cmd/`.
- **Managed namespace.** An accepted artifact is written to
  `.athanor/artifacts/<kind>/<artifact-id>` inside `project.repository_path`.
  The namespace is athanor-owned, so it never collides with user files and
  needs no artifact→path mapping.
- **Per-project agent branch.** The commit lands on `athanor/<project-id>`,
  created on first use. Commits are atomic; history is never rewritten.
- **Clean-worktree guard.** If the worktree has uncommitted changes, the
  adapter returns `ErrDirtyWorktree` and the engine **skips and audits**
  (`git_commit_skipped`); it never disturbs work it did not create.
- **Never push.** Push is external and irreversible and stays HITL-gated
  (M6, `git_push`).
- **Best-effort, non-fatal.** Git-as-undo runs after the acceptance
  transaction commits. A missing repository, a dirty worktree, or a git
  failure is audited (`git_commit_skipped` / `git_commit_failed`) and the
  job still completes; the artifact simply has no undo point. Failing the
  job after acceptance would leave a failed job with an accepted artifact,
  which is worse.
- **Recorded, not inferred.** `artifacts.git_commit` (migration 0015) holds
  the SHA, written by `artifact.Store.SetGitCommit` with its own audit row.
- **`git_operation` stays reserved.** The §25 tool remains declared with no
  route: this decision makes Git Core-side, and a pod-initiated git tool is
  an M6 decision, not a prerequisite for Git-as-undo.

## Consequences

- Accepting an artifact now produces an undo point when the project has a
  clean git repository; the artifact row carries the commit SHA.
- Projects without a repository (or with a dirty worktree) still accept
  artifacts; the audit trail says why there is no commit.
- Users see agent commits on a dedicated branch and can `git revert` or
  reset freely. The agent never commits to the user's current branch and
  never pushes.
- A future "natural path" placement (per-archetype filenames, source
  trees) would supersede the managed namespace; the seam is the adapter
  method, so the engine change is small.
