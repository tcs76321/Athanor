# Frontier-model quality evaluation — Athanor v3 smoke

**Rater:** DeepSeek V4.1 Flash in OpenCode (frontier cloud model) ·
**Date:** 2026-11-09 · **Tier:** machine / frontier — **not human**
**Protocol:** blind, eight artifacts (four quality tasks × single/full),
quality-rubric v1, one rater, one run, one generator (`bonsai-2:27b`).
**Raw evidence:** `frontier-blind.md`, `frontier-mapping.json`, and the
`quality-scores.json` in the same run tree.

> This is a **provisional machine anchor**, not a human evaluation. It can
> stand in for a human while the human anchor is unavailable, but it does not
> replace it (the same rule `eval/anchor/README.md` applies to agent ratings).

## Protocol

1. The two `artifacts.json` files from the single and full arms were merged,
   shuffled with a fixed seed, and relabeled `Q01`–`Q08` with no arm or model
   label.
2. Each artifact was scored against the eight `quality-v1` dimensions
   (1–5), with the correctness gate and the bloat penalty applied.
3. `frontier-mapping.json` was opened only after all eight ratings were
   recorded.
4. This is a **single frontier rater**. It is not inter-rater agreement and
   not a human sample.

## Ratings

| Case | Task | Correctness | Completeness | Depth | Originality | Structure | Robustness | Maintainability | Appropriate complexity | Overall |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Q01 | research brief | 5 | 5 | 4 | 4 | 5 | 5 | 4 | 5 | **4.63** |
| Q02 | rate-limit RFC | 5 | 4 | 4 | 3 | 5 | 4 | 4 | 4 | **4.13** |
| Q03 | refactor | 5 | 4 | 3 | 2 | 4 | 4 | 4 | 4 | **3.75** |
| Q04 | refactor | 5 | 4 | 3 | 2 | 4 | 4 | 4 | 5 | **3.88** |
| Q05 | cancel API | 5 | 5 | 4 | 3 | 5 | 4 | 4 | 5 | **4.38** |
| Q06 | cancel API | 5 | 5 | 5 | 4 | 5 | 4 | 4 | 4 | **4.50** |
| Q07 | rate-limit RFC | 5 | 5 | 5 | 4 | 5 | 5 | 5 | 5 | **4.88** |
| Q08 | research brief | 5 | 4 | 3 | 2 | 5 | 4 | 5 | 5 | **4.13** |

### Rationale, briefly

- **Q01** — The strongest research brief: the mail-carrier analogy explains
  the relay boundary without inventing a fact, and the third paragraph
  distinguishes disruption from exposure. Slightly more voice than a formal
  brief needs.
- **Q08** — Accurate and clean, but formulaic: quote, then “this means…”. It
  meets the brief and adds no synthesis. Correctness is not in question; depth
  and originality are thin.
- **Q02** — Complete and readable, but the state location is only “a local
  SQLite database”; the corruption mitigation (“periodic checkpoints and
  backups”) is generic rather than specific.
- **Q07** — The best artifact in the set. Names the table and file path, uses
  WAL deliberately, gives a concrete contention mitigation, and handles stale
  state with an ACID argument plus a clamp.
- **Q03** — Behavior-preserving early-return refactor, plus a tidy `__all__`
  in `__init__.py`. Clear, but the per-mode rules are still inline; it does
  not extract the rules into named helpers.
- **Q04** — The same early-return refactor with a smaller diff and no
  `__init__.py` rewrite. Slightly better on appropriate complexity, slightly
  worse on completeness as an artifact.
- **Q05** — Competent and implementable. Optional `Idempotency-Key`, terminal
  and unknown-job cases covered. The 422 reservation is dead weight.
- **Q06** — Stronger idempotency design: required key, 24-hour response cache,
  terminal conflict independent of the key. Slightly over-specified and makes
  a “never 5xx” claim that a real service cannot guarantee.

## Unblinded result

| Task | Single | Full | Winner | Δ (full − single) |
|---|---:|---:|---|---:|
| research brief | Q01 4.63 | Q08 4.13 | **single** | −0.50 |
| rate-limit RFC | Q02 4.13 | Q07 4.88 | **full** | +0.75 |
| refactor | Q03 3.75 | Q04 3.88 | full | +0.13 |
| cancel API | Q05 4.38 | Q06 4.50 | full | +0.13 |
| **Mean** | **4.22** | **4.34** | | **+0.13** |
| **Win rate** | | | | **3/4 (75%)** |

## What I think, as a frontier evaluator

**The “full loop is usually higher quality” claim is not supported by this
smoke.** The full arm wins three of four tasks, but the mean delta is only
+0.13, below the pre-registered +0.50 mean and CI-lower thresholds, and the
research brief is a real regression: the single arm’s brief is richer and more
useful than the full arm’s terse restatement. The RFC is the only material
win; the refactor and API differences are small enough to be noise. This is
consistent with the two judges disagreeing at 0.17–0.33: the signal is
**task-dependent and weak at n=4**, not a stable “full is better” effect.

### Task design is the limiting factor more than the loop

- **Research brief:** too easy and too short. Both arms can satisfy it by
  restating three sentences. It rewards concise paraphrase, not synthesis, so
  it cannot show a loop advantage—and the full arm’s extra “this means…”
  sentences made it slightly worse.
- **Refactor:** a three-line function is not enough headroom. Both arms
  produce the same early-return refactor. There is no multi-file design choice
  for the loop to improve.
- **Rate-limit RFC:** good headroom. The full arm wins by being concrete.
- **Cancel API:** decent headroom, but both arms are competent; the full
  arm’s idempotency design is better, not dramatically more creative.

To test “usually far more creative and higher-quality,” the suite needs tasks
with more design surface: multi-module refactors, architecture with real
trade-offs, research synthesis with conflicting sources, migration plans with
rollback, and specs where “more detail” has to be *correct* detail rather than
restatement.

### The rubric is serviceable but not yet a measuring instrument

- Absolute 1–5 scores **compress at the top**: most competent artifacts get
  5s, and the differences live in one or two dimensions. This makes the mean
  insensitive.
- Dimensions overlap (completeness vs depth vs structure; originality is
  ambiguous for technical docs). The **appropriate-complexity** dimension is
  doing most of the anti-bloat work.
- **Pairwise A/B with position swap** is a better protocol for this claim. It
  directly produces a win rate, avoids absolute-scale drift, and matches the
  question the product actually makes (“is this better than one shot?”).
  Absolute scores can stay as a diagnostic.
- A **frontier rater is useful as a provisional anchor**, especially for
  identifying task-level regressions like the research brief. It is not a
  substitute for humans: it shares a family of blind spots with the system it
  is judging, and it is one rater.

## Recommendation

1. Do not claim quality superiority from this smoke. Report it as **inconclusive
   / not supported**, with the RFC win as the positive case.
2. Move the quality claim to **pairwise blind A/B** (both orders, two judges,
   frontier/human adjudication) and report a win rate.
3. Increase headroom in the quality suite before the freeze: the current four
   tasks are a pipeline smoke, not a capability test.
4. Keep the absolute-rubric scores as diagnostics, not the headline.
5. Use the human anchor when available; this document is the provisional
   machine anchor in the meantime.

## Limitations

- One frontier rater, one run, four tasks, one generator.
- The rater is also the agent that helped build the benchmark; task familiarity
  is a bias risk. The blind packet is the mitigation, not a guarantee.
- No human ratings yet, so this cannot be reported as human agreement.
- The `quality-scores.json` judges (granite, mimo) are separate from this
  rating; they disagreed with each other and with this rater on the research
  brief and refactor.
