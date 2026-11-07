# ADR 0115 — Reframe as an explicit plan revision

**Status:** Accepted · **Date:** 2026-11-06 · **Refs:**
ARCHITECTURE §13.1, §13.3; ADR-0109 (notebook), ADR-0090 (regulator),
ADR-0064 (cognitive operations); `internal/engine`, `internal/prompt`

## Context

The regulator already distinguishes *refine* (same frame, better execution)
from *reframe* (the frame itself is wrong). The reflection phase produces a
proposal, and the next divergence pass receives it through
`getReflectionProposal`. That is a real retry improvement, but it is not a
**plan of record**: the original plan from the planning phase is still in the
notebook, the proposal is only an observation, and the model has no explicit
signal that the original approach has been superseded.

The result is a subtle drift: the model can read "first plan" and "reflection
note" and choose either, or blend them. DAG-level replanning (re-decomposing
the remaining graph) is the larger T3b item; this is the smaller, immediate
step that makes a reframe change the plan the model follows.

## Decision

When the regulator returns **Reframe**, append the reflection output as a new
**plan** entry in the job notebook, phase `replanning`, and audit `replanned`.

- The notebook is chronological; a later `plan` entry is the current plan.
  The prompt header says explicitly: "a later plan supersedes an earlier one
  when they conflict."
- The original plan remains visible. The model can see what it is abandoning
  and why; the notebook is append-only, not a destructive overwrite.
- The reflection proposal still reaches the next divergence through the
  existing seam. This adds the durable, explicit plan-of-record, not a second
  mechanism.
- DAG-level re-decomposition remains a separate follow-up: this changes the
  plan inside one task; it does not rewrite the task graph.

## Consequences

- A reframe is visible as a plan change, not only as a prompt hint. The next
  phase sees the revised approach in tier 2 and cannot evict it.
- The audit trail distinguishes "we tried again" (Refine) from "we changed the
  approach" (Reframe) and records the new plan.
- Cost: one more notebook entry per reframe (bounded by the notebook caps).
- A future DAG replanner can consume the same `plan/replanning` entries as
  evidence that the current decomposition is wrong.
