# Frozen corpus v1 — design and authoring spec

**Status:** proposed (authoring not started) · **Date:** 2026-10-17 · **Refs:**
[ADR-0075](adr/0075-frozen-corpus-compass.md) (frozen-corpus compass),
[ADR-0061](adr/0061-harder-benchmark.md) (harder benchmark),
[ADR-0070](adr/0070-declared-checks.md) (declared checks); `eval/bench/`.

This spec defines the controlled study ADR-0075 calls for: a corpus that is
**pinned from 0.0.0 until 1.0.0**, large and representative enough to carry that
span, and never edited. It replaces the current 15-task `eval/bench/tasks.yaml`
as the fixed reference.

## 1. Immutability

- v1 is **content-pinned**: a SHA-256 over `eval/bench/tasks.yaml` plus every
  fixture tree, recorded in `eval/bench/CORPUS.digest`.
- v1 is **never edited** after the 0.0.0 freeze. New or corrected tasks go into
  **v2** (`eval/bench/v2/`), and v1 stays comparably runnable forever.
- The digest is checked by a small `make corpus-check` target (a doc/discipline
  check, not CI).

## 2. Composition

Target **40 tasks**, weighted so the corpus has headroom (a good model should
not saturate it) and covers every archetype:

| Archetype | E | M | H | total |
|---|---|---|---|---|
| code | 2 | 6 | 8 | 16 |
| document | 2 | 5 | 5 | 12 |
| text | 2 | 4 | 2 | 8 |
| data | 1 | 2 | 1 | 4 |
| adversarial | 0 | 1 | 3 | 4 |
| **total** | **7** | **18** | **19** | **40** |

Rationale: code dominates because its deterministic tests are the strongest
grounded signal; document/data carry the deterministic-check arms; text and
adversarial keep the judge/verification pressure; adversarial supplies the
"must not fabricate / must flag" cases.

Runtime target: **≤ 3 h for one full pass** on the reference 9B host, and
**≤ 30 min for the smoke subset** (below). If a task blows the per-task budget,
it is a corpus bug and must be fixed **before** the freeze.

## 3. Tiers and held-out

- **E** — sanity: a competent model should pass. Guards against harness
  regressions.
- **M** — discriminating: separates model tiers.
- **H** — headroom: a 9–12 B should fail a meaningful fraction; if every model
  passes an H task, it is really M.
- **Held-out slice**: **10 of 40** (one per archetype plus extras) are marked
  `held_out: true`. They run in the soak like any other task, but they are
  **never used to tune prompts, routing, thresholds, or the escalation ladder**.
  They exist so a reported improvement has an uncontaminated check.

## 4. Per-task requirements (the authoring checklist)

Every task must have:

1. **One clear goal** with an explicit, bounded deliverable.
2. **Deterministic acceptance**: a `test_command` (code) or `checks:` map
   (document/text/data), never a judge-only gate.
3. **A self-contained fixture** checked into the repo (no network, no absolute
   paths, no generated-at-runtime inputs).
4. **No network** and no external services.
5. **A tier justification** (why E/M/H) and, for H, **headroom evidence**: at
   least one reference model fails it.
6. **Expected failure modes** (the trap the task is testing).
7. **A quality rubric** for the human anchor / advisory judge.
8. **A runtime bound** (the task must finish within the per-task budget).
9. **Contamination check**: the task and its fixture do not appear in the
   anchor set, the qualification suite, or any tuning corpus.
10. **A stable name** (`code-…`, `doc-…`, `text-…`, `data-…`,
    `adversarial-…`) so results join across versions.

## 5. Smoke subset

A fixed **6-task** subset for engine-surface changes (ADR-0075 §3): two H code,
one M code, one H document, one H text, one H data. It must finish in ~30 min
on the reference host and catch the common regressions (candidate parsing,
verdict parsing, checks, pod execution, context assembly, scheduling).

## 6. Outputs retained per run

ADR-0075 §4 requires raw, re-gradable evidence. A run stores: per-task
artifacts, verdicts, check results, `strategy_outcomes`, the event log, and
costs — not only aggregate scores. A future verifier or judge can then re-score
the same run.

## 7. How it is used

- **Smoke** after any engine-surface change; record a one-paragraph delta.
- **Full** before a tag; compare against the previous tag's run on the same v1.
- **Never** as a CI job and never as a tag blocker (ADR-0075).
- **Never** tune on the held-out slice.

## 8. Open items

- Author the 25 new tasks (the current corpus has 15; v1 needs 40).
- Mark the held-out 10 and record the digest.
- Add `make corpus-check` (digest verification) and a smoke-subset selector.
- Validate the runtime budget on the reference host before freezing.
