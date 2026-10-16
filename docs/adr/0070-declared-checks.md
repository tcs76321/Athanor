# ADR 0070 — Declared deterministic checks (the document/text acceptance edge)

**Status:** Accepted — M9 · **Date:** 2026-10-16 · **Refs:**
ARCHITECTURE §6.2, §9.3, §13.1, §19; ADR-0031 (project execution config),
ADR-0045/ADR-0058 (verification-first gates), ADR-0061 (bench corpus);
`internal/project`, `internal/verify`, `internal/engine/verify.go`,
`eval/bench`, `spikes/m3-t7-probe/checks.go`

## Context

F4 made deterministic verification the load-bearing acceptance signal
(ADR-0045): code candidates are decided by a real test run, `data` candidates
by a JSON Schema (M9-T3). Text and document candidates had no such edge — the
`structureVerifier` only parses a *heuristic subset* of the free-text
acceptance criteria (word limits, exact paragraph counts, "sections named …",
placeholder absence, `at least N x`), and falls back to the LLM judge for
anything it cannot parse.

The M9 soak made the gap concrete. The `eval/bench` corpus declares a
machine-readable `checks:` block per non-code task
(`spikes/m3-t7-probe/checks.go`, M8-T18/T21) — `required_sections`,
`strict_section_order`, `exact_paragraphs`, `max_words`, `forbidden_phrases`,
`must_contain`, `count_alternatives_min`, `count_risks_min` — and scores the
accepted artifact against it *offline*. But the engine never saw those checks:
it saw only the natural-language `criteria:`. So the benchmark's ground truth
and the engine's acceptance could disagree, and a document task could be
accepted at runtime that the corpus would fail. The LLM judge could not close
the gap — it is advisory and its confidence signal is degenerate (ADR-0046,
G-F4 closeout).

## Decision

**Add an operator-declared, machine-checkable constraint set to the project
execution config, enforced by a hard verifier.**

- `project.Execution` gains `Checks *ExecutionChecks`, riding
  `projects.execution_json` (ADR-0031) — **no migration**. The zero value
  declares nothing and is exactly pre-ADR behavior.
- `internal/verify` gains `checksVerifier`, registered in `Default()` for the
  `text` and `document` archetypes. It is **hard**: a declared check is
  authoritative, like a code test run or a JSON Schema, so a failure decides
  rejection and the reward-hacking guard (ADR-0046) protects it. A candidate
  with no declared checks leaves the verifier unapplied.
- The vocabulary mirrors the corpus's deterministic keys — `required_sections`,
  `strict_section_order`, `exact_paragraphs`, `min_words`/`max_words`,
  `forbidden_phrases`, `must_contain`, `no_headings`, `no_placeholders`, and a
  general `min_items` (`[{noun, min}]`) that subsumes
  `count_alternatives_min` / `count_risks_min`. Matching rules (case-insensitive
  phrases, ATX-heading detection, body-fallback for a count whose heading is
  absent) mirror the benchmark scorer so the two agree.
- The engine threads `p.Execution.Checks` into `verify.Input` in
  `verifyCandidate`, so both `phaseEvaluate` and `phaseCompare` enforce it
  through the existing `HardDecisive()` path. The API `projectExecution` wire
  type and `athanor project create -checks <json>` expose it.
- The probe (`spikes/m3-t7-probe`) translates a bench task's `checks:` into the
  project's execution config — and, for `data`, maps `json_schema` to
  `data_schema` — so the benchmark's deterministic checks are enforced at
  runtime, not only scored after the fact. The probe keeps its own `runChecks`
  as the **independent** offline scorer: the benchmark does not trust the
  engine's verifier to grade the engine.

## Consequences

- A `text`/`document` goal whose project declares checks is accepted only when a
  parser agrees; a failing declared check routes through reflection/repair and,
  if no candidate passes, fails or escalates per §7.2 — exactly as a failed code
  test does. This is the missing edge the F4/G-F4 "deterministic verifiers decide
  acceptance" arm wanted for non-code tasks.
- The `structureVerifier` is unchanged and stays advisory (non-hard): it still
  parses whatever the free-text criteria state, while the declared checks are the
  operator's explicit, authoritative set. Both can be present; the declared set
  never weakens a heuristic match into a fail (it is a separate verifier).
- Matching-rule parity with `spikes/m3-t7-probe/checks.go` is by construction
  (mirrored helpers) and by test; drift between the two is a benchmark-validity
  bug the tests should catch.
- `min_distinct_arguments` (corpus `text-argued-essay`) is deliberately **not**
  in the engine vocabulary: it is a proxy (`countParagraphs >= n`) rather than a
  true structural check. The probe's offline scorer still reports it.
- A declared check set that is entirely no-op entries (e.g. a `min_items` entry
  with `min <= 0`) leaves the verifier unapplied rather than a free pass.

## Alternatives rejected

- **Fold the checks into the free-text criteria and rely on `structureVerifier`.**
  That is the status quo and the gap: the parser sees only what the phrasing
  happens to expose, and the operator cannot state a constraint the parser does
  not already recognize.
- **Make the declared verifier advisory (non-hard).** Then it could not decide
  acceptance, which is the point; it would be one more signal feeding a
  degenerate judge.
- **Have the probe import `internal/verify` for its scorer.** The benchmark's
  value is an *independent* check; sharing the engine's verifier would make the
  measurement circular.

## Not in scope

- A `media` deterministic verifier (still deferred, ROADMAP §7).
- Declared checks for `code` (the test command and F5 gates already cover it) or
  `data` (`data_schema` covers it).
- Encoding the corpus's fixture-dependent checks (`required_substrings_source`,
  `preserve_entities_from_fixture`, `every_item_once`) — these stay advisory in
  the probe.
