# v5 results — gemma12b

Generated 2026-11-11T18:28:04Z · protocol `v5` (candidate, cut `pending`)

## foundation

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| full | 12 | 92% (95% CI 65%–99%) | 13372 |
| single | 12 | 83% (95% CI 55%–95%) | 6947 |

## agency

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| full | 2 | 100% (95% CI 34%–100%) · directional | 21474 |
| single | 2 | 100% (95% CI 34%–100%) · directional · saturated | 6258 |

## utility

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 5 | 40% (95% CI 12%–77%) · directional | 9239 |
| cycle | 5 | 60% (95% CI 23%–88%) · complete 80% | — |

## safety

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 4 | 100% (95% CI 51%–100%) · directional · saturated | 4612 |

## memory

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 2 | 50% (95% CI 9%–91%) · directional | 8503 |

## quality

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| full | 6 | 83% (95% CI 44%–97%) · directional | 26996 |
| single | 6 | 67% (95% CI 30%–90%) · directional | 8316 |

## Paired vs single

- **foundation**: 1 wins / 11 ties / 0 losses (full vs single) · delta CI +0.0%..+25.0%
- **agency**: 0 wins / 2 ties / 0 losses (full vs single) · delta CI +0.0%..+0.0%
- **quality**: 1 wins / 5 ties / 0 losses (full vs single) · delta CI +0.0%..+50.0%
- **utility**: 2 wins / 2 ties / 1 losses (cycle vs single)

## Claims

- **Safety**: 4/4 (100% (95% CI 51%–100%)), 0 failures — PASS (directional; containment is Gate G2)
- **Non-inferiority foundation**: PASS (delta +8.3 pts, margin -10; 95% CI +0.0..+25.0 pts)
- **Utility briefs**: 2/2 (min 2) — PASS
- **Utility news_tuning**: 1/2 (min 2) — FAIL
- **Utility news_heldout**: 0/1 (min 1) — FAIL
- **Utility completion**: 4/5 (FAIL)
- **Utility aggregate**: 60% (95% CI 23%–88%) (directional; not a 90% claim)
- **Mechanism coverage**: 0% (task 0/4, arm violations 0) — FAIL

## Cost (full vs single on saturated tasks)

| suite | n | token median | wall median | tok over | wall over |
|---|---|---|---|---|---|
| agency | 2 | 4.45× | 3.74× | 2 | 1 |
| foundation | 10 | 2.10× | 1.34× | 9 | 0 |
| quality | 4 | 2.16× | 1.95× | 4 | 2 |

- **Cost envelope**: FAIL (token median ≤1.5×, wall median ≤2.0×)

## Reliability (agency pass@2)

| arm | tasks | runs | pass@1 | pass@2 | role |
|---|---|---|---|---|---|
| full | 2 | 2 | 100% | 100% | report |
| single | 2 | 2 | 100% | 100% | report |

## Quality — blind pairwise (full vs single)

**Not measured.** protocol marks quality not measured

## External — Aider Polyglot (Python subset, custom adapter)

**1/8** official Exercism exercises passed; engine accepted 1/8 (converged).

> Custom adapter over the official Exercism tests; not an official Aider leaderboard entry.

## Provenance

- Corpus digest: `7eef524eb92ca621…`
- Protocol digest: `224210fbfdf3601a…`
- Commits: 36be1b94c2d7a4810901d15c590d041931bd7604, eab7d228da4b3838e20c6e00cb257f792e5cdc86 · **mixed revisions**

