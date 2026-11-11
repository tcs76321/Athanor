# v5 results — bonsai27b

Generated 2026-11-11T21:07:57Z · protocol `v5` (candidate, cut `pending`)

## foundation

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| full | 12 | 92% (95% CI 65%–99%) | 17992 |
| single | 12 | 83% (95% CI 55%–95%) | 9810 |

## agency

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| full | 2 | 100% (95% CI 34%–100%) · directional · saturated | 12792 |
| single | 2 | 100% (95% CI 34%–100%) · directional | 6730 |

## utility

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 5 | 100% (95% CI 57%–100%) · directional · saturated | 7350 |
| cycle | 5 | 80% (95% CI 38%–96%) · complete 100% | — |

## safety

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 4 | 100% (95% CI 51%–100%) · directional · saturated | 3849 |

## memory

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 2 | 100% (95% CI 34%–100%) · directional · saturated | 8792 |

## quality

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| full | 6 | 83% (95% CI 44%–97%) · directional | 14824 |
| single | 6 | 83% (95% CI 44%–97%) · directional | 7194 |

## Paired vs single

- **foundation**: 1 wins / 11 ties / 0 losses (full vs single) · delta CI +0.0%..+25.0%
- **agency**: 0 wins / 2 ties / 0 losses (full vs single) · delta CI +0.0%..+0.0%
- **quality**: 1 wins / 4 ties / 1 losses (full vs single) · delta CI -50.0%..+50.0%
- **utility**: 0 wins / 4 ties / 1 losses (cycle vs single)

## Claims

- **Safety**: 4/4 (100% (95% CI 51%–100%)), 0 failures — PASS (directional; containment is Gate G2)
- **Non-inferiority foundation**: PASS (delta +8.3 pts, margin -10; 95% CI +0.0..+25.0 pts)
- **Utility briefs**: 2/2 (min 2) — PASS
- **Utility news_tuning**: 2/2 (min 2) — PASS
- **Utility news_heldout**: 0/1 (min 1) — FAIL
- **Utility completion**: 5/5 (PASS)
- **Utility aggregate**: 80% (95% CI 38%–96%) (directional; not a 90% claim)
- **Mechanism coverage**: 0% (task 0/4, arm violations 0) — FAIL

## Cost (full vs single on saturated tasks)

| suite | n | token median | wall median | tok over | wall over |
|---|---|---|---|---|---|
| agency | 1 | 2.40× | 1.72× | 1 | 0 |
| foundation | 10 | 1.65× | 1.14× | 5 | 2 |
| quality | 4 | 2.06× | 1.76× | 4 | 1 |

- **Cost envelope**: FAIL (token median ≤1.5×, wall median ≤2.0×)

## Reliability (agency pass@2)

| arm | tasks | runs | pass@1 | pass@2 | role |
|---|---|---|---|---|---|
| full | 2 | 2 | 100% | 100% | report |
| single | 2 | 2 | 50% | 100% | report |

## Quality — blind pairwise (full vs single)

**Not measured.** protocol marks quality not measured

## External — Aider Polyglot (Python subset, custom adapter)

**4/8** official Exercism exercises passed; engine accepted 4/8 (converged).

> Custom adapter over the official Exercism tests; not an official Aider leaderboard entry.

## Provenance

- Corpus digest: `7eef524eb92ca621…`
- Protocol digest: `224210fbfdf3601a…`
- Commits: 2a9751a2b3539d18bb402271427aaefb44d8b806, 36be1b94c2d7a4810901d15c590d041931bd7604, 4737cf57a574c65816ec2b892c2a0d2547380fb9, a6a1fbc5acbc52bd6aa0a154b8d4cf53f2ffbdab · **mixed revisions**

