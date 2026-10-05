# ADR 0031 — Per-project execution configuration (F3-T4)

**Status:** Accepted · **Date:** 2026-10-04 · **Refs:** ARCHITECTURE §6.1,
§6.2, §14; ROADMAP F3-T4; ADR-0009 (engine pod wiring), ADR-0024 (engine
pod dispatch), ADR-0028 (projects.repository_path precedent).

## Context

ARCHITECTURE §6.2 gives each archetype concrete execution definitions. For
`code` it names `language`, `test_framework`, `build_command`, `run_command`,
and `linters`. None were modeled: `internal/engine/evaluate.go` ran a literal
`pytest -q` for **every** code project, and the rubric told the judge so.
A code project could not choose a different test command, which is a
correctness gap for anything that is not Python/pytest.

The narrowing constraints:

- **Language stays closed at `python` for now.** The internal API's
  `execute_code` handler enforces a one-language closed set
  (`internal/internalapi/exec.go`), and multi-language execution is a
  separate, larger change (image selection, interpreter argv). F3-T4 makes
  the *command* configurable, not the language.
- **`project.create` already has a wide signature.** Adding an argument
  would churn every caller, most of which have no override.
- **§6.2's field set differs by archetype** and is still evolving.

## Decision

- **One JSON column.** Migration 0016 adds
  `projects.execution_json TEXT NOT NULL DEFAULT '{}'`. Existing rows and
  rows created without an override resolve to archetype defaults at use
  time, so no backfill is needed.
- **A small typed model.** `project.Execution{TestCommand, BuildCommand,
  Linters}` marshals to that column. The field set can grow without a
  migration per field.
- **Built-in defaults, resolved dynamically.** `project.Project.TestCommand()`
  returns an explicit `test_command` when set, else
  `DefaultCodeTestCommand` (`"pytest -q"`) for a `code` project, else `""`.
  Legacy rows (empty JSON) get the same default as new ones.
- **Set out of band.** `project.Repo.SetExecution` is a separate write
  after `Create`, so the common path stays a single transaction. Exposure:
  `athanor project create -test-command/-build-command` and the
  `execution` object on `POST /projects`.
- **The engine reads it.** `phaseEvaluate` uses `p.TestCommand()` for the
  `run_tests` call and records the command that failed. The rubric no
  longer names pytest.

## Consequences

- A code project can run `go test ./...`, `cargo test`, etc.; the default
  preserves current behavior and the existing tests (`pytest -q`).
- Non-code archetypes are unaffected (`TestCommand()` is empty; the engine
  only calls the runner for `code`).
- `language` remains `python` in the closed set until a dedicated
  multi-language task; a project cannot yet run Node or Go directly.
- A future `run_command`, `documentation_required`, or per-archetype schema
  extends the same JSON column, or supersedes it with typed columns if the
  field set stabilizes — the seam is `project.Execution`.
