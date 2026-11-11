# ADR 0136 — v5 is the 0.0.0 grounded-agency benchmark

**Status:** Accepted — amended 2026-11-11 (v5 is the 0.0.0 instrument) ·
**Date:** 2026-11-10 ·
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

### 1. v5 is the 0.0.0 instrument

`eval/bench/v5/` measures the shipped Core Pod on two local generators and two
local judges. **v5 is the benchmark 0.0.0 is gated on.** 0.0.0 tags only when
v5 (or its successor) is frozen at one revision, its release-blocking claims
are green or explicitly excluded, and the report and provenance are committed.

If v5 cannot meet its release-blocking gates, **v6 is cut as the 0.0.0
instrument**; v5 stays `status: candidate` and is never edited after v6 exists.
The old mapping is historical: v2 was the 0.0.0-era pilot, v3 the paired 0.x
experiment, and v4 the superseded utility instrument. ADR-0135 §1 is
superseded for the 0.0.0 benchmark.

The benchmark is the **baseline for tracking improvement**, not a claim of
feature completeness. 0.0.0 is the point where the frozen instrument, the
GUI day-loop, and the existing install/security/soak gates are all satisfied.

### 2. Local-only 2×2 roster

Generators: `bonsai-2:27b` and `gemma4:12b-mlx`.
Judges: `granite4.2:8b` and `mimo-v2.6:9b-mxfp8`. The **in-loop** judge is
granite; the **blind pairwise quality panel** is separate and admits only
judges that pass the anchor agreement floor. A model can be a sound in-loop
security judge and still be disqualified from pairwise judging by position
bias.

The headline is deterministic and external: a task is labeled by a probe-owned
re-run of frozen tests/checks, never by the engine's own acceptance or the LLM
judge ([ADR-0092](0092-benchmark-label-integrity.md)). Engine acceptance,
cycle completion, judge scores, and mechanism telemetry are reported
separately.

Each benchmark task gets one scheduler job (`max_task_retries: 0`). The `full`
arm's multiple candidates and reflection are inside that job; the benchmark
does not hide a retry behind a row. The external track may skip the repository
index pass because its official tests do not depend on it.

### 3. Claim suites

- **foundation** — atomic code, data, document, and text tasks; paired
  `single` vs `full`.
- **agency** — multi-file repair and long-horizon composition; the suite must
  discriminate. A 100% suite is reported as saturated, not as evidence.
- **utility** — brief and news cycles over local `sources/` and `wire/`
  fixtures, scored by external citation/new-item checks. The claim is a set of
  **component gates**, not a "90% of five tasks" aggregate: both briefs, both
  tuning news days, the held-out day2, and 100% cycle completion must pass. The
  aggregate rate is reported with a Wilson interval and is not called a 90%
  claim. Cycle completion is a separate mechanism gate.
- **safety** — injection, exfiltration, path escape, and HITL refusal. The
  suite measures instruction-following/refusal; containment itself is Gate G2
  plus the §31.3 audit. A single benchmark failure means the 2×2 safety claim is
  not established.
- **memory** — a correction or insight applied in a later job, with injection
  evidence from the event log.
- **quality** — blind pairwise A/B judging, both orders, two anchor-qualified
  judges, with agreement, a minimum usable-pair count, and a task-clustered
  interval. If no two judges reach the floor, the claim is `not_measured` and is
  removed from the release vector rather than reported as a low number.
- **cost** — the full arm's token/wall ratio on tasks both arms pass; the
  protocol's envelope is a guardrail, not a capability claim.
- **mechanism coverage** — task-level `requires` plus arm-level contracts
  (full must diverge and compare; cycle must complete). A pass without the
  mechanism is a capability gap.
- **reliability** — agency pass@1 and pass@2; pass@2 is reported, not a release
  gate at n=5.

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

Freeze v5 only when **all** of the following hold:

- the full 2×2 was run at **one commit** with a clean tree (no
  `mixed_revisions`);
- the Aider engine acceptance converges with the external tests;
- **utility component gates** pass: briefs 2/2, tuning news 2/2, held-out day2
  1/1, cycle completion 100%;
- **safety** is 0 failures for both generators, with the benchmark's
  instruction-following scope stated separately from Gate G2/§31.3;
- **memory** correction/insight passes with proven injection;
- **mechanism coverage** is 1.0 (task-level requires and the arm contracts);
- the **foundation non-inferiority** claim holds (full ≥ single − 10 points,
  with a task-clustered interval);
- **quality** is either established by two anchor-qualified judges with
  agreement ≥ 0.6 and enough usable pairs, or explicitly `not_measured` and
  removed from the release vector;
- agency is reported as saturated/directional, not as a non-inferiority claim;
- **cost** is reported; exceeding the envelope is a red claim, not a waiver;
- the analyzer reads the protocol, quality, and external data and omits unrun
  arms; every declared claim has a computation path;
- reports, evidence, provenance, this ADR, and the v5 README are committed;
- `CORPUS.digest` is regenerated, `status: frozen`, and `cut: 0.0.0`.

A failed hard gate is reported as a red claim. It is never waived. If a gate
cannot be met, v5 stays a candidate and the next instrument (v6) carries the
fix as the 0.0.0 instrument.

---

## Amendment — 2026-11-11: v5 is the 0.0.0 benchmark

This amendment records the release-line decision and the claim-vector corrections:

1. **v5 is the 0.0.0 instrument.** 0.0.0 was waiting on three things: the
   benchmark, a fully functional GUI day-loop, and a defensible baseline for
   tracking improvement. The frozen v5 report is that baseline. If v5 cannot
   meet its gates, v6 is the 0.0.0 instrument; v5 is not edited afterward.
2. **Version map.** v2 historical pilot · v3 historical paired experiment ·
   v4 superseded utility instrument · v5 0.0.0 · v6+ per major.
3. **Utility is component-gated**, including the held-out day2. The old 90%
   aggregate was a 5/5 perfection gate wearing a percentage and could not be
   statistically established at n=5.
4. **Ghost claims removed.** `operations` (no tasks), `recovery` (G7/24h soak),
   and `grounding` (needs a closed-book arm) are not v5 claims. `cost`,
   `mechanism_coverage`, and `reliability` are computed.
5. **Safety scope.** The benchmark's safety suite is instruction-following and
   refusal; containment remains Gate G2 plus the §31.3 audit. The two are
   reported separately.
6. **Evidence integrity.** A committed report must match its committed raw rows
   and provenance; `make bench-v5-verify` fails on a stale number, a moved
   corpus/protocol digest, a ghost claim, or a missing run.
7. **ADRs 0061/0133/0135 are historical** for the 0.0.0 gate. They remain
   valid records of the pilot, paired experiment, and superseded utility
   instrument.
