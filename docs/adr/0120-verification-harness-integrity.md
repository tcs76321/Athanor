# ADR 0120 — Verification harness integrity

**Status:** Accepted · **Date:** 2026-11-06 · **Refs:** ADR-0065, ADR-0058,
ADR-0082; `internal/toolenvelope`, `internal/engine`, `cmd/athanor`

## Context

A code candidate is a file tree staged over a copy of the repository or a
fixture. The task's real test command then runs in that same tree. Before this
decision the candidate could emit `tests/test_*.py`, `conftest.py`,
`pytest.ini`, `pyproject.toml`, `Makefile`, `go.mod`, or any other file the
harness uses, and thereby weaken the check that decides acceptance.

That is not a prompt-injection edge; it is a normal model output shape. It
makes "the real test passed" true only relative to tests the model may have
rewritten.

## Decision

The verification harness is **immutable to the candidate**:

- Candidate paths are classified before staging. Harness directories (for
  example `tests/`, `test/`, `spec/`, `__tests__/`), test file names, and
  runner/config manifests are removed from the candidate tree.
- Every removed path is audited as `harness_path_ignored`.
- A candidate containing only harness files is rejected as producing no
  implementation rather than tested against a pristine tree.
- The same filter applies to the git-as-undo commit, so ignored harness edits
  are never committed.
- The pod-side staging remains defense in depth: the engine filter is the
  policy, and the pod must not silently accept a path the Core rejected.

The classifier is deliberately conservative: an ambiguous path is treated as
harness. It covers the common Python, Go, Node, Rust, JVM, Ruby, PHP, and Make
ecosystems without parsing arbitrary test commands.

## Consequences

- A test pass can be trusted as evidence about the candidate, not about a
  candidate-modified test suite.
- A task whose genuine deliverable is a test file must use an external or
  hidden harness; the candidate cannot author the gate that judges it.
- The benchmark's inline `python -c` checks are unaffected; fixture/repository
  tasks are now protected.
- New harness file names require extending the classifier, ideally with a
  regression test that proves the path is ignored.
