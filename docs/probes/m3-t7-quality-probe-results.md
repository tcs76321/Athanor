# M3-T7 — Results

Protocol and smoke findings: [`m3-t7-quality-probe.md`](m3-t7-quality-probe.md).
Full run 2026-10-05, `results/main2`, generator `ornith-1.5:9b` only.

## Environment

- Machine: Apple M2 Max, 32 GB unified memory, macOS (darwin/arm64).
- Ollama 0.35.1.
- Models + digests (`ollama list`):
  - generator `ornith-1.5:9b` `e5df7dcdd8a2` (6.6 GB)
  - judge `gemma4:12b-mlx` `117d0d84cf2a` (7.7 GB)
  - judge `granite4.2:3b` `7a65e6414ad6` (2.2 GB)
- Config: `inference.think: false`, `max_output_tokens: 4096`,
  `json_format: true`, `json_schema: false`.
- Reflection: on (matrix default); `-no-reflect` not used.
- Wall time ≈ 66 min total (dialectical arm ~58 min / 30 jobs; single arm
  ~8 min / 10 jobs).
- Soak: dialectical `samples=111 peak_rss=29MB max_db=5.5MB min_free=280.8GB`;
  single `samples=24 peak_rss=29MB max_db=4.5MB min_free=279.3GB`.
  **0 orphan pods** after either arm.

Two collection defects were found and fixed during post-processing
(`M3-T7.15`/`.16`): a ~10 s code-goal outcome-capture lag (arm teardown could
lose the last row), and the probe's SQLite build having **no `json_extract`**
(so the per-cycle diversity event reader had always silently fallen back). The
numbers below are after `make probe-reconcile`.

## Headline — dialectical (N=3) vs single (N=1)

Mean score over runs (dialectical n=3, single n=1), mean token cost.

| goal | archetype | single score | dialectical score | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|
| always-reverse | code | 1.00 | 1.00 | +0.00 | 16037 | 20170 |
| book-collection | code | 1.00 | 1.00 | +0.00 | 4272 | 13162 |
| cache-class | code | 1.00 | 1.00 | +0.00 | 8106 | 14966 |
| fibonacci | code | 1.00 | 1.00 | +0.00 | 6691 | 12991 |
| local-first-essay | text | 0.97 | 0.95 | **−0.02** | 5301 | 10047 |
| md2html-readme | document | 1.00 | 1.00 | +0.00 | 7623 | 8459 |
| onboarding-email | text | 0.92 | 0.96 | **+0.04** | 3873 | 8638 |
| stringutils | code | 1.00 | 1.00 | +0.00 | 4539 | 14224 |
| sunrise-alarm-brief | document | 0.92 | 1.00 | **+0.08** | 9151 | 10839 |
| todo-list | code | 1.00 | 1.00 | +0.00 | 4910 | 12512 |

**Summary.** Dialectical **beat on 2 goals** (onboarding-email +0.04,
sunrise-alarm-brief +0.08), **lost on 1** (local-first-essay −0.02), **tied on
7** (all at the 1.00 ceiling). Cost: ~**1.8×** total tokens (70,503 → 126,008);
per-goal 1.1× (md2html) to 3.1× (book-collection). The engine rubric has almost
no variance to move, so this is "no detectable signal," not "proven equal."

## T-a — candidate diversity (per cycle)

From the engine's `divergence_jaccard` events (single is 0 by construction).

| arm | mean diversity | n |
|---|---|---|
| dialectical | **0.795** | 30 |
| single | 0.000 | 10 |

The divergence phase does its job: candidates are textually distinct.

## T-b — judge-confidence calibration (dialectical arm)

Reliability diagram, engine confidence vs observed score.

| confidence bin | n | mean observed score |
|---|---|---|
| [0.5,0.6) | 0 | — |
| [0.6,0.7) | 0 | — |
| [0.7,0.8) | 0 | — |
| [0.8,0.9) | 0 | — |
| [0.9,1.0) | 30 | 0.99 |

**Degenerate.** Every verdict landed in [0.9,1.0), mean observed 0.99 — the
confidence signal is a near-constant and carries no information, and the rubric
saturates at 1.00. T-b cannot be assessed from this run.

