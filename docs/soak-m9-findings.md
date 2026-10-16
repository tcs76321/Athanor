# M9 soak findings — 2026-10-16

A data-gathering soak (not the Gate G7 sign-off, which runs after the freeze).

**Setup:** generator `gemma4:12b-mlx`, `full` arm (N=3, reflection, self-refine on
via M9-T1b), cross-family judge `granite4.2:3b`, corpus `eval/bench/tasks.yaml`
(15 tasks), `-kill-after 1h`, 2 h budget. **Stopped early at ~1 h 12 m** on
operator request. Hardware: M2 Max 32 GB; `OLLAMA_MAX_LOADED_MODELS=1`.

## Quality — pass 1 (complete, 15/15)

**All six code tasks accepted** (bank-atomic, expr-eval, lru-ttl, unified-diff,
refactor-provided, parse-duration). Per-tier deterministic pass rate: **H 6/10,
M 3/5 (60%)**.

Failing rows are all non-code deterministic checks: `doc-api-readme`,
`doc-cited-brief`, `doc-changelog-categorize`, `text-level-rewrite`,
`data-json-normalize`.

## Quality — pass 2 (partial)

`code-bank-atomic` and `code-expr-eval` **failed** (deterministic verifier
reject — the model's code failed the hidden tests), then the manual interruption
stopped the pass at `code-lru-ttl`.

**Variance finding (important):** the same model passed **6/6 code in pass 1**
and **0/2 in pass 2**. A capable-but-general 12 B is not *reliable* on the H-tier
code tasks; a single pass's pass@k is noisy. The routing recommendation
(`gemma4:12b-mlx` over `ornith-1.5:9b`) still holds on aggregate (≈6/8 vs 2/6),
but the absolute rate must be read with that variance in mind.

## Recovery — clean

```
2026-10-16T05:10:28Z injecting kill -9 after 1h0m50s
2026-10-16T05:10:36Z daemon restarted; state recovered
```

The `kill -9` fired at the 1-hour mark; the daemon restarted and recovered state
in **~8 s**, and the run continued into pass 2. Crash recovery is clean.

## M9-T4b fired

`divergence_start` candidate counts rose from **3 to 10** on several post-restart
cycles — the evidence-based hard-task raise (a task class the model keeps failing)
engaged for the archetypes whose accept rate was low. The lever works.

## Guidance channel — inert (as expected)

`strategy_insights` is empty and `strategy_notes_injected` = 0: the technique loop
found nothing to activate on this small run (the soak config's cohort floor is 8,
and no winning technique cleared the grounding bar). So the pass-2 results are
**not** confounded by injected guidance.

## Endurance (short run)

Peak RSS **33 MB**, SQLite **5.5 MB**, goroutines **≤ 61**, disk stable across
~1 h+ — bounded. (This is a short run; the Gate G7 soak is longer and runs after
the freeze.)

## Interruption note (not a defect)

Stopping the run by signalling the daemon mid-job raced the F5 stop-pods
shutdown, leaving **1 orphan Job Pod**, removed by hand. The M2-T5 boot sweep also
catches orphans on the next daemon start; normal operation tears pods down.

## Follow-ups (from the M9 runs)

- **Qualification must repeat runs — mechanism landed (2026-10-16).** The 6/6 →
  0/2 swing means a single-pass pass@k is not a routing signal on its own. The
  probe now has a `measure` subcommand that turns a results tree + anchor run
  into `onboard.Measurement` JSON with `Samples` set (generator accept rate over
  the collected jobs; judge Spearman over rated cases), so `onboard.Route`'s
  `MinSamples` guard can act and `athanor models -qualification` consumes it.
  The operator still must *run* repeats (`-dialectical-runs` > 1); the probe now
  records how many samples back each score.
- **Engine verifier vs benchmark checks — FIXED (2026-10-16).** The bench
  `checks:` are stricter than the task `criteria:` the engine sees, and the
  engine's structural verifier is a heuristic over natural-language criteria —
  it cannot verify a check the criteria do not state. The engine now gains
  *declared* document checks (generalize the `execution.data_schema` pattern):
  `execution.checks` ([ADR-0070](adr/0070-declared-checks.md)) feeds a hard
  `checksVerifier`, and the probe sends each bench task's `checks:` (and, for
  `data`, its schema) to the project so the engine gates at runtime.
- **Ship a surface for the routing table — FIXED (2026-10-16).** `internal/onboard`
  was a pure library with no production caller. `athanor models` now presents the
  per-role inventory (configured vs installed, fitting alternatives) and, with a
  `-qualification <file>`, the advisory `onboard.Route` proposal — the surface
  M9-T2's acceptance wanted (applying it is a config edit; the human is the gate,
  ADR-0049). `onboard.Assess` composes the two.
- **Interrupt-time pod teardown — FIXED (2026-10-16).** Signalling the daemon
  mid-job could leave one orphan Job Pod (the §F5 ~10 s shutdown raced a
  mid-exec pod); the M2-T5 boot sweep caught it. The engine now sets a drain
  flag in `StopPods` (a phase still in flight cannot start a new pod once
  shutdown begins) and `jobpod.Manager.Shutdown` seals the manager
  (`Start` → `ErrSealed`) and force-removes every `athanor-job-*` container
  regardless of the in-memory map; `serve.go` calls it right after `StopPods`.
  The M2-T5 boot sweep remains the backstop.

## Caveats

- Early-stopped; small n; one generator. Directional, not conclusive.
- The anchor remains the F4-flagged instrument; the generator number is a
  pass@k on deterministic tests (the trustworthy signal), but the sample is tiny.
