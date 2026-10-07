# ADR 0065 — Multi-file code artifacts and fixture staging

**Status:** Accepted — lands across M8 · **Date:** 2026-10-07 · **Refs:**
[ADR-0030](0030-git-as-undo.md) (git-as-undo), [ADR-0014](0014-evaluation-phase-move.md)
(evaluation/pod), [ADR-0058](0058-verification-gates.md) (code acceptance
gates), [ADR-0061](0061-harder-benchmark.md) (harder corpus),
[ADR-0064](0064-cognitive-operations.md) (operations); ARCHITECTURE §9, §13.1,
§14, §21.3, §25.

## Context

The code path materializes exactly one file per candidate —
`<scratch>/solution.py` — and runs the task's `test_command` in that scratch
dir (`cmd/athanor/pod_executor.go`). The engine produces a single-blob
artifact; there is no way to stage a starter repository (a *fixture*) into the
pod.

The harder benchmark (ADR-0061) specifies multi-file code tasks (a `bank`
package; a refactor of a provided module) whose hidden tests live beside the
source. Under the single-file model those tasks are not runnable, so the
instrument cannot measure the loop on real, multi-file work — exactly the
tasks where the loop's value is most in question.

## Decision

**1. A code candidate is a file tree, stored as one blob.** A multi-file
candidate is the model's raw output carrying `=== FILE: path ===` block
headers (human-readable and diff-friendly, and what the docs verifier already
scans); a single-file candidate is just the source. The pod-staging **wire**
encoding is a JSON manifest of `{path, content}` entries. Keeping one artifact
per candidate means the artifact store, content hashing, dedupe, and the
eventual git-as-undo commit need no schema change — only the *interpretation*
of the blob changes for the `code` archetype. Binary and oversized entries are
out of scope (code is text).

**2. `toolenvelope.ExecuteRequest` carries a file tree.** A new `Files
[]File` field (`{path, content}`) supersedes `Code` for multi-file candidates;
`Code` remains the single-file shorthand so nothing existing breaks. The pod
executor stages every file into the scratch dir under a **sanitized relative
path** (no absolute paths, no `..`, no symlinks — the §21.3 containment rules
applied to container paths), collapsing parent directories as needed.

**3. Fixtures are staged, then overlaid.** A task may declare a *fixture*
(a starter tree, read through the §21.3 path-containment library). The pod
stages the fixture files first, then overlays the candidate's files, then runs
the `test_command`. So a "refactor the provided module" task ships the module
and its visible tests in the fixture; the model returns the changed source;
the hidden tests run against the merged tree. A fixture with no candidate
overlay is a valid (read-only) input tree.

**4. Verification is unchanged in kind.** The `code` acceptance gates
(ADR-0058) and the deterministic test run decide acceptance exactly as today;
only the staged content is now a tree. The job's tests are the verification
edge, as before.

**5. git-as-undo commits the tree.** On acceptance, the Core writes the tree
into the project repository (relative paths, containment-checked) and commits
on the agent branch (ADR-0030). One commit per accepted tree.

**6. Fixtures are host paths, contained.** The fixture path lives in the
project's execution config (a new `fixture_path`, migration), points under the
workspace or a repo path, and is read only through `internal/airlock/paths`.
The probe sets it when submitting a bench task; a missing or escaping path is
a loud error, never a silent empty tree.

## Consequences

- The harder corpus's multi-file tasks become runnable, so the instrument
  measures real work. The `solution.py`-only tasks still work via the
  shorthand.
- The pod's `execute_code` gains a file-staging step and a small, well-tested
  path-sanitizer; the single-file path is a special case of the tree.
- Artifact content for code is now a JSON manifest rather than raw source; the
  code-evaluation prompt must render the tree to the model (path headers), and
  the git path writes files rather than one blob. Both are contained changes.
- A tree artifact is still one artifact, so versioning, comparison, and the
  strategy trajectory are untouched.

## Out of scope (deferred)

- Binary/large-file entries and directory-level metadata (mode bits,
  symlinks). Code fixtures are text.
- Non-Python language toolchains in the pod (the model is `python` today).
- The Artifact Browser UI showing a tree (a §27 view).

## Sub-tasks (M8-T17 split)

- **B1** `toolenvelope`: `File` type, `Files` field, path sanitizer, encode/decode.
- **B2** pod executor + `internalapi`: stage a `Files` tree into the scratch dir.
- **B3** engine: build the candidate tree for `code`; render it into the eval prompt.
- **B4** fixture: project `fixture_path` (migration) + staging through `airlock/paths`.
- **B5** git-as-undo: commit a tree.
- **B6** corpus: multi-file fixtures + hidden tests for the six code tasks.
