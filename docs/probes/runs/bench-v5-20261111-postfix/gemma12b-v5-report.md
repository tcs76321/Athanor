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
| cycle | 5 | 40% | complete 40% |

Cycle mechanism: engine accept 60%, complete 40%. Completion is a separate gate, not part of the external label.

## safety

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 4 | 100% | 4612 |

## memory

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 2 | 50% | 8503 |

## quality

| arm | n | external pass | tokens/pass |
|---|---|---|---|
| single | 6 | 67% | 8316 |
| full | 6 | 83% | 26996 |

## Paired vs single

- **foundation**: 1 wins / 11 ties / 0 losses (full vs single)
- **agency**: 0 wins / 2 ties / 0 losses (full vs single)
- **quality**: 1 wins / 5 ties / 0 losses (full vs single)
- **utility**: 1 wins / 4 ties / 0 losses (cycle vs single)

## Claims

- **Safety 100%**: PASS ({'n': 4, 'pass_rate': 1.0, 'tokens': 4612.5})
- **Non-inferiority foundation**: PASS (delta +8.3 pts, margin -10)
- **Non-inferiority agency**: PASS (delta +0.0 pts, margin -10)
- **Utility external pass ≥90%**: NOT ESTABLISHED (40%)
- **Cycle completion 100%**: NOT ESTABLISHED (40%)

## External — Aider Polyglot (Python subset, custom adapter)

Track v1 · `479f11b44b02` · athanor-custom · arm `single`.

**1/8** official Exercism exercises passed.

Engine accepted 1/8. The external test is the label; convergence is reported separately.

| exercise | pass | engine | reason |
|---|---|---|---|
| two-fer | False | False | FAILED two_fer_test.py::TwoFerTest::test_another_name_given - AssertionError:...; FAILED two_fer_tes |
| leap | False | False | ERROR leap_test.py; !!!!!!!!!!!!!!!!!!!! Interrupted: 1 error during collection !!!!!!!!!!!!!!!!!!!! |
| pangram | True | True | ............                                                             [100%]; 12 passed in 0.02s |
| isogram | False | False | ERROR isogram_test.py; !!!!!!!!!!!!!!!!!!!! Interrupted: 1 error during collection !!!!!!!!!!!!!!!!! |
| raindrops | False | False | ERROR raindrops_test.py; !!!!!!!!!!!!!!!!!!!! Interrupted: 1 error during collection !!!!!!!!!!!!!!! |
| rna-transcription | False | False | ERROR rna_transcription_test.py; !!!!!!!!!!!!!!!!!!!! Interrupted: 1 error during collection !!!!!!! |
| acronym | False | False | FAILED acronym_test.py::AcronymTest::test_lowercase_words - AssertionError: '...; FAILED acronym_tes |
| hamming | False | False | ERROR hamming_test.py; !!!!!!!!!!!!!!!!!!!! Interrupted: 1 error during collection !!!!!!!!!!!!!!!!! |

> Custom adapter over the official Exercism tests; not an official Aider leaderboard entry. The label is external to Athanor's verifier.

