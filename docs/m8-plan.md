# M8 plan — Integrated Validation & Cognitive Regulation → 0.0.0

**Status:** living · **Date:** 2026-10-15 · **Refs:**
[ROADMAP](../ROADMAP.md) §3 (G7/G8), §6 (M8);
[ADR-0063](adr/0063-f4-closeout.md), [ADR-0064](adr/0064-cognitive-operations.md),
[ADR-0066](adr/0066-release-scope-soak.md),
[ADR-0067](adr/0067-technique-guidance-loop.md);
[cognitive-operations.md](cognitive-operations.md);
[m8-workflows.md](m8-workflows.md); [m8-process-matrix.md](m8-process-matrix.md).

This document freezes **M8's scope and the finite 0.0.0 acceptance set**. M8's
plan was previously tracked in-conversation; recording it here is the point —
"is M8 done?" must have an answer. The done-set below is the work recorded in
[CHANGELOG.md](../CHANGELOG.md); the remaining set is what 0.0.0 requires.

## 1. Context

F4 closed **inconclusive** for an instrument reason (a saturated M3-T7 task set,
a provisional anchor) and seeded M8 (ADR-0063). M8 asks not "is the dialectical
engine good?" but **which cognitive operations earn their keep, where, and for
whom**. Its instrument is the harder, deterministic-first corpus (ADR-0061).

Two gates remain, both human checkpoints. ADR-0063 fused them into one combined
validation run; [ADR-0066](adr/0066-release-scope-soak.md) re-specifies the soak
by coverage rather than a fixed 24-hour wall-clock.

## 2. Done (recorded in the changelog)

| Task | What landed |
|---|---|
| M8-T1 | Gateway policy denial is distinguishable (`451` + `ErrPolicyDenied`); O1 |
| M8-T2 | `GET /internal/v1/jobs/{id}` resolves the real job; O2 + F-3 |
| M8-T3 | Scope-isolation structural gate (`gate_scope_test.go`) |
| M8-T4 | Documented deliberate trust boundaries (`SECURITY.md`) |
| M8-T6 | Cognitive-operations model ([ADR-0064](adr/0064-cognitive-operations.md)) |
| M8-T7 | Strategy trajectory: `strategy_outcomes.operations_json` (migration 0024) |
| M8-T8 | Operation-selection seam + shared `internal/cognitive` vocabulary |
| M8-T9 | Novelty detection + anti-rut reset (`novelty_reset: auto`) |
| M8-T10 | Exploration floor (`OperationSamples`, `ensureExploration`) |
| M8-T11 | Self-refine operation (opt-in `self_refine`, bounded) |
| M8-T14 | Probe JSON-schema validator + `must_contain` check |
| M8-T16 | Bench-corpus loader in the probe |
| M8-T17 | Multi-file code artifacts + fixture staging ([ADR-0065](adr/0065-multi-file-code-artifacts.md)) |
| M8-T18 | Deterministic non-code check engine in the probe |
| M8-T19 | Repo-backed task wiring (index a copy + seed the working set) |
| M8-T21 | Corpus-aware probe report (tier × arm deterministic pass rate) |
| M8-T22 | Component ablation arms (single/bestof3/reflect/full) |
| M8-T23 | Combined endurance + quality soak harness (one daemon, resumable) |
| M8-T24 | Process & recovery matrix |
| M8-T25 | Real-world-internal workflow map |

## 3. Remaining for 0.0.0

| Task | Deliverable | Gate |
|---|---|---|
| M8-T26 | Technique mining over grounded trajectories | G8 (selection learned) |
| M8-T27 | Technique guidance channel (advisory, audited) | G8 |
| M8-T28 | Auto-activate winning techniques (strict, bounded, reversible) | G8 |
| M8-T29 | Exploration blend on top of the novelty reset + floor | G8 |
| M8-T30 | Guidance A/B measurement in the probe | G8 |
| M8-T31 | Soak `-until <deadline>` + goroutine sampling + compressed time-arms | G7 |
| fix(config) | Default `inference.ollama_url` → `127.0.0.1:11434` (host-binary Core) | G7 (fresh install) |
| — | Combined validation run, fresh-install demo, §31.3 audit sign-off | G7 + G8 |
| — | CHANGELOG 0.0.0 cut, version stamp, `v0.0.0` tag | release |

## 4. The 0.0.0 acceptance set

A candidate is **0.0.0** when all of the following hold:

**Gate G7 (release)**
- `make check`, `make test-integration`, `go test ./internal/gate/` green.
- A soak run shows no orphan pods, no unrecovered `kill -9`, bounded
  RSS/goroutines/WAL/disk, one human sleep/wake, and the time-triggered arms
  exercised (compressed schedules + their unit tests) — per
  [ADR-0066](adr/0066-release-scope-soak.md).
- Fresh-install demo < 15 minutes on a clean state dir.
- §31.3 audit checklist signed off in [m7-gate-g7.md](m7-gate-g7.md).

**Gate G8 (integrated validation & cognitive regulation)**
- End-to-end run over the hard corpus (`eval/bench/`) loses no state.
- The technique-guidance loop is demonstrated: techniques mined from grounded
  trajectories → activated → injected, with an observed grounded delta
  ([ADR-0067](adr/0067-technique-guidance-loop.md)).
- A novel task class resets learned bias toward the shipped default heuristics
  blended with exploration randomness (novelty reset + exploration blend).

## 5. Deferred (recorded, in the repertoire, not invoked)

- **M8-T8b (selection applied at execution as narrowing)** — **superseded** by
  the advisory technique-guidance channel (ADR-0067). The repertoire is never
  pruned.
- **M8-T12 (adversarial self-critique, tree search)** — deferred with rationale:
  no cheap verification edge (see [cognitive-operations.md](cognitive-operations.md) §7).
- **M8-T13 (human-eval calibration intake)** and **T15/T20 (corpus enabling
  work: fixtures, test suites, anchor pairs)** — not referenced elsewhere in the
  tree; anchor-pair authoring remains partial. Tracked here so they are not
  silently lost.

## 6. Post-0.0.0

Statistical power for the guidance claim; deterministic verifiers for
`data`/`media`; model onboarding/qualification ([ADR-0049](adr/0049-model-onboarding-qualification.md));
the brainstorm corpus task; M8-T12; Browser Mode; the cloud credential broker;
the skills runtime and `skill_refinement`; Exploration Paths; the full
Statistics/Memory-Browser views.
