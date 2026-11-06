# ADR 0108 — Project priority with a single critical project

**Status:** Accepted · **Date:** 2026-11-06 ·
**Refs:** ARCHITECTURE §6 (project model), §24 (capacity);
[ADR-0096](0096-capacity-aware-concurrency.md) (capacity-aware concurrency).

## Context

Athanor can hold several projects but had no operator-facing way to say which
one matters most. The dashboard and project list were ordered by creation time,
which is the wrong signal when an operator is waiting on one project and
parking another. The scheduler itself is single-slot by default; project
priority is therefore primarily an attention and ordering signal, not a
preemption mechanism.

A "critical" tier is useful but dangerous if it becomes a free-for-all: several
critical projects would defeat the point and make the label meaningless.

## Decision

1. **Four priorities:** `critical`, `high`, `medium` (default), `low`. The set
   is closed and validated by the repository.
2. **At most one critical project.** A partial unique index
   (`projects_one_critical`) enforces it in SQLite. `SetPriority` demotes the
   previous critical project to `high` in the same transaction, so the
   invariant is never transiently violated.
3. **Priority orders the surfaces operators use to choose work:** the dashboard
   cards, the project list, and the project-query order used by the UI. Within
   a priority, creation order remains the tiebreak.
4. **No silent preemption.** Setting a priority does not kill or pause a running
   job. The project page says so explicitly. Preemption would require a job
   cancellation policy and is not implied by a label.
5. **API and UI share the same repository operation.** `POST
   /ui/projects/{id}/priority` and `PATCH /projects/{id}/priority` call
   `project.Repo.SetPriority`; there is no second semantics.
6. **The UI explains the critical demotion.** Selecting Critical on a project
   demotes the previous critical project to High; the settings copy states this
   and the change is audited.

## Consequences

- The operator can express attention without editing data by hand.
- The single-critical invariant is structural, not convention.
- Priority affects ordering and review attention; with single-slot execution it
  does not reorder an already queued job. A future admission queue can consume
  the same field without a schema change.
- A project created before migration 0037 reads as `medium`.

## Alternatives rejected

- **Free-form integer weights.** Harder to explain, easy to tune into noise.
- **Multiple critical projects.** Makes the label meaningless.
- **Priority preempts running work.** Surprising and unsafe without a
  cancellation/HITL policy.
- **Priority only in the UI.** The API and UI would drift; the repository is
  the single writer.
