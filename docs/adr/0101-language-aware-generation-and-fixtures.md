# ADR 0101 — Language-aware generation and contained fixtures

**Status:** Accepted · **Date:** 2026-11-05 · **Refs:** ARCHITECTURE §6,
§10.1, §25; [ADR-0065](0065-multi-file-code-artifacts.md),
[ADR-0093](0093-project-capabilities-not-archetypes.md).

## Context

The capability model is language-agnostic and `DetectTestCommand` recognizes Go,
Rust, JavaScript, Java, Ruby, PHP, Python, and Make. The generation path was not:
the prompt said "raw Python source", the module-name heuristic was Python-only,
and `execute_code` accepted only Python. A Go or Rust repository could run its
real tests but was asked for Python.

Fixtures were also broader than ADR-0065 claimed: `fixture_path` accepted any
absolute host directory and only checked containment inside itself. That made
the "under the workspace or repo, through the airlock" statement untrue.

## Decision

1. **Language comes from evidence.** The planner may emit `LANGUAGE: <name>`;
   the engine validates it against a closed set. If the planner is silent, the
   repository manifest and the detected test command decide. Unknown is a valid
   answer and produces a language-neutral prompt rather than a Python guess.
2. **Generation is language-aware.** `codeInstruction(language)` names the
   language and keeps the shared `=== FILE: path ===` multi-file contract.
3. **Non-Python candidates are staged as trees.** A single non-Python file gets
   its conventional name (`solution.go`, `solution.rs`, …) and is staged through
   `Files`; the repository's test command executes it. Python keeps the direct
   `python solution.py` shorthand. `execute_code` accepts a recognized language
   for a tree, while single-file execution remains Python-only.
4. **Fixtures need a boundary.** `execution.fixture_root` (or, by default, the
   project's `repository_path`) is the canonical root a fixture must live under.
   A fixture with no boundary is refused. The benchmark sets `fixture_root` to
   the corpus fixtures directory.

## Consequences

- Go/Rust/JS repositories get prompts that match their verifier; the Job Pod
  still relies on the repository's real test command.
- Single-file non-Python accepted code is committed as a managed artifact until
  the repository-relative path is known; multi-file trees already commit at
  their real paths.
- A fixture cannot be pointed at an arbitrary host directory. Existing callers
  must set `fixture_root` when the fixture is outside the repository.
- Unknown languages remain supported, but the prompt cannot promise a specific
  interpreter.
