# ADR 0032 — DAG decomposition as an explicit Core step (M6-T1)

**Status:** Accepted · **Date:** 2026-10-04 · **Refs:** ARCHITECTURE §7,
§7.1–§7.3, §13.1; ROADMAP M6-T1; ADR-0001 (phased state machine),
ADR-0002 (context-floor semantics), ADR-0012 (structured JSON verdicts).

## Context

ARCHITECTURE §7.1 says that when a Goal is submitted, the `tall` persona
decomposes it into a DAG of Tasks with dependency edges, and that the DAG is
validated for cycles, leaf coverage, and budget feasibility. The M1 walking
skeleton never did this: `project.Repo.SubmitGoal` creates exactly **one**
task per goal, and `goal submit` runs exactly one job. Autonomous DAG
execution is scheduled for M6-T2, and failure policy for M6-T3.

M6-T1 must therefore introduce decomposition **without** regressing the
walking-skeleton path that the whole test suite and demo depend on. There is
no scheduler yet, so a `goal submit` that produced a DAG without running it
would silently stop producing artifacts — a behavioral regression.

Two questions must be answered:

1. **Where does decomposition run?** As part of `goal submit`; as a phase of
   the goal's first job; or as its own explicit Core step.
2. **How is a task's lifecycle status named?** The shipped `tasks.status`
   CHECK (migration 0001) is
   `pending|ready|in_progress|blocked|done|failed|cancelled`; ARCHITECTURE
   §7.3 names `pending|ready|running|paused|blocked|completed|failed`.

## Decision

### 1. Decomposition is an explicit Core step (phased, like ADR-0001)

M6-T1 adds a `internal/dag` package (a pure graph model, parser, and
validator) and an `internal/decompose` orchestrator. They are exposed to the
operator through `athanor goal decompose` and
`POST /projects/{id}/decompose`. The existing `goal submit` path is
**unchanged** until M6-T2 wires the dependency scheduler; at that point
submission will decompose and schedule. This is the same deliberate,
scoped deviation pattern as ADR-0001: ship the capability with its enabler,
not before.

### 2. Pure validation, deterministic judgment

`dag.Validate` is a pure function over the parsed graph. It checks: unique
non-empty keys; every `parent`/`depends_on` reference resolves; no
self-dependency; acyclicity (Kahn's algorithm, which also yields a
topological order); at least one root; a maximum depth and node count;
leaf coverage (every task that is nobody's parent declares at least one
acceptance criterion); and budget feasibility (the sum of leaf `max_jobs`
budgets is within the configured bound). It returns a typed
`ValidationError{Kind, NodeKey, Detail}` so the caller can report a
machine-readable rejection reason. No LLM is in the judgment path.

### 3. Decomposition retries with the simpler `main` persona

Attempt 1 uses `tall` at the planning temperature. If parsing or validation
fails, attempt 2 uses `main` — §7.2's "decomposition failure retries with a
simpler strategy". If both fail, the step returns a typed
`ErrDecompositionRejected` carrying the last reason and persists nothing;
the attempt and the reason are audited under `category=jobs`.

### 4. Persistence reuses the existing schema

The `tasks` table already carries `parent_task_id`, `depends_on_json`,
`acceptance_criteria_json`, `budget_json`, and `priority`, and
`goals.status` already admits `decomposed`. M6-T1 therefore needs **no
migration**. `project.Repo.CreateDAG` maps model keys to generated IDs,
resolves dependency references, and writes the whole graph plus the goal
status in one transaction.

### 5. Task status names are reconciled at M6-T2, not M6-T1

M6-T1 only ever writes `pending`, which exists in both spellings. The
canonical §7.3 set (which includes `running`, `paused`, `completed`, and
`task_type`) is installed by the M6-T2 scheduler migration, when the
scheduler is the first code to set those states. This avoids two `tasks`
table rebuilds and keeps each migration tied to the capability that needs it.

## Consequences

- A goal's DAG is inspectable and testable **before** any job runs, which is
  what M6-T1's acceptance criterion requires. `goal submit` behavior and its
  tests are untouched.
- Until M6-T2, `POST /projects/{id}/decompose` produces tasks but does not
  enqueue them; the operator can inspect via `GET /projects/{id}/tasks`.
- The decomposition LLM call honors §12.6 feasibility for the `tall` persona
  in the planning phase (the ADR-0002 exemption applies) and never silently
  reduces context.
- `internal/dag` is pure and `internal/decompose` is the only new importer
  of `internal/llm` for orchestration; Gate G1 is unaffected (no tool
  execution, no syscall).
- The §7.3 status-name discrepancy is tracked here and closed by the M6-T2
  migration and its ADR.

## Implemented (M6-T1.2–T1.6)

`internal/dag` (pure parser + validator), `internal/decompose` (tall→main
orchestration with typed rejection/infeasible errors and `jobs` audit rows),
`project.CreateDAG`/`TasksByGoal`/`TasksByProject`, the
`POST /projects/{id}/decompose` and `GET /projects/{id}/tasks` routes, and
`athanor goal decompose` all landed. The graph bounds are
`execution.dag_max_tasks/depth/total_jobs`. No migration was needed. The M6-T2
scheduler migration remains the point that reconciles the §7.3 status names,
as §5 records.
