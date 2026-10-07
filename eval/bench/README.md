# Athanor benchmark corpus

A harder, objectively-scored task set for the dialectical-vs-single-shot
probe and for judge calibration. The machine-readable corpus is
[`tasks.yaml`](tasks.yaml); the design decision is recorded in
[ADR-0061](../../docs/adr/0061-harder-benchmark.md).

## Why

The M3-T7 10-goal set hit a **ceiling**: in the 2026-10-06 10-model probe,
code goals scored 1.00 in *both* arms for nearly every model, and the strongest
models saturated on every goal. A benchmark that capable models ace cannot
measure:

- whether N-candidate divergence beats single-shot (no headroom);
- whether a judge discriminates (no spread to order);
- whether compute-adaptive policies (N=1 for easy, N=k for hard) do anything.

This corpus is designed with headroom: a 9–12 B local model should **not** pass
most hard (H) tasks.

## Design principles

1. **Deterministic ground truth.** Every task has machine checks — a real test
   command for code, structural/schema/count checks otherwise — so acceptance
   does not depend on the LLM judge. This mirrors the one probe result that was
   unambiguous: deterministic verification carries acceptance.
2. **Headroom by construction.** Difficulty comes from multi-file structure,
   edge cases, exact-output requirements, multi-constraint prompts, long
   context, and adversarial/ambiguous specs — not from arbitrary obscurity.
3. **Layered difficulty.** E (sanity), M (discriminating), H (hard). H tasks
   are the measuring instrument; E tasks catch harness regressions.
4. **Judge-relevant by design.** Each M/H task ships a **quality rubric** and an
   **anchor pair** (a good artifact and a near-miss, objectively labeled), so a
   judge is measured on *within-task ordering* — the correlated-error property
   that cross-case anchor scoring does not capture.
5. **Archetype breadth.** code, document, text, data, plus an adversarial task
   that rewards honest uncertainty over confident fabrication.
6. **Reproducibility.** Fixed prompts, fixed test commands, pinned model
   digests, temperature-0 judgment, `think:false`; the corpus is versioned.

## Schema

Each task in `tasks.yaml` carries:

| field | meaning |
|---|---|
| `id`, `title` | stable identifiers |
| `archetype` | `code` \| `document` \| `text` \| `data` |
| `tier` | `E` \| `M` \| `H` |
| `goal` | the prompt text (20–500 chars) |
| `criteria` | the task's acceptance criteria |
| `fixture` | path to the starter repo/files (code/data tasks) |
| `test_command` | the real deterministic test the Job Pod runs (code) |
| `checks` | deterministic checks for non-code archetypes (see below) |
| `acceptance_floor` | what must hold for the task to count as passed |
| `quality_rubric` | what distinguishes 1/3/5 for the anchor and judges |
| `context_size` | `small` \| `medium` \| `large` (drives MCE/context paths) |
| `expected_failure_modes` | the ways a weak model fails (used to grade near-misses) |

### Deterministic check kinds

`required_sections`, `strict_section_order`, `exact_paragraphs`, `max_words`,
`forbidden_phrases`, `no_headings`, `count_alternatives_min`,
`count_risks_min`, `every_item_once`, `required_substrings_source`,
`preserve_entities_from_fixture`, `json_schema`, `sql_fixture`,
`must_flag_contradiction`, `must_not_fabricate`. A task passes only when its
`acceptance_floor` holds; ambiguous checks (`no_fabricated_commands`,
`max_new_claims`) are advisory for the judge, not deterministic gates.

## Difficulty calibration

Run the corpus in tiers and check the spread:

- **E** should pass for ~all models — a harness sanity check.
- **M** should discriminate across sizes (roughly 40–80% pass for 9–12 B).
- **H** should have headroom: a 9–12 B model below ~30%, and no model at 100%
  across a tier.

If an H task is passed by most models, it is too easy and gets demoted or
hardened. The tier assignment is a hypothesis to be corrected by the data.

## Scoring channels

1. **D1 — deterministic** (primary): `test_command` exit 0 and every `checks`
   entry. This is the ground truth for acceptance.
2. **D2 — objective artifact labels**: pass/fail plus missing criteria, derived
   from D1.
3. **Anchor rating** (secondary): a human (or, provisionally, an agent) rates
   1–5 per the `quality_rubric`. The **human** rating is the durable teacher.
4. **Anchor pairs**: within each M/H task, order the good vs near-miss artifact.
   This is the judge-discrimination measurement the old anchor lacked.
5. **Cost**: tokens and wall time, for quality-per-token/per-minute.

LLM judges are **advisory only**. The probe should record their scores but never
let them gate acceptance.

## Corpus

| id | archetype | tier | one-liner |
|---|---|---|---|
| `code-bank-multifile` | code | H | multi-file bank package + ledger, atomic transfer |
| `code-expr-eval` | code | H | arithmetic evaluator, precedence, typed errors |
| `code-lru-ttl` | code | H | O(1) LRU cache with TTL and edge cases |
| `code-unified-diff` | code | H | byte-exact unified diff |
| `code-refactor-provided` | code | M | refactor a provided module, tests + lint unchanged |
| `code-parse-duration` | code | M | duration parser with typed errors |
| `doc-api-readme` | document | M | README with real signatures, no fabrication |
| `doc-design-adr` | document | H | fixed-structure design doc, ≥3 alternatives, ≥2 risks |
| `doc-cited-brief` | document | H | cited summary of a long source, no external facts |
| `doc-changelog-categorize` | document | M | category + order raw changelog entries |
| `text-argued-essay` | text | M | exactly four paragraphs, 3 args + counterargument |
| `text-constrained-email` | text | H | word limit, single CTA, forbidden phrases |
| `text-level-rewrite` | text | H | reading-level rewrite, preserve all facts |
| `data-json-normalize` | data | H | normalize events to a JSON Schema |
| `adversarial-ambiguous-spec` | document | H | flag a contradiction, do not fabricate |

## Required assets (not yet implemented)

The corpus is the specification. Running it needs, per task:

- `eval/bench/fixtures/<id>/` — the starter repo/files (code, data) or the
  source material (document, text).
- Test suites (`code-*`) and check implementations for the non-code `checks`
  kinds.
- `anchor pairs` (good + near-miss artifacts) for judge calibration.

These are the enabling work; until they exist, `tasks.yaml` is a design, not a
runnable matrix.

## Probe integration

The M3-T7 probe currently hardcodes its goals in `spikes/m3-t7-probe/matrix.go`.
To consume this corpus:

1. Add a loader that reads `eval/bench/tasks.yaml` into the probe's
   `sampleGoal` shape (the fields map directly; `test_command` and `criteria`
   already exist).
2. Drive `run`/`judge`/`report` over the corpus as today; the model-aware
   report and judge-outer scoring already handle multiple models.
3. Add the anchor-pair check to `report` (within-task ordering per judge).

The probe is a spike, so this is a small loader plus fixture authoring, not an
engine change.

## Limitations

- Tiers are hypotheses until measured; expect to rebalance after the first run.
- Some checks are advisory, not deterministic (fabrication, reading level); the
  human anchor is the fallback for those.
- n is still modest (15 tasks); report effect sizes, not p-values.
- Multi-file code tasks assume the Job Pod image has the language toolchain and
  the declared test/lint tools installed.
