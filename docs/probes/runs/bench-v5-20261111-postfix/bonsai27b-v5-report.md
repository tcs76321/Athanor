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
| cycle | 5 | 60% | complete 0% |

Cycle mechanism: engine accept 40%, complete 0%. Completion is a separate gate, not part of the external label.

## safety

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 4 | 75% | 3634 |

## memory

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 2 | 50% | 6807 |

## quality

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 6 | 83% | 7194 |
| full | 6 | 83% | 14824 |

## Paired vs single

- **foundation**: 1 wins / 11 ties / 0 losses (full vs single)
- **agency**: 0 wins / 2 ties / 0 losses (full vs single)
- **quality**: 1 wins / 4 ties / 1 losses (full vs single)
- **utility**: 1 wins / 4 ties / 0 losses (cycle vs single)

## Claims

- **Safety 100%**: NOT ESTABLISHED ({'n': 4, 'pass_rate': 0.75, 'tokens': 3634.5})
- **Non-inferiority foundation**: PASS (delta +8.3 pts, margin -10)
- **Non-inferiority agency**: PASS (delta +0.0 pts, margin -10)
- **Utility external pass ≥90%**: NOT ESTABLISHED (60%)
- **Cycle completion 100%**: NOT ESTABLISHED (0%)

## Quality — blind pairwise (full vs single)

- Judges: granite4.2:8b, mimo-v2.6:9b-mxfp8
- Pairs: 5; full wins 3, single wins 1, ties 1
- Full win rate: 0.75 (floor 0.60)
- Inter-judge agreement: 0.00 (floor 0.60)
- Forward/reverse self-consistency: 0.30
- **Quality claim**: NOT ESTABLISHED

## External — Aider Polyglot (Python subset, custom adapter)

Track v1 · `479f11b44b02` · athanor-custom · arm `single`.

**4/8** official Exercism exercises passed.

Engine accepted 4/8. The external test is the label; convergence is reported separately.

| exercise | pass | engine | reason |
|---|---|---|---|
| two-fer | False | False | FAILED two_fer_test.py::TwoFerTest::test_another_name_given - AssertionError:...; FAILED two_fer_tes |
| leap | True | True | .........                                                                [100%]; 9 passed in 0.02s |
| pangram | True | True | ............                                                             [100%]; 12 passed in 0.02s |
| isogram | True | True | ..............                                                           [100%]; 14 passed in 0.02s |
| raindrops | False | False | ERROR raindrops_test.py; !!!!!!!!!!!!!!!!!!!! Interrupted: 1 error during collection !!!!!!!!!!!!!!! |
| rna-transcription | True | True | ......                                                                   [100%]; 6 passed in 0.02s |
| acronym | False | False | =========================== short test summary info ============================; FAILED acronym_tes |
| hamming | False | False | FAILED hamming_test.py::HammingTest::test_single_letter_different_strands - T...; FAILED hamming_tes |

> Custom adapter over the official Exercism tests; not an official Aider leaderboard entry. The label is external to Athanor's verifier.

