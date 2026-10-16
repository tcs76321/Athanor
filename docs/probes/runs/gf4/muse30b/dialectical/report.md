# M3-T7 results

Collected 6 job rows.

## Headline — dialectical vs single-shot (per model)

| model | family | goal | archetype | single | dialectical | delta | single tok | dialectical tok |
|---|---|---|---|---|---|---|---|---|
| muse30b | muse | book-collection | code | 0.00 | 1.00 | +1.00 | 0 | 6691 |
| muse30b | muse | fibonacci | code | 0.00 | 1.00 | +1.00 | 0 | 6570 |
| muse30b | muse | local-first-essay | text | 0.00 | 0.50 | +0.50 | 0 | 16200 |
| muse30b | muse | md2html-readme | document | 0.00 | 1.00 | +1.00 | 0 | 7333 |
| muse30b | muse | onboarding-email | text | 0.00 | 1.00 | +1.00 | 0 | 6639 |
| muse30b | muse | sunrise-alarm-brief | document | 0.00 | 1.00 | +1.00 | 0 | 7854 |

## T-b — judge-confidence calibration (dialectical arm)

| confidence bin | n | mean observed score |
|---|---|---|
| [0.9,1.0) | 5 | 1.00 |

## T-c — verdict stability (dialectical arm)

| class | goals |
|---|---|
| insufficient | 6 |

## T-a — candidate diversity (per model)

| model | dialectical | single |
|---|---|---|
| muse30b | 0.448 | 0.000 |

## F4 — verification decisions

Code accepts: 2; decided without the LLM judge: 2 (100%).
Comparisons that called the LLM judge: 3.

## Environment / caveats

- Fill in the model digests, Ollama version, and any confounds from the daemon logs.
- T-c across fresh jobs confounds model nondeterminism with input nondeterminism; note it.
