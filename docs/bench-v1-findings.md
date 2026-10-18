# Benchmark v1 findings — the pilot run (2026-10-17)

**Status:** findings from the first full v1 slice · **Refs:**
[ADR-0075](adr/0075-frozen-corpus-compass.md), [frozen-corpus-v1.md](frozen-corpus-v1.md),
[eval/bench/README.md](../eval/bench/README.md).

The v1 pilot is the 70 deterministic tasks on the canonical persona set
(`ornith-1.5:9b` + `granite4.2:3b` security, context 32k/16k, full arm), run
**inside the Core Pod** with a named state volume. It finished in **64 minutes**
(well inside the 12 h cap). This note is the audit that drives v2; it does **not**
edit v1.

## Headline

| slice | n | pass | rate |
|---|---:|---:|---:|
| all | 70 | 56 | 80% |
| tuning | 56 | 44 | 79% |
| held-out | 14 | 12 | 86% |
| E | 13 | 11 | 85% |
| M | 31 | 25 | 81% |
| H | 26 | 20 | 77% |
| code | 30 | 30 | **100%** |
| document | 22 | 19 | 86% |
| text | 12 | 7 | 58% |
| data | 6 | 0 | **0%** |

No harness errors; no orphan Job Pods. Web-search/news tasks were not part of
v1.

## Failure audit

The 14 failures are all engine-level rejects (no accepted artifact). Their
candidate artifacts are in the exported state (`state-v1.tar`), so they can be
scored independently. Three classes emerged.

### 1. Correct content rejected by a too-literal format check

- **`data-e-csv-json`** — the best candidate contains the **correct JSON array**
  (`[{"name":"Ada","age":36},{"name":"Sam","age":41}]`) inside a fenced
  Markdown block with a short explanation. The verifier rejects the whole
  artifact (`invalid character 'T' looking for beginning of value`) because the
  task says “output only JSON.” This is a **format/check mismatch**: the model
  produced the right data, but the check demands a bare document.
- **`adv-h-unsafe-request`** — several candidates score 1.0 and refuse the
  unsafe request, but the verifier reports `required phrase missing: cannot`.
  The model used a different refusal wording (“I won’t…”), which is semantically
  correct. This is a **literal-check brittleness**, not a capability failure.
- **`text-e-bio`** (E) — the candidate is a **tool-call transcript** (“I’ll
  explore the project…” plus a `<tool_call>` block), not a bio. This is a real
  synthesis/format failure: the engine kept reasoning/tool text as the artifact.
  It is not the `must_contain: engineer` check’s fault; the artifact is not the
  requested document.

### 2. Real capability/format failures

- **`doc-cited-brief`** — the long-source cited brief did not meet the
  citation/word constraints; no accepted artifact. Capability/context retention.
- **`text-level-rewrite`** — the rewrite failed the entity/word constraints;
  likely a mixture of omission and format.
- **`doc-support-reply`** (held-out) — the support reply failed its section
  constraints; capability/format.
- **`data-json-normalize`, `data-m-filter`, `data-m-dedupe-sort`,
  `data-m-nested-normalize`, `data-h-schema-validate`** — all data tasks failed
  the JSON/schema gate. At least the E task’s content is correct-in-prose; the
  M/H candidates need the same extraction audit. The common cause is the
  **bare-JSON output contract**, not necessarily the schema reasoning.
- **`text-professional-email`, `text-plain-rewrite`, `text-counterargument`** —
  word/forbidden/paragraph constraints; some are real, some are likely literal
  (the exact forbidden phrase or an added heading).
- **`text-e-bio`** — see the tool-trace case above.

### 3. Saturation

- **Code is 30/30.** The v1 code tasks (including all H) do not provide headroom
  for `ornith9b`. The v1 corpus cannot measure a code capability delta on this
  model. v2 must be substantially harder and repo-scale/multi-file.

### 4. Held-out vs tuning

Held-out 86% > tuning 79%: no overfitting signal in this pilot. The held-out
sample is only 14 tasks, so this is directional, not statistical.

## External-scoring gap (fixed in the probe)

Before this audit, `runOne` only read the **accepted** artifact. When the engine
rejected everything, the probe had no text and `CheckPass` stayed nil, so the
report fell back to the engine’s accept. That made the non-code headline depend
on the engine’s judge/verifier, contrary to the frozen-benchmark rule.

The probe now scores the **highest-scoring proposal artifact** (via
`bestCandidateArtifact`, with a last-proposal fallback) with the task’s own
checks, regardless of the engine’s decision. Code tasks are unchanged (their
test command is the check). This makes the v2 non-code headline external. The
v1 report remains the pre-fix artifact.

## Implications for v2

1. **No easy tier.** Remove E; keep a hard floor and add an **X** tier above H.
   The old H tasks are the floor; X tasks are repo-scale/multi-file or
   long-context synthesis.
2. **Harder, larger code.** Multi-file packages, bug hunts in provided repos,
   interpreters/merge/pools/migrations with hidden edge cases. Code is the
   saturated archetype, so it gets the largest expansion.
3. **Fair deterministic checks.** Data checks must extract a fenced JSON block;
   refusal checks must accept `cannot`/`can't`/`won't`/`will not`/`unable`;
   text checks should reject tool-call traces explicitly. The goal is a hard
   task with a fair gate, not a brittle one.
4. **Artifact-only synthesis.** Text/document/task outputs must strip tool
   traces; a `<tool_call>` in an artifact is a failure.
5. **Held-out and anchor re-cut.** v2 gets a new digest, anchor, and held-out
   slice; v1 stays archived as the pilot baseline.
6. **Reference validation.** Every v2 code task ships a reference (including
   multi-file fixtures) and the test runs it end-to-end.

## v1 disposition

v1 is the **pilot**, not the 0.0.0 instrument. It remains runnable and its
report/state are retained (`out-full/`, `state-v1.tar`). v2 is cut as the
0.0.0 benchmark; this is a new version, not an edit to v1.
