# Benchmark v2 — post-P0 full run (2026-11-02)

**Status:** measured · **Corpus:** [frozen v2](../frozen-corpus-v2.md) (41 H/X deterministic) · **Run:** Core Pod, one canonical persona set, full arm, one pass
**Raw evidence:** [`runs/bench-v2-p0-20261102/`](runs/bench-v2-p0-20261102/) (report, results, config, metrics)

This is the first full v2 slice after the P0 capability, containment, and
benchmark-label changes. The headline uses the benchmark's own labels: the
recorded frozen test for code rows (ADR-0092) and the deterministic checks for
non-code rows. Engine acceptance is reported separately.

## Run shape

| Field | Value |
|---|---|
| Generator / judge | `ornith-1.5:9b` / `granite4.2:3b` (cross-family) |
| Arm | `full` (N=3, reflection, self-refine) |
| Corpus | 41 H/X tasks, one pass |
| Runtime | Core Pod image, named state volume |
| Wall clock | **50 m 43 s** (3,043 s of job time), well under the 12 h cap |
| Jobs | 41 |
| Tokens | 488,058 prompt + 135,619 completion (**623,677 total**) |
| Cost proxy | 15,212 tokens/job; 19,490 tokens/pass |
| Latency | p50 51.0 s; p95 162.4 s per job |

## Headline

| Slice | n | Pass | Rate | Pre-P0 baseline |
|---|---:|---:|---:|---:|
| **All deterministic** | **41** | **32** | **78%** | 30/41 (73%) |
| H tier | 26 | 23 | 88% | 24/26 (92%) |
| X tier | 15 | 9 | **60%** | 6/15 (40%) |
| Held-out | 10 | 6 | 60% | 6/10 (60%) |
| Tuning | 31 | 26 | 84% | 24/31 (77%) |
| Code | 18 | 16 | **89%** | 12/18 (67%) |
| Document | 13 | 8 | **62%** | 13/13 (100%) |
| Text | 5 | 5 | 100% | 5/5 (100%) |
| Data | 5 | 3 | **60%** | 0/5 (0%) |

The aggregate moved +2, but the shape changed materially: **code, X, and data
improved; document regressed.** Read the aggregate as a trade, not a uniform
win.

## Code test label vs engine accept

| Code rows | Test pass | Engine accept | Test pass, not accepted | Test missing | Legacy |
|---:|---:|---:|---:|---:|---:|
| 18 | **16** | **16** | **0** | 0 | 0 |

Every code pass is both a recorded frozen-test pass and an engine accept; there
is no hidden candidate that passed the test but was rejected by the engine. All
16 accepts were decided without the LLM judge. This is the label fix working,
not a claim that the test is independently re-run (see the caveats).

## X-code

**4/6**, up from 0/6 in the pre-P0 baseline and consistent with the focused
2026-10-29 diagnostic (4/6 host, 3/6 Core).

| Task | Result | Note |
|---|---|---|
| `x-code-repo-ratelimit` | ✅ | multi-file package, working set present |
| `x-code-bugfix-pipeline` | ✅ | three-bug fix in a provided package |
| `x-code-interpreter` | ❌ | recorded test failed |
| `x-code-diff3` | ✅ | conflict markers and indices |
| `x-code-threadpool` | ✅ | previous hang is gone |
| `x-code-sqlite-migration` | ❌ | recorded test failed |

The two failures are candidate logic, not missing repository context. The
working-set pack was active in every code job.

## Data

**3/5**, up from 0/5.

| Task | Result | Failing check |
|---|---|---|
| `data-json-normalize` | ✅ | — |
| `data-h-schema-validate` | ✅ | — |
| `x-data-reconcile` | ✅ | — |
| `x-data-schema-evolve` | ❌ | `array_length` |
| `x-data-aggregate` | ❌ | `array_length` |

The schema-in-prompt and version-local schema-path fixes moved the H data tasks
from 0/2 to 2/2. The two X failures are wrong array cardinality, not schema
validity: the engine's schema verifier passes, while the benchmark's stricter
`array_length` check rejects. That is a capability/contract gap, not a verifier
bug.

## Document regression

Five document tasks failed the `max_words` check:

- `doc-threat-model`
- `doc-migration-guide`
- `adv-h-unsafe-request`
- `x-adv-injection-long`
- `x-adv-contradictory-sources`

The pre-P0 baseline passed all of document (13/13). This run's output was
longer than the check allows. One pass cannot separate model variability from a
prompt-length regression, but five failures in one slice is the strongest
signal in the run and deserves a focused investigation before the next tag.

## Mechanism telemetry

| Metric | Value |
|---|---:|
| LLM calls | 419 |
| Calls by phase | diverging 157 · evaluating 165 · planning 41 · refining 8 · reflecting 9 · synthesizing 39 |
| Repairs (self-refine) | 8 |
| Reflection loops | 9 |
| Candidate raises | 41 |
| Working-set chunks / bytes | 45 / 11,850 |
| Dormant Index rows | 308 |
| Memory hits / compactions | 0 / 0 |
| KV warnings / critical | 0 / 0 |
| Context-floor violations | 0 |
| Mean candidate diversity | 0.616 |

The run was faster than the baseline (p50 51 s vs 67.8 s; p95 162 s vs 320 s)
and used fewer tokens per job (15.2k vs 18.6k). The host-side `make check` and
security work ran in parallel with part of the slice, so treat the latency
comparison as directional.

## Judge

- **Confidence is still saturated:** 39/39 observations in `[0.9, 1.0)`, mean
  1.00. The confidence threshold is not discriminative.
- **No comparison called the LLM judge.** All 16 code accepts were decided by
  the deterministic verifier.
- The headline is not the judge; the judge remains advisory.

## Caveats

- One pass, 41 tasks, one canonical model set; **not a statistical claim**.
- The code label is the engine's recorded frozen-test outcome, not yet an
  independent re-run in a probe-owned pod (ADR-0092's next step).
- Host-side test/lint activity overlapped part of the run; pass rates are
  unaffected, latency may be.
- Held-out is 10 tasks; 6/10 has wide uncertainty.
- Conformance scenarios report 0/0/0 under external mode; they are exercised by
  the focused harness, not this run.

## What to do next

1. **Investigate the five `max_words` document failures.** The document
   regression is the largest negative delta. Check whether synthesis now
   exceeds the declared limit (a prompt/contract issue) or whether it is model
   variance.
2. **X-code interpreter and sqlite-migration** are the two remaining code
   failures; both are candidate logic, not context or harness.
3. **X-data cardinality:** the engine verifies schema but not the benchmark's
   array-length/ordering constraints. Decide whether to promote those checks
   into `execution.checks` for `data`, or keep them benchmark-only.
4. **Run the full slice again** after the document fix to confirm the trade.
