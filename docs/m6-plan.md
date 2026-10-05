# M6 Plan — Autonomy & Feedback

**Milestone:** M6 (Autonomy & Feedback) · **Entry ADR:**
[0032-dag-decomposition.md](adr/0032-dag-decomposition.md) ·
**Gate:** G6 — HITL gates every external/irreversible action; feedback-loop
E2E green; a multi-task DAG demo passes unattended.

This is the milestone-level plan. M6-T1 is the current work item and is
specified to commit granularity below; T2–T11 are outlines that fix
interfaces and sequencing so later tasks do not paint themselves into a
corner.

## Shared decisions

- **No new dependencies.** The M6-T8 Web UI uses the standard library
  (`net/http`, `html/template`, `embed`). A UI framework would be a project
  decision surfaced before implementation, not taken by an agent.
- **One migration per capability.** Migrations stay forward-only; each is
  numbered and bookkept in `internal/store/store_test.go`,
  `enum_migration_test.go`, and `job_migration_test.go`.
- **Pure logic first.** Graph, scheduling, strategy aggregation, and failure
  policy are pure functions with table/property tests; LLM calls sit behind
  seams filled by `cmd/athanor` adapters.
- **Gate discipline.** Re-prove Gate G1 after any new `internal/` package;
  Gate G2 is untouched until a tool route changes.

## M6-T1 — DAG decomposition (detailed)

**Status: ✅ complete (2026-10-04).** All seven commits landed; the
`goal submit` path is deliberately still single-task pending M6-T2.

**Acceptance:** Generated DAGs pass validation or are rejected with reason;
decomposition failure retries with the simpler `main` persona.

**Design:** [ADR-0032](adr/0032-dag-decomposition.md). Decomposition is an
explicit Core step (`athanor goal decompose` /
`POST /projects/{id}/decompose`); `goal submit` is unchanged until M6-T2.

### Commit sequence

| # | Commit | Scope |
|---|---|---|
| 1 | `M6-T1.1: ADR-0032 + M6 plan` | this file, `docs/adr/0032-dag-decomposition.md` |
| 2 | `M6-T1.2: internal/dag model, parser, validator` | pure package + property tests |
| 3 | `M6-T1.3: project task-graph persistence` | `TaskSpec`, `CreateDAG`, `TasksByGoal`, `Task` fields |
| 4 | `M6-T1.4: decompose orchestration with tall→main retry` | `internal/decompose` + fake-client tests |
| 5 | `M6-T1.5: config DAG bounds + example` | `internal/config`, `config.example.yaml` |
| 6 | `M6-T1.6: /decompose route + CLI + boot wiring` | `internal/api`, `cmd/athanor` |
| 7 | `M6-T1.7: close-out + §7.3 status reconciliation` | ROADMAP/README/CHANGELOG; ADR "Implemented" note |

### Package map

- `internal/dag` — pure: `Node`, `Budget`, `Graph`, `Parse`, `Validate`,
  `ValidationError`, `Limits`. No I/O, no LLM, no `internal/project`.
- `internal/decompose` — orchestration: `Decomposer` over an `LLMClient`
  seam, `*project.Repo`, `*store.Store`, `*config.Config`, `*llm.Registry`.
- `internal/project` — `TaskSpec`, `Repo.CreateDAG`, `Repo.TasksByGoal`,
  extended `Task` fields.
- `internal/api` — `Decomposer` interface + `SetDecomposer`,
  `POST /projects/{id}/decompose`, `GET /projects/{id}/tasks`.
- `cmd/athanor` — `decompose.go` adapter + `cli_decompose.go`, wired in
  `serve.go`.
- `internal/config` — `execution.dag_max_tasks` (25), `dag_max_depth` (6),
  `dag_max_total_jobs` (100).

### Validation rules

Unique non-empty keys · every parent/dependency reference resolves · no
self-dependency · acyclic (Kahn; returns a topological order) · at least one
root · depth ≤ `dag_max_depth` · node count ≤ `dag_max_tasks` · every leaf
has ≥1 acceptance criterion · Σ leaf `max_jobs` ≤ `dag_max_total_jobs`.

### Verification

- `make check` green after every commit.
- Gate G1 re-proven after T1.2 and T1.3
  (`CGO_ENABLED=1 go test ./internal/gate/`).
- No migration in T1 (ADR-0032 §4); `internal/deps` unchanged.
- Tests: parser tolerance + rejection corpus; validator table + property;
  `CreateDAG` mapping/atomicity; orchestrator fallback with a scripted fake
  client; API statuses (201/400/404/409/422/503); CLI round-trip.

## M6-T2 — Dependency scheduler

**Status: ✅ complete (2026-10-04).** Commits `b83d5e5` (ADR), `73688e9`
(migration 0017), `f5a96f5` (task lifecycle + `job.ByTask`), `6719ea0` (pure
graph functions), `a5a09e8` (orchestrator + engine seam + boot reconcile),
`dec291b` (gated submit). `goal submit` stays single-task unless
`execution.dag_decomposition` is true.

