# ADR 0132 — Project intake is one intent, two decisions

**Status:** Accepted · **Date:** 2026-11-09 ·
**Refs:** [ADR-0093](0093-project-capabilities-not-archetypes.md),
[ADR-0094](0094-project-discovery-portfolio.md),
[ADR-0095](0095-first-class-gui-and-onboarding.md),
[ADR-0097](0097-ai-plan-candidates.md), ARCHITECTURE §5–§7, §27.

## Context

Guided project creation collected a goal on the create form, then asked for
the goal again in a brief, alongside constraints, limitations, sources, and
success criteria. The brief kept its own workflow status while the UI also
inferred the stage from the presence of discovery and plan sessions. The
project goal existed in four places: `projects.goal`, `project_briefs.goal`,
the initial `goals` row, and the approved plan's synthesized goal text.

The duplication had concrete costs:

- `CreateDAG` accepted a `criteria` parameter and never read it; the
  create-form criteria only reached `project_briefs.success`, which the
  AI-plan path ignored.
- `project_briefs.status` declared `discovered` and `skipped` but never wrote
  them; the stage was reconstructed by a long UI switch.
- Every project paid for a discovery portfolio and a three-plan run, even
  when the goal was precise and repository-backed.
- Directions carried six required prose fields per option, making a portfolio
  of ten directions harder to compare than the decision required.

## Decision

### 1. One intent record

A project is created from **one required field, the goal**. Name, repository,
boundaries, and sources are optional context on the same record. The guided
create path writes exactly one `projects` row: no brief, no initial `goals`
row, and no placeholder task. The approved plan creates the execution graph
and records `projects.approved_plan_id` in the same transaction.

`project_briefs` is removed (migration 0047). Boundaries and sources are
backfilled onto the project in migration 0045. Project-level acceptance
criteria are removed; task criteria come from the approved plan, with the
direction's check as the deterministic fallback.

### 2. Infer the on-ramp, allow an override

`project.InferOnRamp` chooses **direct** or **explore** from the goal, the
repository, and the inferred capabilities. A repository-backed code goal
plans directly; prose goals and explicit exploratory language explore. The
operator can override the choice, and the chosen value is stored in
`projects.onramp`.

- **Direct:** goal → one planning call → approve.
- **Explore:** goal → direction portfolio → choose → one recommended plan
  (two alternatives available) → approve.

### 3. Directions are a comparison surface

A direction is `title`, `outcome`, `method`, and `check` (all required), plus
optional `scope`, `risks`, and `assumptions`. `outcome` folds the old
problem + deliverable; `method` replaces approach. The database columns are
migrated in 0046.

### 4. Stage is derived, not stored

`internal/intake` owns the flow. Its pure `evaluate` function derives exactly
one stage from durable artifacts: intent, directions, choose, plan, working.
A generating session with no live run is reported as interrupted and
retryable. The UI renders the state; it no longer writes intake SQL.

### 5. The UI and API share one service

`internal/intake` owns create/save-intent, direction and plan generation
(including the durable and in-memory run view), selection, and approval. The
HTTP API exposes the same flow. The UI is a renderer. Direction generation is
asynchronous and polled through `GET /projects/{id}/intake`.

### 6. Approval is explicit and audited

Approval validates the task graph with the same rules as autonomous
decomposition, creates the goal and tasks atomically, records the approved
plan, and then starts the scheduler. Events: `project_created`,
`intent_updated`, `onramp_overridden`, `discovery_started`,
`discovery_completed`, `direction_selected`, `plan_started`,
`plan_completed`, `plan_selected`, `plan_approved`.

## Consequences

- A precise project reaches work in **one required field and one approval**.
- An ambiguous project reaches work in one required field, one direction
  choice, and one approval. Limits and sources remain available but never
  gate the first model call.
- A project has one intent field, one stage function, and one approval
  record. Re-running intake after approval is a new plan, not an edit to the
  old graph.
- Existing brief rows are preserved through the 0045 backfill; `goal` and
  `success` are intentionally dropped (the user chose to remove project-level
  criteria).
- The benchmark's project-create payload no longer carries criteria; task
  criteria remain on `POST /projects/{id}/goals`.

## Alternatives rejected

- **Keep the brief and hide fields.** It leaves two goal fields, two
  workflow states, and UI-owned SQL. The fields are a symptom, not the cause.
- **Always explore.** Precise repository work pays an unnecessary discovery
  call and reading burden.
- **Store success criteria at the project.** The primary path ignored them;
  task criteria are the enforceable unit.
- **Persist the intake stage.** The durable sessions and task graph already
  determine it; a stored copy can diverge.
