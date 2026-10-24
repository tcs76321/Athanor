# ADR 0082 — The repository working set is a staged read-only snapshot

**Status:** Accepted · **Date:** 2026-10-24 · **Refs:**
ARCHITECTURE §3.1 (isolation), §10.1 (lossless context), §21.3 (path
containment), §25 (tools); [ADR-0028](0028-repository-indexing.md)
(repository indexing), [ADR-0059](0059-automatic-working-set.md) (working
set), [ADR-0078](0078-core-pod-only-runtime.md) (Core Pod),
[ADR-0080](0080-engine-executed-tools.md) (engine-executed tools);
`docs/first-use.md` (real-repo gap).

## Context

A Job Pod currently mounts only its token directory; the candidate is staged
into `/tmp`. The model reaches repository content through the M5-T8 index and
`query_memory`/`context_swap`. That is enough for self-contained benchmark
tasks, but not for the documented "work on my repository" workflow: the pod
cannot run the repository's real test suite against the candidate overlay, and
the model may not see the files the task refers to.

Mounting the operator's repository directly is the obvious shortcut, but it
would expose a host path to untrusted code, let the pod read anything in the
worktree (including ignored secrets), and make the pod's view depend on the
operator's working tree at run time.

## Decision

1. **The Core stages a per-job snapshot under its own state directory**:
   `<state>/scratch/<job>/repo`. The snapshot contains tracked files only
   (`git archive` of the project's `repository_path`), excludes `.git`, `.env`,
   private keys, and common dependency/build directories, and is size-capped.
2. **The snapshot is the only repository mount**: read-only at
   `/workspace/repo`. No host repository path is ever a mount source. The
   existing token mount and `/tmp` remain.
3. **`run_tests`/`lint` run against the snapshot** (the candidate overlay is
   copied into a writable scratch work tree, bounded by the snapshot cap).
   A candidate that needs to write outside the work tree fails closed.
4. **The model's context is still engine-driven**: the existing indexer ranks
   relevant snapshot files/chunks into the Dormant Index; the engine may seed
   the active chunk. No model tool call is introduced (ADR-0080).
5. **Lifecycle:** the snapshot is removed when the job reaches a terminal
   state, and a boot sweep removes any orphaned `<state>/scratch/<job>`
   directory. Snapshot size and count are reported in the soak telemetry.
6. **Gate G2 is extended**: the argv test allows exactly the token mount and a
   staged snapshot whose source is under the state scratch root; any other
   source, any writable repository mount, and any host path fails the gate.

## Consequences

- The pod sees a stable, reproducible view of the repository, not the
  operator's live worktree; ignored files and secrets do not leak by default.
- The Core does more work at job start (snapshot + extraction), bounded by the
  size cap. A large repository may exceed the cap and is reported rather than
  silently truncated.
- The test command can exercise the real project instead of a single file.
- Scratch is now a disk-growth source on the same scale as artifacts; the
  cleanup and telemetry requirements in (5) are load-bearing, not optional.

## Alternatives rejected

- **Bind-mount the host repository directly.** Simpler, but exposes secrets,
  ignores the snapshot boundary, and makes runs non-reproducible.
- **Copy the whole worktree including `.git` and ignored files.** Leaks
  secrets and inflates the snapshot; tests rarely need history.
- **Only index the repository (no mount).** The model can read summaries but
  the test command cannot run against the real project.
- **Add `read_file`/`write_file` model tools now.** A different decision with
  a larger containment surface; the engine-driven snapshot is the smaller
  first step (ADR-0080).
