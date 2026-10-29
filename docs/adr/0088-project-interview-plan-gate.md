# ADR 0088 — The project interview is a resumable plan gate

**Status:** Accepted · **Date:** 2026-10-29 · **Refs:**
`docs/first-use.md` F3; ARCHITECTURE §6.1 (project model), §20 (HITL),
§27 (UI); [ADR-0031](0031-project-execution-config.md) (execution config),
[ADR-0087](0087-cooperative-job-cancellation.md) (job start).

## Context

`project create` starts the first job immediately. That is the right default
for a CLI user with a precise goal, but it is the wrong shape for first use:
the operator has not yet clarified scope, risk, or the verification command,
and the project has no reviewable plan before machine time is spent. The
first-use specification calls for a resumable clarifying interview and a plan
the operator approves (F3), with a skip path.

A model-generated plan would add a second source of truth and a possible
prompt-injection surface. The plan is about intent, not prose quality.

## Decision

1. **A small, archetype-specific question set.** Three questions or fewer;
   `code` asks scope, verification, and what must not break; `data` asks schema
   stability, source, and invalid-row handling; `text`/`document` ask audience,
   facts to preserve, and style; other archetypes ask scope and constraints.
2. **The plan is deterministic.** `interview.BuildPlan` assembles Markdown from
   the project name, goal, criteria, execution test command, and answers. No
   model call, no invented requirements.
3. **The interview is resumable and skippable.** One `project_interviews` row
   per project (migration 0029); answers and the plan are stored before
   approval. `approved` and `skipped` both start the first job; `skipped` uses
   the goal/criteria/test command as the plan.
4. **The first job starts only on a decision.** `firstuse.StartProject` finds
   the first task with no job, creates one, and enqueues it. Re-approval
   returns the existing job instead of creating a duplicate.
5. **The API and UI share the state.** `GET/POST /projects/{id}/interview`,
   `POST .../approve`, and `POST .../skip`; the UI renders the same questions
   and plan at `/ui/projects/{id}/interview`.
6. **No HITL request is required for the plan gate.** The operator is already
   in the loop in the UI/API; the plan decision is a direct authenticated-local
   action, not an external side effect.

## Consequences

- A metadata-only project can be created, clarified, and approved without
  spending model time; the first job is still a deliberate action.
- The plan is auditable and reproducible; a later change to the goal or
  criteria does not silently rewrite an approved plan.
- The `project_interviews` row is small and project-scoped; deleting a project
  cascades it.
- The UI's start checkbox still exists for the direct CLI-style path. The
  interview page is reachable from every project; making it the mandatory first
  path is a UI-product decision, not a schema constraint.
- A plan can be skipped, so the gate never makes first use harder than the
  pre-F3 flow.

## Alternatives rejected

- **LLM-generated plan.** Slower, non-deterministic, and invites fabricated
  requirements; the operator is reviewing intent, not prose.
- **Block the first job on a HITL queue item.** Duplicates the human action and
  adds an expiry race for an internal, reversible decision.
- **Edit `project create` to always stop.** Breaks the CLI/API contract and the
  existing M9-T5 behavior; the metadata-only path already exists.
