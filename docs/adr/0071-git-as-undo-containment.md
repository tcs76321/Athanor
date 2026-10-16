# ADR 0071 — Git-as-undo is confined to the project's own worktree

**Status:** Accepted · **Date:** 2026-10-16 · **Refs:**
[ADR-0030](0030-git-as-undo.md) (git-as-undo),
[ADR-0065](0065-multi-file-code-artifacts.md) (multi-file code artifacts);
ARCHITECTURE §9.2, §14, §21.3; Gate G1.

## Context

Git-as-undo (§14; [ADR-0030](0030-git-as-undo.md)) commits an accepted artifact
on `athanor/<project-id>` inside `project.repository_path`. The adapter
(`cmd/athanor/git_client.go`) is the only `git` call site in the binary.

A 0.0.0 soak exposed three containment failures in that adapter and its caller:

1. **Ancestor-repo escape.** The probe staged a fixture copy under its results
   tree — *inside the developer's worktree* — with no `.git`, then set that copy
   as the project's `repository_path`. Git resolves the nearest ancestor `.git`,
   so the adapter created and checked out `athanor/<uuid>` branches **in the
   developer's athanor repository**, moving its `HEAD` and committing there. The
   reflog recorded the checkout storm; the branch was deleted and `main` was
   force-reset to recover.
2. **Interactive hang.** The probe's fixture git-init ran `git commit` without
   disabling signing. On a machine with a global `commit.gpgsign=true` it
   invoked GPG and blocked on a pinentry prompt, wedging the unattended run and
   the operator's terminal.
3. **No root check.** Nothing refused a `repository_path` that was merely a
   subdirectory of a larger repository.

## Decision

1. **Fail closed on a non-root `repository_path`.** The adapter requires
   `repository_path` to be the top level of a git worktree
   (`git rev-parse --show-toplevel`, canonicalised for symlinks) and returns
   `ErrNotRepoRoot` otherwise. The engine audits the skip and acceptance still
   stands (best-effort, as [ADR-0030](0030-git-as-undo.md)). A subdirectory, a
   bare repo, or a plain directory cannot become the target of a branch or a
   commit.
2. **The harness never targets the developer's checkout.** The probe makes each
   staged fixture copy its own git repository, so the copy is the worktree root
   and git can no longer resolve an ancestor. The probe half and the adapter
   half are independent guarantees of the same property.
3. **No git call may sign or prompt.** The probe's fixture commit disables
   signing and terminal prompts
   (`-c commit.gpgsign=false`, `--no-gpg-sign`, `GIT_TERMINAL_PROMPT=0`); the
   adapter passes `--no-gpg-sign` and sets `GIT_TERMINAL_PROMPT=0` on every
   invocation. An unattended daemon cannot answer a prompt, so a prompt is a
   hang; failing loudly is the only acceptable outcome.
4. **The adapter never moves the operator's `HEAD`.** Git-as-undo commits
   through an **ephemeral worktree**: `git worktree add` a temporary path on the
   agent branch, write the files there, commit, then `git worktree remove`. The
   project's checked-out branch and working tree are never touched. This
   strengthens [ADR-0030](0030-git-as-undo.md)'s "never commits to the user's
   current branch" to "never disturbs the user's checkout at all." An empty
   repository (unborn `HEAD`) is refused with a clear error rather than moved.

## Consequences

- The soak can index and commit fixture copies without touching the developer's
  repository; a mispointed `repository_path` is refused and audited instead of
  escaping to a parent repository.
- A `repository_path` that is not a worktree root yields `ErrNotRepoRoot`; the
  artifact is still accepted, with no undo point, and the reason is on the event
  log.
- The agent branch is created and advanced without a checkout, so an operator's
  working tree stays clean and `HEAD` stays put; they can still
  `git checkout athanor/<id>` to inspect the commit.
- One extra `git worktree add`/`remove` pair per acceptance — negligible at one
  job at a time (§C single-slot invariant).
- An empty project repository no longer receives a first agent commit; the skip
  is audited. A project repository is expected to have at least one commit.

## Alternatives rejected

- **Keep `git checkout` and restore the original branch afterward.** Still moves
  `HEAD` (and races), and an interrupted restore leaves the operator on the
  agent branch — the failure mode this ADR exists to remove.
- **Pure plumbing (`hash-object` / `write-tree` / `commit-tree` /
  `update-ref`).** Never touches a worktree, but stops materialising the
  accepted tree, changing [ADR-0065](0065-multi-file-code-artifacts.md) §5's
  behaviour and losing the operator-visible files on `git checkout`.
- **Trust the caller's path.** The incident is exactly a caller that pointed at
  a parent repository; the guard is cheap and the failure is otherwise silent.