- `internal/scheduler`: pure `Ready`/`Topological` + `Blocked`
  propagation; a `Scheduler` that creates and enqueues jobs for ready leaf
  tasks, marks tasks `ready`/`running`/`completed`, and blocks descendants
  when a dependency fails (§7.2).
- Transition hook: a nil-safe `OnJobTerminal` seam on the engine (plus a
  boot reconcile pass) so the scheduler learns of completion; ADR-0033.
- Migration **0017**: rebuild `tasks` with the §7.3 status set
  (`pending,ready,running,paused,blocked,completed,failed`) and
  `task_type`. This is the reconciliation ADR-0032 §5 defers.
- Wire `goal submit` to decompose + schedule once this lands.
- Acceptance: diamond dependencies execute in order; blocked propagation;
  crash-recovery reconcile.

## M6-T3 — Failure policies

**Status: ✅ complete (2026-10-04).** Commits `7277bcd` (ADR), `c0cfb9a`
(task edges + `max_task_retries`), `7d3cba8` (scheduler policy + tests). The
concrete alternative-persona re-decomposition adapter is **T3b** and the real
HITL writer is **M6-T4**; both seams are nil-safe today.

- §7.2 rows: max-retry → `blocked` → `alternative` re-decomposition →
  HITL; dependency failure → descendants `blocked`; budget exhausted →
  pause remaining and escalate; decomposition failure → `main` (already
  T1). An `Escalator` seam keeps the HITL dependency injectable.
- Config `execution.max_task_retries`; ADR-0034.
- Acceptance: one automated scenario per §7.2 row.

## M6-T4 — HITL request queue

**Status: ✅ complete (2026-10-04).** Commits `3d9fe12` (ADR), `b3b6e49`
(migration 0018 + resumable `awaiting_approval`), `e667577` (`internal/hitl`
repo + service), `711edfc` (config), `533c898` (API/CLI/wiring). The first
job-linked caller is **M6-T5**'s HITL-gated `git_push`.

- `internal/hitl` repo over the existing `hitl_requests` table: types,
  severity, expiry, approve/reject/modify/defer; pausing and resuming jobs.
- Add `awaiting_approval` edges to `internal/job/state.go`, with a
  `resume_state` to return to. Migration rebuilds `hitl_requests` for the
  canonical set and `deferred`; ADR-0035.
- Acceptance: a paused job resumes exactly on approval; expiry denies by
  default; decisions logged.

## M6-T5 — `git_push` behind HITL

**Status: ✅ complete (2026-10-04).** Commits `27de9a4` (ADR), `ddf9e10`
(approver seam), `ae0834c` (push path + API/CLI/wiring).

- Core-side push adapter (Gate G1 `os/exec` allowlist) that first creates a
  HITL request; a denied push leaves the remote untouched. Small.
- Acceptance: push attempt creates an approval request; deny is a no-op.

## M6-T6 — CorrectionRecords

**Status: ✅ complete (2026-10-04).** Commits `4ad0cc9` (ADR), `004c2da`
(migration 0019), `6126583` (`internal/corrections`), `f1b7f61` (engine
sink), `8798602` (API/CLI/wiring). Injection is M6-T7.

- `internal/corrections` repo over the existing `corrections` table.
- Capture for every §18.1 source; the §18.4 rejection form is mandatory
  (category, severity, reason, desired behavior, scope). Migration adds
  `artifact_id`, `scope`, `user_feedback`; ADR-0036.
- Acceptance: every rejection yields a well-formed record; each source
  produces one.

## M6-T7 — Feedback injection

- Corrections become §11.2 position 8 in the engine's tier assembly, with
  severity/scope ordering, vector/full-text retrieval over the MCE, and
  token accounting; mute/edit/promote API. ADR-0037.
- Acceptance: high-severity project corrections outrank; injection
  positions and token accounting are logged.

## M6-T8 — Web UI

- `internal/ui` over stdlib templates + `embed`; an SSE bus. (a) Dashboard +
  Approvals; (b) Projects/Artifacts/Corrections CRUD with diffs; (c) Watch
  View with live token stream, phase tree, tool log, and pause/stop/retry.
- Adds a streaming path to `llm.Client`; ADR-0038.
- Acceptance: dashboard reflects phase within ~1s; approval resumes the
  correct job; CRUD persists; diffs render.

## M6-T9 — Feedback-loop E2E

- E2E: reject an artifact with a structured reason → CorrectionRecord →
  injected into the next job's prompt → the mistake is avoided (§31.4).

## M6-T10 — Strategy capture

- Persist `StrategyProfile` at job start and an immutable `StrategyOutcome`
  at job end, transactionally with transitions; derive with zero inference
  from the persona plan; backfill pre-capture jobs. Migration adds
  `strategy_profiles`, `strategy_outcomes`, `strategy_insights`; ADR-0039.

## M6-T11 — Strategy analysis

- Deterministic aggregation over outcomes → proposed insights at thresholds;
  HITL-gated activation/muting (T4); strategy notes at assembly position 14;
  Statistics panel (T8). Proposed insights are provably inert until
  approved. ADR-0040.

## Sequencing

`T1 → T2 → T4 → {T3, T5, T6} → T7 → T8 → T9`, with `T10 → T11` last.
Gate G6 closes after T11 with the three required proofs.
