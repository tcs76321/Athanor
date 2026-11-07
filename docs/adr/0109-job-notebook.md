# ADR 0109 — Persistent job notebook (working memory)

**Status:** Accepted · **Date:** 2026-11-06 · **Refs:**
ARCHITECTURE §10, §11.2, §13; ADR-0023 (context assembly), ADR-0064
(cognitive operations), ADR-0070 (declared checks); `internal/notebook`,
`internal/engine`, `internal/prompt`

## Context

Athanor's phases each receive a fresh prompt assembled from project/task
context, criteria, corrections, memory, and candidates. The event log records
everything, but it is an audit trail, not a model-readable state: it is too
large to inject verbatim and it is ordered by write, not by relevance.

The result is a **working-memory gap**. The plan is generated once and then
mostly disappears from the model's view. Evaluation results and regulator
decisions are rendered into per-phase instructions, but there is no explicit
record of "what we tried, what happened, and what we decided" that survives
across phases. Retrospection is a prompt phase, not persistent state. On a
long or retried job the model can repeat a failed approach because the failure
is not part of its working set.

This is the same gap an interactive coding agent closes with a live todo list
and a conversation transcript. Athanor deliberately does not keep a chat
transcript (it is unbounded and noisy), but it needs the bounded, structured
equivalent.

## Decision

Add a **per-job notebook**: an append-only, bounded table of structured
working-memory entries, injected into every phase in pinned tier 2.

- **Schema (migration 0038).** `job_notebook` stores `(id, job_id,
  project_id, kind, phase, content, created_at)`. `kind` is a closed set:
  `plan`, `hypothesis`, `observation`, `experiment`, `result`, `decision`,
  `question`, `answer`. No foreign keys: like the event log, the notebook is
  derived state and must not make job/project retention brittle.
- **Bounded.** The store keeps at most 200 entries per job and trims one entry
  to 8 KiB. The prompt renders at most 20 entries / 4 KiB, one line each.
  A long job cannot grow the notebook without limit.
- **Written by the engine, not the model.** The engine appends the plan after
  planning, a hypothesis summary after divergence, one result per evaluated
  candidate, the regulator decision and reflection output, the synthesis
  decision, and the final comparison verdict. This is deliberate: the notebook
  is a faithful record of what the *engine* actually did, not a model
  self-report. Model-authored notes can be added later through a separate,
  audited path.
- **Pinned, not evictable.** `prompt.SectionNotebook` renders in
  `TierTaskCriteria` (the same pinned tier as criteria and declared checks).
  The §10.5 eviction ladder cannot drop the plan or a recorded failure.
- **Best-effort writes.** A notebook failure logs a warning and never fails
  the job; the event log remains the authoritative audit trail.

## Consequences

- The model sees a compact, chronological narrative of the job:
  `[plan/planning] …`, `[result/evaluating] candidate … failed …`,
  `[decision/reflecting] regulator: reframe — …`, and the final verdict.
- Repeated approaches are visible. A reflection can reference the actual
  earlier failure rather than reconstructing it from the current prompt.
- The notebook is the foundation for the next steps: cross-job project notes,
  model-authored hypotheses, and a "job summary" that feeds project memory.
- Cost: one bounded table, a few rows per job, one extra pinned prompt section
  (typically a few hundred tokens). The 200-entry/8 KiB caps keep it small.

## Alternatives rejected

- **Inject the event log.** It is unbounded, noisy, and not written for the
  model. A filtered view is possible, but the notebook gives an explicit,
  typed, bounded schema now.
- **Store the notebook in `system_state` as JSON.** It would grow without a
  row bound, be hard to query, and mix operator state with job state.
- **Let the model write the notebook directly.** That would make the notebook
  another self-report channel and a prompt-injection surface. The engine
  writes facts it observed; model hypotheses are recorded as hypotheses, not
  as truth.
