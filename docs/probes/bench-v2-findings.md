# Benchmark v2 findings — first full run (2026-10-25)

**Status:** measured · **Corpus:** [frozen v2](../frozen-corpus-v2.md) (41 H/X deterministic + 4 memory + 6 human) · **Run:** Core Pod, one canonical persona set, full arm, one pass
**Raw evidence:** [`docs/probes/runs/bench-v2-20261025/`](runs/bench-v2-20261025/) (report, results CSV/JSON, per-job metrics)

This is the first full v2 slice. The non-code headline uses the probe's
**external** deterministic checks; the code rows in this baseline use engine
acceptance (see the correction below).

> **Measurement correction (2026-10-29).** The 18 code rows in this baseline are
> labeled by **engine acceptance** (`completed` + `winner=new`), not by an
> independent test runner. The results record no per-candidate test outcome, so
> the X-code 0/6 is six engine failures — it is not proof that the six
> candidates failed their frozen tests. A focused 2026-10-29 X-code diagnostic
> after the P0 working-set and harness fixes measured **4/6 on the host dev
> runtime and 3/6 on the Core Pod**
> ([bench-xcode-p0](bench-xcode-p0.md)). [ADR-0092](../adr/0092-benchmark-label-integrity.md)
> changes the code label to the recorded `tests_ran` outcome and reports engine
> accept separately; the next full run replaces this baseline as the capability
> number. Do not quote 30/41 or 0/6 X-code as the current tree's result.

## Run shape

| Field | Value |
|---|---|
| Generator / judge | `ornith-1.5:9b` / `granite4.2:3b` (cross-family) |
| Arm | `full` (N=3, reflection, self-refine) |
| Corpus | 41 H/X tasks, one pass |
| Runtime | Core Pod image, named state volume |
| Wall clock | **1 h 39 m 30 s** (02:06–03:45 UTC), well under the 12 h cap |
| Jobs | 41 |
| Tokens | 576,806 prompt + 186,373 completion (763,179 total) |
| Cost proxy | 18,614 tokens/job; 25,439 tokens/pass |
| Latency | p50 67.8 s; p95 320.4 s per job |

## Headline

| Slice | n | Pass | Rate |
|---|---:|---:|---:|
| **All deterministic** | **41** | **30** | **73%** |
| H tier | 26 | 24 | **92%** |
| X tier | 15 | 6 | **40%** |
| Held-out | 10 | 6 | 60% |
| Tuning | 31 | 24 | 77% |
| Code | 18 | 12 | 67% |
| Document | 13 | 13 | **100%** |
| Text | 5 | 5 | **100%** |
| Data | 5 | 0 | **0%** |

The H floor is strong. **X is the wall:** all 6 X code tasks failed; the six X
passes are document/text/adversarial (`x-doc-spec-from-code`,
`x-doc-long-synthesis`, `x-text-fact-preserving`,
`x-text-multi-constraint`, `x-adv-injection-long`,
`x-adv-contradictory-sources`).

## Code acceptance is deterministic

- **12 code accepts; 12 decided without the LLM judge** (100%).
- All 12 are H-tier. The 6 X code tasks were **not accepted by the engine**
  (`x-code-repo-ratelimit`, `x-code-bugfix-pipeline`, `x-code-interpreter`,
  `x-code-diff3`, `x-code-threadpool`, `x-code-sqlite-migration`). This baseline
  does not record a per-candidate test outcome, so it cannot separate a test
  failure from a later engine failure.
- The judge was not consulted for a single code accept, which is the
  verification-first posture working as designed. This is the engine's
  deterministic verifier, not an independent probe check; the new code label
  (ADR-0092) makes that distinction explicit.

## Mechanism telemetry

| Metric | Value |
|---|---:|
| LLM calls | 538 |
| Calls by phase | diverging 205 · evaluating 216 · planning 41 · refining 18 · reflecting 25 · synthesizing 33 |
| Repairs (self-refine) | 18 |
| Reflection loops | 25 |
| Diversity re-rolls | 0 |
| Candidate raises | 41 |
| KV-pressure observations | 538 |
| Context-floor violations | 0 |
| Mean candidate diversity | 0.679 |

## Judge and confounds

- **Confidence is saturated:** 33/33 observations in `[0.9, 1.0)` (mean 1.00).
  The v1 finding is unchanged; the confidence threshold is not
  discriminative.
- **T-c is insufficient:** one pass per task cannot separate model
  nondeterminism from input nondeterminism.
- The `x-code-interpreter`, `x-code-diff3`, and `x-code-sqlite-migration`
  rows show an evaluation score of 1.0 but were not accepted. The score is the
  LLM rubric score, not the frozen test result (`evaluate.go` overrides the
  pass flag for a hard verifier but not the score). For code rows this baseline
  has no independent test label; ADR-0092 adds it.
- `text-level-rewrite`, `doc-migration-guide`, and `x-text-multi-constraint`
  passed their external checks but the engine ended `failed`; those are
  engine-side failures worth a separate look, not capability failures.

## Comparison with v1 (pilot)

| | v1 (70 tasks) | v2 (41 tasks) |
|---|---:|---:|
| Overall | 56/70 (80%) | 30/41 (73%) |
| Code | 30/30 (100%) | 12/18 (67%) |
| H | 20/26 (77%) | 24/26 (92%) |
| X | — | 6/15 (40%) |
| Data | 0/6 (0%) | 0/5 (0%) |

Not a regression claim: v2 removed the easy tier, replaced the code floor with
repo-scale X tasks, and changed **non-code** scoring to external checks. Code
rows in this baseline still use engine accept. The useful comparison is
qualitative: **H is solid, X code was not accepted in this baseline, and data is
still failing** — with the X-code and data numbers both predating the P0
fixes.

## What to do next

1. **X code is the capability target.** This baseline did not accept the six
   X-code tasks; the 2026-10-29 focused run then measured 4/6 host / 3/6 Core.
   The next full run labels each code row by its recorded frozen test
   (ADR-0092) so a test pass and an engine rejection are visibly different.
2. **Data is 0/5.** This repeated the v1 result; audit whether the failure is
   the JSON-only output contract, schema extraction, or the verifier. The P0
   fixes pinned the schema in the prompt and resolved the v2 schema-path leak;
   this baseline predates both.
3. **Investigate the engine-failed-but-check-passed rows.** A job should not
   end `failed` when its candidate passes the external check; those are engine
   or harness bugs, not capability.
4. **Do not tune on the held-out slice.** Held-out is 6/10; n is small.
5. **Keep the judge advisory.** Confidence saturation confirms the v1
   conclusion; the verifier remains the acceptance edge.

## Caveats

- One model, one pass, n=41. Directional, not statistically conclusive.
- The held-out slice is only 10 tasks and is X-heavy; a 60% held-out vs 77%
  tuning gap is not an overfitting signal at this n.
- Raw results are retained; a future grader can re-score them.
- The run was repeated after fixing a duplicate-job harness bug (project create
  auto-start + explicit goal submit); the contaminated run is archived at
  `tools/bench/results/bench-v2/out-contaminated-auto-start` and is not evidence.
