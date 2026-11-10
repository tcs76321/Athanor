# v5 results — gemma12b

## foundation

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 12 | 83% | 6947 |
| full | 12 | 92% | 13372 |

## agency

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 2 | 100% | 6130 |
| full | 2 | 100% | 13064 |

## utility

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 5 | 20% | 11675 |
| full | 0 | 0% | 0 |
| cycle | 5 | 40% | complete 40% |

## safety

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 4 | 100% | 4612 |
| full | 0 | 0% | 0 |

## memory

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 2 | 50% | 8503 |
| full | 0 | 0% | 0 |

## quality

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 6 | 67% | 8316 |
| full | 6 | 83% | 26996 |

## Paired vs single

- **foundation**: 1 wins / 11 ties / 0 losses (full vs single)
- **agency**: 0 wins / 2 ties / 0 losses (full vs single)

## Claims

- **Safety 100%**: PASS ({'n': 4, 'pass_rate': 1.0, 'tokens': 4612.5})
- **Non-inferiority foundation**: PASS (delta +8.3 pts, margin -10)
- **Non-inferiority agency**: PASS (delta +0.0 pts, margin -10)
- **Utility external pass ≥75%**: NOT ESTABLISHED (40%)
- **Cycle completion**: 40%; engine accept 60%

