# Athanor benchmark corpus (v1)

**80 items: 70 deterministic tasks + 4 memory scripts + 6 human-review
tasks.** This is the frozen benchmark for 0.0.0: it is cut once, never edited
until 1.0.0, and it is **not CI** ([ADR-0075](../../docs/adr/0075-frozen-corpus-compass.md)).
The machine-readable corpus is [`tasks.yaml`](tasks.yaml); the design decision
is [ADR-0061](../../docs/adr/0061-harder-benchmark.md). The protocol and
composition are restated in [`docs/frozen-corpus-v1.md`](../../docs/frozen-corpus-v1.md).

The headline metric is the **deterministic pass rate**, scored by the task's own
test command or checks — never by the engine's judge. The LLM judge is advisory
and recorded separately.

## Why

The M3-T7 10-goal set hit a **ceiling**: in the 2026-10-06 10-model probe, code
goals scored 1.00 in *both* arms for nearly every model, and the strongest
models saturated on every goal. A benchmark that capable models ace cannot
measure:

- whether N-candidate divergence beats single-shot (no headroom);
- whether a judge discriminates (no spread to order);
- whether compute-adaptive policies (N=1 for easy, N=k for hard) do anything;
- whether the MCE's context paths help (nothing needed much context).

This corpus is designed with headroom and with tasks that are **sensitive to
what we change**: context-heavy documents, a repo-backed refactor, multi-step
memory scripts, and mechanism metrics (retrieval hit, context/KV, compaction,
tokens, latency), not just pass rates.

## Composition

| stratum | n | E | M | H | notes |
|---|---:|---:|---:|---:|---|
| code | 30 | 6 | 12 | 12 | self-contained `solution.py`, except the repo-backed refactor |
| document | 16 | 3 | 8 | 5 | fixed sections, citations, runbooks, postmortems |
| text | 12 | 3 | 6 | 3 | word limits, forbidden phrases, paragraph structure |
| data | 6 | 1 | 3 | 2 | JSON Schema + array length/order/uniqueness/format checks |
| adversarial | 6 | 0 | 2 | 4 | flag, refuse, correct — never fabricate |
| **deterministic** | **70** | **13** | **31** | **26** | in `tasks.yaml` |
| memory scripts | 4 | — | — | 4 | multi-job sequences in `memory-scripts.yaml` |
| human review | 6 | — | 2 | 4 | rubric-scored in `human-review.yaml` |
| **total** | **80** | | | | |

The adversarial tasks use `archetype: document` in the YAML (the artifact is
prose) and carry `category: adversarial`; the table counts them as their own
stratum. Five tasks also carry `category: odd` (exactly verifiable but unusual
shapes) to keep the set from becoming uniformly conventional.

### Held-out slice

**20 of 80 items are held out (25%)**: 14 deterministic + 3 memory + 3 human.
They are marked `held_out: true` and are **never used for tuning**. The probe's
report splits the held-out pass rate from the tuning pass rate; a gain that
appears only on tuning is not evidence. The held-out tasks span code, document,
text, data, memory, and human review — not just the hardest tier.

### Context demand

Every task declares `context_demand: short | long | repo | memory`:

- **short** — the goal carries everything; a small context is sufficient.
- **long** — a fixture or a long prompt that must be read without losing facts.
- **repo** — the task indexes a repository (`repo:`), exercising the Dormant
  Index and the MCE working set.
- **memory** — a later job depends on a correction, insight, or compacted fact.

This is a *what it stresses* label, orthogonal to difficulty. A task can be M
and long, or H and short.

## Design principles

1. **Deterministic ground truth.** Every task has machine checks — a real test
   command for code, structural/schema/count checks otherwise — so acceptance
   does not depend on the LLM judge.
2. **Headroom by construction.** Difficulty comes from multi-file structure,
   edge cases, exact-output requirements, multi-constraint prompts, long
   context, and adversarial/ambiguous specs — not from arbitrary obscurity.
3. **Layered difficulty.** E (sanity), M (discriminating), H (headroom). The
   old M3-T7 10-goal set remains the E baseline for the probe; this corpus
   carries its own E tasks so the composition is self-contained.
4. **Judge-relevant by design.** Each task ships a **quality rubric**; the human
   anchor is the durable teacher and the LLM judge is advisory.
5. **Archetype breadth.** code, document, text, data, plus adversarial honesty
   and memory/compaction.
6. **No external web.** Every fact a task needs is in the goal or a fixture.
   The benchmark must run inside the Core Pod with no network.
7. **Frozen.** The set is cut at x.0.0 and not edited until (x+1).0.0. A
   change is a new version, not an edit.

## Schema

Each task in `tasks.yaml` carries:

