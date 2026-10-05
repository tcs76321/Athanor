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

**T0b validation (re-run with the fixed protocol).** After the protocol fix —
retry on empty responses, reject a missing `score`, drop the JSON-format hint on
retry, and a per-judge reliability gate — the same 37 packets judged cleanly:
**gemma 32/37 (86%)** and **granite 33/37 (89%)** of calls succeeded (up from
78% / 49%). Reliability is fixed. But **saturation persists**: every successful
gemma score is 1.0, and granite is 1.0 on 27/33 (0.0 on 6). Absolute-score
judging still cannot rank. Discrimination needs *harder* tasks (the ceiling
effect) and/or a pairwise protocol — a reliable judge is not yet a
discriminating one. That is F4-T4's problem.

## Human rating sheet

Agent read of `artifacts.md` for the text/document goals (the ones where the
rubric has range to move). This is **not** human ground truth — it is one reader
with reasons, to check whether the engine's ordering survives scrutiny. Code
goals are omitted: the no-op test command leaves them uninformative.

| goal | arm | run | criteria met | score 1–5 | notes |
|---|---|---|---|---|---|
| local-first-essay | dialectical | 1 | **N** | 2 | one blob paragraph, four reasons crammed, no separate conclusion — **engine scored it 0.97, the highest of the three** |
| local-first-essay | dialectical | 2 | Y | 4 | three clean reasons + conclusion |
| local-first-essay | dialectical | 3 | Y | 4 | three labeled reasons; crisp |
| local-first-essay | single | 1 | Y | 4 | three reasons + title + conclusion; comparable |
| md2html-readme | dialectical | 1 | Y | 3 | sections in order; title is the raw project id |
| md2html-readme | dialectical | 2 | Y | 2 | sections out of order (License first); absurd `pip install <project-id>`; trailing LIMITATION |
| md2html-readme | dialectical | 3 | Y | 4 | clean `# md2html`, correct order, sane install |
| md2html-readme | single | 1 | Y | 3 | raw project id as title; absurd install name |
| onboarding-email | dialectical | 1 | — | — | **job failed, no artifact** |
| onboarding-email | dialectical | 2 | Y | 4 | under 120 words, one clear CTA |
| onboarding-email | dialectical | 3 | Y | 4 | clear CTA |
| onboarding-email | single | 1 | Y | 4 | clear CTA; comparable |
| sunrise-alarm-brief | dialectical | 1 | Y | 4 | concrete module brief, two real risks |
| sunrise-alarm-brief | dialectical | 2 | Y | 3 | metaphorical (sun / sleeper / alarm); inventive but off-brief |
| sunrise-alarm-brief | dialectical | 3 | Y | 2 | self-referential ("brief document" as a part); meta LIMITATION |
| sunrise-alarm-brief | single | 1 | Y | **5** | most professional (Alarm/Scheduler/Interface), concrete risks — **engine scored it lowest (0.92)** |

**The engine's ordering does not survive the read.** Two clear inversions:
`local-first-essay` run 1 violates the explicit "exactly three paragraphs"
criterion yet scored highest (0.97), and `sunrise-alarm-brief` single is the best
artifact yet scored *lowest* (0.92 vs 1.00). The candidates differ in obvious
quality; the rubric cannot see it.

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
6. **The engine rubric is not merely saturated — it is wrong.** In the human
   read, two of the largest quality gaps were scored against the grain
   (`local-first-essay` r1 — the one artifact that violates the explicit
   three-paragraph criterion — scored highest at 0.97; `sunrise-alarm-brief`
   single — the best artifact — scored lowest at 0.92). So the load-bearing
   signal failed independently of the third-party judges.

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
- The agent read of `artifacts.md` is in the human rating sheet above; a true
  human pass over `packets/index.json` is still wanted as the anchor.