## T-c — verdict stability at T=0 (dialectical arm)

| class | goals |
|---|---|
| 3-same | 7 |
| 2-1 | 3 |
| 3-way | 0 |

Mostly stable, no three-way splits. Confound: fresh jobs mix model
nondeterminism with input nondeterminism (Ollama is not bit-exact at T=0), so
"2-1" overstates instability.

## Third-party judge scores — **no usable signal**

`judge` → `judges.json`: 37 packets × 2 judges = 74 rows.

| judge | ok | errored | error rate | successful scores |
|---|---|---|---|---|
| gemma4:12b-mlx | 29 | 8 | 22% | 29 × 1.0 |
| granite4.2:3b | 18 | 19 | **51%** | 16 × 1.0, 2 × 0.0 |

- **Reliability fails.** 27/74 rows errored. Error mode is an **empty model
  response** (18 granite, 7 gemma: `no JSON object in judge response: ""`) plus
  2 truncated JSON payloads. Granite fails half the time.
- **Saturation.** Every successful score is 1.0 except two granite zeros
  (`local-first-essay` dialectical runs 1–2) — i.e. the judges are as saturated
  as the engine rubric and cannot rank artifacts.
- **Agreement is vacuous.** Only **10/37 packets (27%)** have both judges
  succeeding; on all 10, gemma = granite = 1.0. That is 100% agreement *on a
  constant* — no discriminative information, so inter-judge agreement cannot be
  established from this run.

Conclusion: **the third-party judge layer did not produce a trustworthy quality
signal.** We cannot use it to adjudicate single vs dialectical, and we must not
train on its verdicts.

## Human rating sheet

Unfilled. The runner's `packets/index.json` maps `packet_id` → goal/model/arm/
run; packets are blind. Rate each artifact against its stated criteria 1–5.

| packet_id | goal | arm | run | criteria met (Y/N) | score 1–5 | notes |
|---|---|---|---|---|---|---|
| | | | | | | |

## Findings & decision

1. **The loop did not earn its cost on any measurable axis.** Engine rubric tied
   on 7/10 and moved ±0.08 at most, for ~1.8× tokens; the judges added nothing.
2. **The limiting factor is measurement, not generation.** Diversity is real
   (0.795) — the engine *produces* meaningfully different candidates — but it
   cannot *rank* them. Best-of-N only pays when the verifier discriminates
   better than the generator; here the **verifier is the same model**
   (correlated error), so best-of-3 ≈ best-of-1.
3. **The code signal is absent.** `test_command: "true"` inflates every code
   goal to 1.00, so half the experiment measured nothing.
4. **Reliability cost.** 3/30 dialectical jobs failed vs 0/10 single.
5. **Judge validity: FAILED** (22%/51% error, saturation at 1.0, vacuous
   agreement). This is the headline the probe exists to produce.

**Decision.** Do not conclude either arm is better; the instrument cannot tell.
The run is a **pilot** (N=10 goals, single arm n=1) and the task set is too easy
(rubric saturates). The result justifies the F4 track, in this order:

- **F4-T3** (verifier interface) and **F4-T4** (judge hardening/quorum) — the
  verifier is the lever, and it must be a **different model family** from the
  generator.
- **F4-T2** (adaptive compute) and **F4-T1** (`Policy` seam) — don't spend N=3
  where a verifier can't winnow it.
- **F4-T5** (heterogeneous diversity) — diversity exists; make it *useful*.
- Fix the judge harness before any re-run: empty responses and truncation, and a
  scoring prompt with a usable range. **Open an ADR**: `min_judge_confidence` is
  mis-calibrated (confidence is a constant near 0.99) and the judge protocol
  needs a reliability gate.

## Follow-ups

- Re-run only after: (a) real code verification (persist the candidate, run the
  real test command), (b) an independent-family verifier, (c) harder tasks that
  the model sometimes fails, (d) a judge protocol that returns a usable range.
- Capture my artifact-level read (`artifacts.md`) as the human column — pending.