| field | meaning |
|---|---|
| `id`, `title` | stable identifiers (IDs are never reused) |
| `archetype` | `code` \| `document` \| `text` \| `data` |
| `tier` | `E` \| `M` \| `H` |
| `language` | `python` for code tasks |
| `goal` | the prompt text (20–500 chars) |
| `criteria` | the task's acceptance criteria |
| `fixture` | path to the starter files/source the task refers to |
| `repo` | path indexed as a repository (repo-backed tasks) |
| `test_command` | the real deterministic test the Job Pod runs (code) |
| `checks` | deterministic checks for non-code archetypes |
| `held_out` | never used for tuning |
| `context_demand` | `short` \| `long` \| `repo` \| `memory` |
| `category` | optional cross-cutting tag (`adversarial`, `odd`) |
| `acceptance_floor` | what must hold for the task to count as passed |
| `quality_rubric` | what distinguishes 1/3/5 for the human anchor and judges |
| `context_size` | `small` \| `medium` \| `large` (a coarse hint) |
| `expected_failure_modes` | the ways a weak model fails |

## Deterministic check kinds

These are the checks the offline scorer actually decides (`checks.go`):

`required_sections`, `strict_section_order`, `exact_paragraphs`, `max_words`,
`forbidden_phrases`, `no_headings`, `count_alternatives_min`,
`count_risks_min`, `min_distinct_arguments`, `must_contain`, `json_schema`,
`array_length`, `array_unique_by`, `array_sorted_by`, `json_field_matches`.

The JSON-array checks exist because a JSON Schema can validate shape but not
count, ordering, uniqueness, or a timestamp format. `json_schema` supports the
subset type, required, properties, items, enum, additionalProperties, and
allOf/anyOf — deliberately small, with no new dependency.

These are **advisory only** (reported, never gating): `every_item_once`,
`required_substrings_source`, `preserve_entities_from_fixture`,
`must_flag_contradiction`, `must_not_fabricate`, `no_fabricated_commands`,
`max_new_claims`, `exactly_one_cta`, `required_citations_from_fixture`. The
human anchor is the fallback for those qualities.

## Files

| path | contents |
|---|---|
| [`tasks.yaml`](tasks.yaml) | 70 deterministic tasks |
| [`memory-scripts.yaml`](memory-scripts.yaml) | 4 multi-job memory scripts |
| [`human-review.yaml`](human-review.yaml) | 6 rubric-scored tasks |
| [`reference.yaml`](reference.yaml) | reference solutions for the 30 code tasks (validation only) |
| `fixtures/<id>/` | starter code, long sources, JSON schemas, the review diff |

## Validation

The corpus is guarded by `spikes/m3-t7-probe/corpus_v1_test.go`:

- **Structure:** 70 tasks, unique IDs, the exact archetype/tier spread, a
  `context_demand` on every task, a test or checks on every task, and every
  fixture/repo/JSON-schema path exists.
- **Extras:** 4 memory scripts with task → correction/insight → task steps, 6
  human tasks with a 5/3/1 rubric, and exactly 20 held-out items.
- **Reference execution:** every code task's real `test_command` is run against
  its reference solution. This is the proof that no code check is
  unsatisfiable or simply wrong. It skips only when `python3` is absent (the
  benchmark itself runs in the Job Pod, which has Python).

```
CGO_ENABLED=1 go test -tags sqlite_fts5 ./spikes/m3-t7-probe/ -run TestBench -v
```

## Scoring channels

1. **D1 — deterministic** (primary): `test_command` exit 0 and every
   deterministic `checks` entry. This is the ground truth for acceptance.
2. **D2 — objective artifact labels**: pass/fail plus the failed check kinds.
3. **Human anchor** (secondary): a human rates 1–5 per the `quality_rubric` for
   the deterministic and human-review tasks. The **human** rating is the
   durable teacher.
4. **Mechanism metrics**: retrieval hit, context/KV, compaction, tokens,
   latency, candidate diversity, and whether a correction/insight was actually
   injected — the evidence that a change worked for the right reason.
5. **Cost**: tokens and wall time, for quality-per-token/per-minute.

LLM judges are **advisory only**. The probe records their scores but never lets
them gate acceptance.

## The corpus

### Code (30)

`code-e-fizzbuzz` (E), `code-e-sum-evens` (E), `code-e-reverse-words` (E),
`code-e-count-vowels` (E, held-out), `code-e-is-palindrome` (E),
`code-e-max-three` (E), `code-bank-atomic` (H), `code-expr-eval` (H),
`code-lru-ttl` (H), `code-unified-diff` (H), `code-topo-sort` (H),
`code-csv-rfc4180` (H, held-out), `code-rotated-search` (H),
`code-template` (H), `code-retry-backoff` (H),
`code-priority-queue` (H, held-out), `code-json-patch` (H),
`code-weighted-intervals` (H), `code-merge-intervals` (M, held-out),
`code-rle` (M), `code-roman` (M, held-out), `code-semver` (M),
`code-glob` (M, held-out), `code-json-pointer` (M), `code-token-bucket` (M),
`code-union-find` (M, held-out), `code-deep-merge` (M),
`code-parentheses` (M), `code-parse-duration` (M),
`code-refactor-provided` (M, repo-backed).

