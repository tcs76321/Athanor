# ADR 0072 — Single-slot execution: one job, one model, one pod

**Status:** Accepted · **Date:** 2026-10-16 · **Refs:**
ARCHITECTURE §8 (job state machine), §10.4 (KV/eviction), §12.5 (model
lifecycle), §24 (power & idle);
[ADR-0071](0071-git-as-undo-containment.md) (containment);
[ADR-0066](0066-release-scope-soak.md) (soak). Amends ARCHITECTURE §24.

## Context

Athanor runs on one local machine for one operator, and in the intended
operating mode runs the largest model the hardware fits at the largest context
it allows. That mode requires the model to stay **resident** and serve **one
request at a time**: a second model evicts the first, and a second job
interleaving personas forces constant load/evict thrash — and corrupts any
per-model timing measured during it.

The code did not enforce that:

- Job concurrency was configurable (`limits.max_concurrent_jobs`, default **2**),
  and the power profiles raised it further: `power.profileLimits` returned
  `MaxConcurrentJobs: 4` for `autonomous` (`internal/power/manager.go`).
- Nothing in Athanor serialized inference. `limits.max_concurrent_llm_calls` was
  parsed, defaulted, validated, and **documented — but never read** by any
  production code.
- Single-model residency was delegated entirely to Ollama's
  `OLLAMA_MAX_LOADED_MODELS=1`.

So an idle-on-AC daemon could place four jobs in flight against one resident
model. The invariant the operator expects — one model, one job, one pod, one at
a time — held only by luck.

## Decision

1. **One job at a time.** `PowerManager.MaxConcurrentJobs` returns **1** for
   every profile, and `limits.max_concurrent_jobs` defaults to **1**. The
   `autonomous` profile continues to gate *daydreaming* and CPU quota; it no
   longer raises job concurrency.
2. **One model call at a time.** `limits.max_concurrent_llm_calls` stops being
   dead: the Ollama client takes a process-wide limiter sized from it (default
   **1**), so at most one inference call is in flight. This is defence in depth —
   single-slot jobs already issue calls sequentially — and it makes the
   documented knob true.
3. **One pod at a time follows.** One job owns at most one Job Pod, so pod count
   is bounded by the same invariant.

## Consequences

- `autonomous` vs `interactive` no longer differ in job concurrency — only in
  daydreaming and CPU quota.
- The soak's generated `max_concurrent_jobs: 1` is now the production default,
  not a special case; the probe's single-model-residency check stays as a guard.
- Attempt-level escalation (a later ADR) searches **attempts sequentially**, not
  parallelism; single-slot execution makes that search simple and cheap.
- A deployment that wants job parallelism is out of scope by decision, not by
  accident.

## Alternatives rejected

- **Per-model serialization gate** (N jobs, serialized per resident model).
  Preserves throughput but adds a scheduler dimension for a single-operator
  local daemon whose stated mode is one big model at a time.
- **Leave residency to Ollama.** Produces thrash, not serialization, and hides
  the problem from Athanor's own metrics — the state that prompted this ADR.
