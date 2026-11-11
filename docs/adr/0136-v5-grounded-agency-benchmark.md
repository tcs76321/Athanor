# ADR 0136 — v5 is the grounded, local-first agency benchmark

**Status:** Accepted · **Date:** 2026-11-10 ·
**Refs:** [ADR-0075](0075-frozen-corpus-compass.md),
[ADR-0092](0092-benchmark-label-integrity.md),
[ADR-0116](0116-benchmark-portfolio.md),
[ADR-0133](0133-paired-0x-benchmark-v3.md),
[ADR-0134](0134-project-cycles-and-blocks.md),
[ADR-0135](0135-v4-utility-benchmark.md), ARCHITECTURE §19.

## Context

v3 was the paired 0.x instrument, but its quality smoke failed its own
agreement gate and the product then changed shape. v4 measured the utility
cycle, but only on a small seed and with a single external source. Neither
instrument measures the architecture the product now wants to defend:
grounded work, cycle/memory composition, repair, and a local-only model roster
that can run on the reference host.

v5 is a new instrument, not an edit to v4. v4 stays in the tree unchanged.
v5 numbers are not comparable to v4 or v3.

## Decision

### 1. v5 is a candidate instrument, not a release claim

`eval/bench/v5/` measures the shipped Core Pod on two local generators and two
local judges. It is not tied to `0.0.0`; no release has occurred. The corpus
stays `status: candidate` and `cut: pending` until every pre-registered hard
gate is re-run after the P0 fixes and the digest is frozen.

### 2. Local-only 2×2 roster

Generators: `bonsai-2:27b` and `gemma4:12b-mlx`.
Judges: `granite4.2:8b` and `mimo-v2.6:9b-mxfp8`; the in-loop judge is
granite.

The headline is deterministic and external: a task is labeled by a probe-owned
re-run of frozen tests/checks, never by the engine's own acceptance or the LLM
judge ([ADR-0092](0092-benchmark-label-integrity.md)). Engine acceptance,
cycle completion, judge scores, and mechanism telemetry are reported
separately.

### 3. Claim suites

- **foundation** — atomic code, data, document, and text tasks; paired
  `single` vs `full`.
- **agency** — multi-file repair and long-horizon composition; the suite must
  discriminate. A 100% suite is reported as saturated, not as evidence.
- **utility** — brief and news cycles over local `sources/` and `wire/`
  fixtures, scored by the external citation/new-item checks. Cycle completion
  is a separate mechanism gate.
- **safety** — injection, exfiltration, path escape, and HITL refusal. Safety
  is a hard gate; a single failure means the 2×2 safety claim is not
  established.
- **memory** — a correction or insight applied in a later job, with injection
  evidence from the event log.
- **quality** — blind pairwise A/B judging, both orders, two judges, with
  agreement and a task-clustered interval. Pointwise rubric scores are
  reference only.

### 4. Deterministic verification is authoritative

A passing hard verifier decides acceptance. The LLM judge may add
`missing_criteria` as advisory quality notes, which are audited, but it does
not veto a candidate whose declared checks or real tests passed. A hard
security finding still blocks.

Block contracts compile deterministic acceptance checks per task: the final
synthesize artifact is checked against the cycle's required sections and
bounds, and internal verify/retain blocks have structural acceptance. This
keeps a cycle from wedging on an advisory prose disagreement.

### 5. External validation track

`eval/bench/v5/aider-polyglot.yaml` is a versioned external validation track
over official Exercism exercises. It uses a custom Athanor adapter, runs the
official tests in a fresh network-none container after Athanor exits, and is
not an official Aider leaderboard entry. The track is single-arm by default;
a 4-exercise full-arm subset is reported separately. The external track does
not tune the claim corpus.

### 6. Frontier tier is a later, separate track

v5 contains no frontier model and makes no frontier-parity claim. A frontier
leaderboard entry requires an OpenAI-compatible inference backend and a live
leaderboard door; it is a follow-on decision, not a v5 score. Local-first
remains the product goal.

### 7. Freeze rules

Freeze v5 only when all of the following hold after the P0/P1 reruns:

- Aider engine acceptance converges with the external tests;
- utility external pass meets the protocol threshold and every cycle completes;
- safety is 4/4 for both generators;
- memory correction passes with proven injection;
- news day1/day3 pass, with day2 reported honestly as held-out;
- agency is no longer saturated or is explicitly excluded from the claim;
- quality pairwise agreement meets the protocol floor;
- the analyzer reads the protocol, quality, and external data and omits unrun
  arms;
- reports, evidence, this ADR, and the v5 README are committed;
- `CORPUS.digest` is regenerated and `cut` is no longer `pending`.

A failed hard gate is reported as a red claim. It is never waived. If a gate
cannot be met, v5 stays a candidate and the next instrument carries the fix.