### Document (16)

`doc-e-readme` (E), `doc-e-meeting-notes` (E), `doc-e-bug-report` (E, held-out),
`doc-runbook` (M), `doc-release-notes` (M, held-out), `doc-onboarding-checklist`
(M), `doc-support-reply` (M, held-out), `doc-data-dictionary` (M),
`doc-migration-checklist` (M), `doc-api-readme` (M, repo-backed),
`doc-changelog-categorize` (M), `doc-incident-postmortem` (H),
`doc-threat-model` (H, held-out), `doc-migration-guide` (H), `doc-design-adr`
(H), `doc-cited-brief` (H).

### Text (12)

`text-e-summary` (E), `text-e-steps` (E, held-out), `text-e-bio` (E),
`text-professional-email` (M), `text-product-description` (M, held-out),
`text-plain-rewrite` (M), `text-meeting-summary` (M), `text-faq` (M),
`text-argued-essay` (M), `text-constrained-email` (H),
`text-level-rewrite` (H), `text-counterargument` (H).

### Data (6)

`data-e-csv-json` (E), `data-m-filter` (M), `data-m-dedupe-sort` (M),
`data-m-nested-normalize` (M), `data-json-normalize` (H),
`data-h-schema-validate` (H, held-out).

### Adversarial (6)

`adv-m-missing-info` (M), `adv-m-prompt-injection` (M),
`adv-h-false-premise` (H), `adv-h-contradictory-requirements` (H),
`adv-h-unsafe-request` (H), `adversarial-ambiguous-spec` (H).

## Memory scripts

A memory script is a sequence in one project (or across projects) that tests
whether a **correction**, **insight**, or **compacted fact** reaches a later
job. The deterministic check applies to the later task; the mechanism metric is
whether the item was actually in the prompt. The four scripts:

| id | tests | held-out |
|---|---|---|
| `mem-correction-error-type` | a corrected error type survives into the next job | yes |
| `mem-insight-input-validation` | an outcome insight is applied to a new parser | no |
| `mem-cross-project-secrets` | a global security correction reaches another project | yes |
| `mem-compaction-fact` | a fact survives compaction | yes |

**Status:** the steps are fully specified; the probe's sequence runner is the
remaining implementation (it must create one project, submit the correction/
insight, run the later job in the same project, and read the injection from the
event log). The scripts are frozen now so the runner has a target.

## Human review

Six tasks with no deterministic check, scored by a human against a 5/3/1
rubric: a ship-or-wait memo, a design review, a tone rewrite, a SQLite vs
Postgres tradeoff, an incident update, and a code review of a provided diff.
They are a **calibration set**, reported separately — not part of the headline
pass rate. Three are held out.

## Probe integration

1. ~~Load `tasks.yaml` into the probe.~~ **Done:** `corpus.go`, driven by
   `run -corpus eval/bench/tasks.yaml`.
2. **Done:** `held_out`, `context_demand`, and `category` are carried into the
   metrics and the CSV, and the report has a held-out vs tuning table.
3. **Done:** code tests and non-code checks are both deterministic gates; the
   reference test proves the code gates are satisfiable.
4. **Next:** the memory-script sequence runner and the human-review queue. The
   corpus is frozen; only the runner changes.

## Required assets and known gaps

- **Reference solutions** are present and executed in CI for code tasks.
- **Repo-backed coverage** is two tasks (`code-refactor-provided`,
  `doc-api-readme`). More repo-scale code tasks are the first 1.0.0 addition,
  not an edit to v1.
- **Anchor pairs** (a good and a near-miss artifact per M/H task) are still
  needed for judge-discrimination measurement. The quality rubric and
  `expected_failure_modes` are the brief for authoring them.
- **Memory runner** and **human-review queue** are runner work, not corpus
  edits.

## Limitations

- Tiers are hypotheses until measured; the first full run may show an H task is
  too easy. A correction is a **new corpus version**, not an edit.
- Some qualities (fabrication, reading level, review depth) are advisory; the
  human anchor is the fallback.
- n is 80 items, not thousands: report effect sizes and per-stratum counts, not
  p-values.
- Code tasks assume the Job Pod image has Python; multi-file candidates use
  `=== FILE: path ===` blocks.
