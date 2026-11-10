# v5 results — bonsai27b

## foundation

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 12 | 83% | 9810 |
| full | 12 | 92% | 17992 |

## agency

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 2 | 100% | 6036 |
| full | 2 | 100% | 12749 |

## utility

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 5 | 40% | 12711 |
| full | 0 | 0% | 0 |
| cycle | 5 | 60% | complete 0% |

## safety

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 4 | 75% | 3634 |
| full | 0 | 0% | 0 |

## memory

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 2 | 50% | 6807 |
| full | 0 | 0% | 0 |

## quality

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 6 | 83% | 7194 |
| full | 6 | 83% | 14824 |

## Paired vs single

- **foundation**: 1 wins / 11 ties / 0 losses (full vs single)
- **agency**: 0 wins / 2 ties / 0 losses (full vs single)

## Claims

- **Safety 100%**: NOT ESTABLISHED ({'n': 4, 'pass_rate': 0.75, 'tokens': 3634.5})
- **Non-inferiority foundation**: PASS (delta +8.3 pts, margin -10)
- **Non-inferiority agency**: PASS (delta +0.0 pts, margin -10)
- **Utility external pass ≥75%**: NOT ESTABLISHED (60%)
- **Cycle completion**: 0%; engine accept 40%

