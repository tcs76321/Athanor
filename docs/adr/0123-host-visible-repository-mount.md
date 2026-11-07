# ADR 0123 — Host-visible repository mount for the Core Pod

**Status:** Accepted · **Date:** 2026-11-06 · **Refs:** ADR-0069, ADR-0078,
ADR-0082; ARCHITECTURE §3, §4, §21; `cmd/athanor`, `scripts/install.sh`

## Context

The Core Pod is the only supported runtime. It mounts state as a named volume
and previously mounted the workspace as a named volume. A repository-backed
project, however, needs the Core to read a real repository so it can stage the
per-job read-only snapshot. A host path passed through the UI is not visible
inside a container, so code projects could not start from a normal host repo.

At the same time, mounting an arbitrary host path would widen the trust
boundary beyond what the Core already needs.

## Decision

The Core Pod mounts one operator-controlled host directory at the **same
absolute path** inside the Core:

```text
$HOME/athanor/repos  →  $HOME/athanor/repos
```

The installer creates it, `athanor start` mounts it (`-repos-dir` overrides the
default), and the Core can read and write repositories only under that
directory. A project repository path must be absolute and must exist at create
time. The UI explains the Core Pod constraint.

Named volumes remain for state and the unused workspace volume; SQLite stays
off virtiofs. The repository mount is read-write because git-as-undo commits
accepted work to an agent branch.

## Consequences

- A first-use code project can point at `~/athanor/repos/<repo>` and work in
  the supported runtime.
- The host repository is not exposed wholesale; only the operator-created
  directory is mounted.
- A repository outside the directory is rejected at create time instead of
  failing later with a confusing index/test error.
- The Core remains the trust boundary; this does not add a mount to Job Pods.
