# Model qualification smoke (M9-T2b) — 2026-10-15

A ~1-hour pre-qualification pass over the locally installed models, to get early
signal for the routing table ([ADR-0049](adr/0049-model-onboarding-qualification.md))
before the full M9-T2b run. Hardware: M2 Max 32 GB, Apple Silicon. Two parts:
the **judge** qualification (the anchor) and a **generator** smoke (the code
suite). Both use existing tooling; this note records what they showed.

## Judge qualification (`make probe-anchor` / `anchor`)

Command:

```bash
go run -tags sqlite_fts5 ./spikes/m3-t7-probe anchor \
  -judges granite4.2:3b,granite4.2:8b,gemma4:12b-mlx,gemma4:e4b-mlx,ornith-1.5:9b \
  -protocol pointwise5 -think false -out spikes/m3-t7-probe/results/anchor-m9
```

| judge | calls ok | reliability | Spearman | Kendall |
|---|---|---|---|---|
| `granite4.2:3b` | 16/16 | 100% | **0.81** | 0.64 |
| `ornith-1.5:9b` | 16/16 | 100% | 0.77 | 0.59 |
| `gemma4:12b-mlx` | 16/16 | 100% | 0.69 | 0.47 |
| `granite4.2:8b` | 16/16 | 100% | 0.67 | 0.49 |
| `gemma4:e4b-mlx` | 16/16 | 100% | **0.48** | 0.35 |

The tool exits non-zero if any judge is below the 0.60 agreement floor — that is
the finding, not an error.

**Findings.**
- `granite4.2:3b` is the best local **judge**: highest agreement (0.81), smallest
  and fastest, and **cross-family** vs a gemma/qwen generator. Use it for the
  `security` persona. (It is what the 0.0.0 soak config already selects.)
- The larger `granite4.2:8b` is a *worse* judge than the 3b (0.67 vs 0.81) —
  capability does not track size here.
- `gemma4:e4b-mlx` (0.48) is below the floor: do not route `security` there.
- All judges were 100% reliable (no parse failures); agreement, not reliability,
  separates them.

## Generator smoke (`run`)

Command (6 code tasks, dialectical arm, the M9-T1 repair loop live):

```bash
go run -tags sqlite_fts5 ./spikes/m3-t7-probe run \
  -corpus eval/bench/tasks.yaml -models ornith9b,gemma12b \
  -only code-bank-atomic,code-expr-eval,code-lru-ttl,code-unified-diff,code-refactor-provided,code-parse-duration \
  -dialectical-runs 1 -judge-model granite4.2:3b -max-wall 45m \
  -out spikes/m3-t7-probe/results/gen-smoke-m9
```

| task | tier | state | winner | judge score | diversity |
|---|---|---|---|---|---|
| code-bank-atomic | H | failed | none | 1.00 | 0.68 |
| code-expr-eval | H | failed | none | 1.00 | 0.75 |
| code-lru-ttl | H | failed | none | 0.00 | 0.57 |
| code-unified-diff | H | failed | none | 1.00 | 0.78 |
| code-refactor-provided | M | failed | none | 1.00 | 0.00 |
| code-parse-duration | H | **completed** | **new** | 1.00 | 0.77 |

**1/6 code tasks accepted.** `winner=none` means the *deterministic verifier*
rejected the final artifact (the real tests failed) even where the LLM judge
scored 1.00 — the judge is saturated and non-discriminating (F4), so the
verifier is the only real signal.

**Findings.**
- With the 9B generator, the repair loop (M9-T1) and the synthesis fix (ADR-0068)
  are live but cannot rescue a model that fails every candidate on a hard task.
  This is the model ceiling ADR-0063 named, not an engine defect.
- **[Actionable — fixed in M9-T4b]** The planner flagged every H-tier code task
  `DIFFICULTY: easy` (`difficulty_hint: easy` in the event log), so **M9-T4's
  hard-task candidate raise never fired** (`divergence_start candidates=3` on
  every cycle). The hard-task lever was gated on the planner's self-assessment,
  which a weak planner gets wrong. **M9-T4b** now also raises on *evidence* (a
  task class with ≥ 5 outcomes and an accept rate below 0.5), and `Adaptive` will
  not narrow it.
- Diversity was real (0.57–0.78) except `code-refactor-provided` (0.00) — the
  candidates were identical there.

## Blocker for the soak

The run **aborted** at the residency guard:

```
m3-t7 run: model residency violated: up to 2 models resident at once;
set OLLAMA_MAX_LOADED_MODELS=1 (ARCHITECTURE §12.5)
```

`OLLAMA_MAX_LOADED_MODELS=1` and `OLLAMA_NUM_PARALLEL=1` are **operator
preconditions** the Core can only verify, not set. The generator→judge swap left
both `ornith-1.5:9b` and `granite4.2:3b` resident. **Set both env vars on the
Ollama server (restart) before the 0.0.0 soak**, or it aborts the same way.

## Caveats

- Small n (6 tasks, 1 model, 1 run per arm). Directional, not conclusive.
- The run aborted on residency before measuring `gemma12b` or the single arm, so
  the generator comparison is incomplete.
- The anchor is the F4-flagged instrument (provisional human ratings, saturated
  judge confidence); judge agreement is directionally useful, not precise.
