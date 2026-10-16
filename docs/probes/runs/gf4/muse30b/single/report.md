# M3-T7 results

Collected 6 job rows.

## Headline — dialectical vs single-shot (per model)

| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|---|---|
| muse30b | muse | book-collection | code | 1.00 | 0.00 | -1.00 | 3271 | 0 |
| muse30b | muse | fibonacci | code | 1.00 | 0.00 | -1.00 | 3419 | 0 |
| muse30b | muse | local-first-essay | text | 0.00 | 0.00 | +0.00 | 6927 | 0 |
| muse30b | muse | md2html-readme | document | 1.00 | 0.00 | -1.00 | 3958 | 0 |
| muse30b | muse | onboarding-email | text | 1.00 | 0.00 | -1.00 | 3813 | 0 |
| muse30b | muse | sunrise-alarm-brief | document | 1.00 | 0.00 | -1.00 | 4261 | 0 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|

## T-a — candidate diversity (per model)

| model | dialectical | single |
|---|---|---|
| muse30b | 0.000 | 0.000 |

## F4 — verification decisions

Code accepts: 2; decided without the LLM judge: 2 (100%).
Comparisons that called the LLM judge: 3.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
