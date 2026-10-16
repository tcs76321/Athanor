# ADR 0073 — The soak is the combined validation harness: endurance, conformance, and benchmark

**Status:** Accepted · **Date:** 2026-10-16 · **Refs:**
[ADR-0066](0066-release-scope-soak.md) (release scope, coverage-based soak),
[ADR-0063](0063-f4-closeout.md) (G7/G8 combined validation),
[ADR-0061](0061-harder-benchmark.md) (harder corpus);
`docs/soak-m7.md`, `docs/0.0.0-plan.md`; Gates G7/G8.

## Context

The `m3-t7-probe soak` subcommand holds one long-lived daemon over the hard
corpus and produces the Gate G7 endurance evidence ([ADR-0066](0066-release-scope-soak.md)):
bounded RSS/goroutines/WAL/disk, one `kill -9` recovery, and the time-triggered
subsystems firing. It also produces a quality readout. But:

- It exercises **only the goal path** (submit → engine loop → job pod →
  collect). HITL *decisions*, push, restore, freeze/unfreeze, corrections,
  export, strategy promotion, digest, alarms, and the UI are exercised by unit
  and integration tests, or by hand — never by the long-running daemon. So the
  soak's "the system survives" claim is narrower than it reads.
- The probe reads only one aggregate row per job (`strategy_outcomes`). The
  engine's **event log** already carries per-call `phase`, `persona`, `model`,
  `prompt_tokens`, `completion_tokens`, `temperature` and `sections`, plus
  `self_refine`, `reflection`, `divergence_reroll`, `difficulty_hint`,
  `kv_cache_*`, `context_floor_violation`, `git_committed`, `tests_ran`, and
  more — none of it reduced. The instrument throws away most of its signal.
- Per-model data is a mean score; there is no per-model leaderboard
  (accept rate, tokens per accepted artifact, wall-time percentiles).

## Decision

The soak is **one combined validation run** with three readouts from the same
long-lived daemon:

1. **Endurance** — unchanged ([ADR-0066](0066-release-scope-soak.md)): bounded
   growth, crash recovery, the time-triggered arms.
2. **Conformance** — a **scenario driver** that drives each subsystem
   end-to-end under the live daemon and asserts a grounded outcome, writing a
   pass/fail matrix into the rolling report. It is **offline**: no scenario
   reaches the network (the gateway is exercised only through its default-deny
   path). Scenarios land incrementally, starting with the ones that are
   *end-to-end-unproven* — push to a **local bare repository**, backup →
   **restore** into a scratch state dir, **freeze/unfreeze**, and **UI + SSE** —
   and unit/integration tests keep owning the rest. A scenario is added only if
   it proves something no existing gate already proves.
3. **Benchmark** — a **telemetry reducer** that reduces a job's event log into
   per-phase/per-persona calls and tokens and the engine's lever counters
   (repair, reflection, re-roll, candidate raise, KV/context), plus a
   **leaderboard** per model (accept rate, tokens per accepted artifact,
   wall-time p50/p95) and the existing `measure` → routing proposal. The
   rolling report emits machine-readable JSON alongside markdown.

The harness never introduces an external-web dependency, and it is
network-isolated by construction: the corpus is self-contained, the push remote
is a local bare repo, and the only web-facing subsystem is probed through its
deny path.

## Consequences

- One run produces endurance + conformance + benchmark evidence; the dated
  summary committed under `docs/probes/runs/` carries all three.
- The soak becomes the instrument for the attempt-escalation ladder (a later
  ADR) and for model routing: per-attempt accept curves and per-model
  quality/cost come from the same tree.
- The probe gains a reducer over an event stream it already has; no engine
  change is required to collect the data.
- Scope discipline: a scenario that only re-tests a unit gate is not added.

## Alternatives rejected

- **A separate end-to-end harness.** Duplicates the daemon lifecycle and the
  corpus; the soak already owns the long-lived process and the results tree.
- **Reduce telemetry in the Core and store rolled-up metrics.** Would change
  the engine's persistence and schema for a read-time concern; the event log is
  already the source of truth and the probe already reads it.
- **Network-fetching scenarios.** Non-deterministic, non-local, and against the
  local-first premise.
