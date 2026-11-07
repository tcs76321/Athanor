# ADR 0124 — Context and capacity planning, coverage, and degraded modes

**Status:** Accepted · **Date:** 2026-11-07 · **Refs:** ADR-0002, ADR-0022,
ADR-0023, ADR-0096, ADR-0104; ARCHITECTURE §10, §12, §24, §29;
`internal/capacity`, `internal/engine`, `internal/llm`, `internal/onboard`

## Context

Context size and concurrency were independent knobs. Every code job used the
static `context_engine.coding_floor` (32k), personas carried fixed context
targets, and `max_concurrent_jobs` was a ceiling with no model-residency
accounting. That is correct for one large model on a large host, but wrong for
a 32 GiB machine running several small models, and wrong for a low-resource
machine that cannot cover the `wide`/`tall` roles at all.

Three facts force a joint plan:

1. A model's KV cache is `kv_per_token × num_ctx × parallel_sequences`.
2. Changing `num_ctx` makes Ollama reload the model, so per-phase context
   changes thrash a multi-model residency.
3. A machine may have one model, no embedding model, no second family, or no
   Job Pod runtime. The system must detect the combination and degrade
   honestly instead of failing or pretending coverage.

## Decision

### 1. One capacity planner

`internal/capacity` is a pure package. Given measured machine/model facts and
per-task demand estimates, it returns:

- a **solo plan** (best model/window for one job);
- **swarm plans** for pod-backed code work and podless light work;
- **coverage** (roles, families, embedding, code execution, capabilities);
- **degraded reasons**; and
- **mission lanes** (solo + swarm) for a set of tasks.

It performs no I/O and imports only the standard library.

### 2. Context is per job, phase-aware, and bucketed

- Demand is `short | long | repo | memory`, inferred deterministically and
  refinable by the planner's `CONTEXT:` hint (a hint may raise demand, never
  lower it).
- Floors are phase-aware: planning and judgment use their own floors and do
  not inherit the repo floor; generation/synthesis use the demand floor.
- `target = max(floor, bucket_up(pinned + working_set + output + safety))`,
  clamped to the model's measured max.
- One bucket is pinned per (model, occupancy window). Output reserves may vary
  per phase; `num_ctx` does not, so the model is not reloaded mid-job.
- If the floor cannot be met, the capability is unavailable or the job pauses.
  Context is never silently reduced.

### 3. Swarms are for smaller tasks than the machine's solo capability

A task is eligible for a swarm only when:

- it is not `repo` demand and not `hard`;
- its need is at most `swarm_task_ratio` (default 0.5) of the solo window;
- it fits the swarm slot's context and model score; and
- the mission's 75th-percentile task is not too large.

When a mission has an ineligible task, one solo slot is reserved and the
largest swarm lane gives up one slot. A swarm job cannot spill into the
reserved solo slot.

### 4. Homogeneous and heterogeneous casts

- Homogeneous: N copies of one model. Weights are resident once; KV is counted
  per sequence. This is the cheap throughput path for small/easy tasks.
- Heterogeneous: distinct families, equal slots. This is the diversity/quality
  path and is also what a cross-family judge needs.
- `cast_policy: auto` chooses homogeneous for easy/short work and
  heterogeneous or solo for hard/long/repo work.

### 5. Coverage and degraded modes

The planner reports, and the UI/doctor expose:

- one model → role fallback (`tall→main→only`, `wide→main→only`, etc.),
  homogeneous casting, same-family judgment with an audit;
- no embedding model → FTS-only retrieval (`vector memory disabled`);
- no second family → same-family advisory judgment;
- no Job Pod runtime → code blocked, text/document/data continue;
- VM too small → no pod swarm, light swarm may still run;
- no model fits → pause with a smaller/quantized-model recommendation;
- `degraded_policy: lite` is opt-in only and is audited.

The shipped `max_concurrent_jobs` stays 1. Onboarding applies the measured
plan; the operator can accept it.

## Consequences

- A 32 GiB machine can run a real mini-swarm of small models, and a low-resource
  machine can run text/document work honestly with one model.
- The engine, UI, doctor, and benchmark share one vocabulary for "what can this
  machine do".
- The capacity package needs measured `weights`, `kv_per_token`, and
  `max_context`; a missing measurement degrades to a conservative estimate or
  an explicit unavailable capability.
- `context_policy` remains `archetype` until the paired evidence run; adaptive
  is the target default after that.
