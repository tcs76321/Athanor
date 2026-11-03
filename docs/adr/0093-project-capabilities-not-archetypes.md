# ADR 0093 — Projects have capabilities, not archetypes

**Status:** Accepted — first implementation slice · **Date:** 2026-11-02 ·
**Refs:** ARCHITECTURE §6 (projects and archetypes), §7.3 (tasks), §9.1
(artifact kinds), §13.1 (dialectical phases), §19 (verification);
[ADR-0031](0031-project-execution-config.md) (execution config),
[ADR-0044](0044-compute-policy-seam.md) (policy features),
[ADR-0045](0045-verification-first-selection.md) (verifier-first),
[ADR-0070](0070-declared-checks.md) (declared checks),
[ADR-0081](0081-code-archetype-tool-defaults.md) (code tool defaults).

## Context

The project model asks the operator to choose one **archetype** (`text`,
`code`, `document`, `data`, or `media`) before any work begins. It also asks
for a **code test command**, defaulting to `pytest -q`. Both are the system's
job, not the operator's:

- A real project is rarely one kind. A code project also writes a README,
  release notes, and test fixtures; a document project may include a JSON
  example; a data project may include a small script. The archetype collapses
  those into a single label that then selects one rubric, one verifier set, one
  final artifact kind, and one tool default.
- The test command is a property of the repository, not of the operator's
  intent. `go.mod`, `pyproject.toml`, `package.json`, `Cargo.toml`, and a
  `Makefile` already say how the project is tested.
- The first-use form makes the operator answer both before the system has seen
  the goal or the repository. A wrong choice is sticky and silently changes
  acceptance.

The archetype is load-bearing today: the database `CHECK`, the engine's rubric,
the verifier registry, tool defaults, the final artifact kind, the working-set
mode, and the benchmark's task wiring all key on it. Removing it is therefore a
model change, not a form change.

## Decision

1. **A project is a goal, a repository, and acceptance criteria.** It has no
   operator-selected archetype. `projects.archetype` becomes a deprecated
   migration shim holding the project's primary capability; no user surface
   reads or writes it.
2. **A project has an inferred capability set.** Capabilities are the kinds of
   work the project can produce: `code`, `data`, `document`, `text`, `media`.
   The set is derived deterministically from the goal and the repository
   contents (file extensions and manifests). It is displayable and auditable,
   not a form control.
3. **A task has one primary capability.** The task's output kind decides the
   final artifact kind and the applicable verifier set. It is inferred from the
   task description, the project capability set, and the repository. Tasks in
   one project may have different capabilities; a project can move from a code
   task to a document task without a second project.
4. **Verification follows the task capability.** Code tasks run the resolved
   test command and lint; data tasks use the declared JSON Schema; text and
   document tasks use declared checks and structural checks; media remains
   judge-advisory. A capability does not grant a tool or weaken containment.
5. **The test command is repository-derived.** Resolution order is an explicit
   execution override (benchmark and test only) → a detected command from the
   repository manifest (`go.mod`, `pyproject.toml`/`pytest.ini`, `package.json`,
   `Cargo.toml`, `Makefile`, `pom.xml`, `build.gradle`, `composer.json`) → no
   command. A code task with no detected command does not invent `pytest -q`;
   it degrades to the judge and is audited as `tests_skipped`.
6. **The UI/API/CLI default path has no archetype or test-command field.** The
   project form asks for name, goal, optional repository, and criteria. The
   project page shows the inferred capabilities and the resolved verification
   read-only. The API keeps the legacy `archetype` field only as an optional
   machine override for the frozen benchmark and compatibility; it is not the
   product path. The CLI's `-archetype`/`-test-command` remain advanced
   compatibility flags, documented as such.
7. **Inference is deterministic and fail-safe.** A goal with no recognizable
   signal defaults to `document` (prose output) plus the capabilities found in
   the repository. Inference never blocks a create. An operator who disagrees
   edits the goal or repository, not a taxonomy field.

## Schema

Migration 0033 adds:

- `projects.capabilities_json TEXT NOT NULL DEFAULT '[]'`
- `tasks.capability TEXT NOT NULL DEFAULT ''`

Existing rows are backfilled from `projects.archetype` (`["code"]`, etc.), and
each existing task gets its project's primary capability. The legacy
`archetype` column remains for one deprecation window; code reads capabilities
and task capability, and writes the primary capability into the legacy column
so old CLIs and API clients keep working.

## Consequences

- A project can contain code, document, data, and text tasks without being
  recreated or relabeled.
- The operator no longer has to know the taxonomy or the test framework.
- Acceptance is consistent with the artifact a task actually produces, not a
  project-wide guess.
- Inference is a heuristic. A misclassified task can be corrected by editing
  its description or by an advanced execution override; the UI must show what
  was inferred so the operator can see the mistake.
- The frozen benchmark's per-task archetype becomes a task capability override;
  its explicit test commands remain an execution override. The corpus and
  scores do not change.
- The legacy column and API field are a migration shim, not a product concept;
  a later cleanup can drop them after one release.

## Alternatives rejected

- **Keep one project archetype but hide the field.** Inference still has to
  collapse a mixed project into one label; the wrong rubric/verifier remains.
- **Ask the operator to choose a capability set.** That is the same taxonomy
  burden with more checkboxes.
- **Ask the operator for the test command with a default.** A default that is
  wrong is worse than a detected command; a missing detection is visible and
  auditable.
- **Let each task declare arbitrary capabilities with no default.** The planner
  would still need a prior; inference plus a visible read-only result is the
  smaller, auditable step.
