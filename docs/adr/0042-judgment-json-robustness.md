# ADR 0042 — Judgment JSON robustness: schema-constrained format + tolerant, audited decoding

**Status:** Accepted · **Date:** 2026-10-05 · **Refs:** ARCHITECTURE §13.1, §19; [ADR-0012](0012-llm-format-json.md); ROADMAP M3-T7; `docs/probes/m3-t7-quality-probe.md`

## Context

The M3-T7 pre-flight smoke (goal #1, `ornith-1.5:9b`, both arms) failed **3 of 4
jobs**, two of them at the evaluation phase:

```
parsing security verdict: ... cannot unmarshal string into
  Go struct field evalVerdict.confidence of type float64
parsing security verdict: ... cannot unmarshal string into
  Go struct field evalVerdict.style_issues of type []string
```

ADR-0012 added `format: "json"` to the judgment phases and consolidated the
brace-scanner parsers. That work was correct, but the smoke proved it
insufficient: **`format:"json"` guarantees *parseable* JSON, not *typed* JSON.**
A model can emit `"confidence": "0.95"` (a string) and `"style_issues": "..."`
(a string where an array is wanted) and still be "valid JSON". The strict
`json.Unmarshal` into `evalVerdict` then fails the entire job — a phase and a
job lost to a type annotation the model did not respect.

The same smoke surfaced a second, quieter defect: the judge returned
`score: 96` in one run and `score: 0.96` in another. The evaluation prompt
never pinned the scale, and `strategy_outcomes.score` is the raw maximum of
`EvaluationRecord.score` (`internal/engine/strategy.go`). Any downstream
analysis (the T-b calibration bins) assumes 0.0–1.0, so an unnormalized 96
is silently wrong.

Two principles constrain the fix:

1. **Judgment is an invariant.** §4.2 pins evaluation and comparison to the
   `security` persona at Temperature 0.0. A judgment that cannot be *read*
   is not a judgment, and silently failing a job on a type annotation is worse
   than recovering it and recording the recovery.
2. **Do not hide signal.** The probe wants to know how reliably each model
   holds the judgment contract. A tolerant parser that silently coerces would
   mask exactly the measurement M3-T7 exists to produce.

## Decision

Three coordinated changes.

### D1. Tolerant, audited decoding

`parseVerdictJSON` gains a tolerant second pass
(`internal/engine/parse_verdict.go`). The strict decode runs first; on failure
the object is decoded generically, each field is coerced to the destination
type (string↔number, string→`[string]`, bool↔number, number→string), and the
result is decoded again. Each coercion is reported. The callers
(`phaseEvaluate`, `phaseCompare`) emit a `verdict_coerced` audit row naming
the phase and the coerced fields when the report is non-empty.

The audit row is the point: the raw conformance rate is now *measurable*
(`verdict_coerced` count / judgment calls) even though the job succeeds.
Coercion handles only the drift shapes seen in practice; an uncoercible value
(e.g. an object where a string is wanted) is still a hard `VerdictParseError`,
so tolerance never swallows garbage.

### D2. Schema-constrained `format` (opt-in, default off)

`llm.Request.Format` becomes `any`, and the engine can send a **JSON schema**
for the two judgment phases (`internal/engine/verdict_schema.go`), so Ollama
grammar-constrains field *types*, not merely JSON well-formedness.

It is **off by default** (`inference.json_schema: false`). The M3-T7 smoke
found that schema-constrained decoding on `ornith-1.5:9b` can run away on an
unbounded string field: one candidate evaluation generated for over seven
minutes with no output, where `format:"json"` returns in ~30 s on the same
input. Because D1 already handles the type drift the schema was meant to
prevent, the default stays `format:"json"`; the schema is an opt-in
experiment. Promoting it would first require bounding generation (e.g. a
`num_predict` cap or bounded string fields in the schema).

### D3. Pinned score scale

The evaluation prompt now states `score (0.0-1.0)` and `confidence (0.0-1.0)`,
and `phaseEvaluate` normalizes defensively: a score `> 1` is divided by 100
and clamped to 1. The §19.2 score and the strategy outcome therefore share one
scale regardless of the model's unit choice.

## Consequences

- The 9B judge no longer hard-fails a job on a type annotation; the smoke's
  exact failure is recovered and audited. The job still completes, and the
  disagreement is visible in the event log.
- Every judgment call records its format (`json-schema`) and, when
  `judgment_seed: derived`, its seed — the full generation provenance.
- The `verdict_coerced` rate is a first-class M3-T7 metric: it distinguishes
  "the model conforms" from "the parser cleaned up", which the probe reports
  per model.
- Schema-constrained decoding is available but off by default, because the
  smoke showed it can hang on a weak model. A model or Ollama version that
  ignores the schema degrades to D1, not to failure; a genuinely unreadable
  verdict is still a hard error and still routes to `failed`.

## Alternatives rejected

- **Strict parsing, treat the failure rate as the finding.** Honest but leaves
  the loop unusable in production and blocks the M3-T7 measurement entirely.
- **Lenient parsing without an audit row.** Would mask the conformance signal
  the probe needs; rejected under principle 2.
- **Coerce silently and never fix the prompt/schema.** Leaves the model
  unguided and the coercion doing all the work; rejected under principle 1.

## Not in scope

- Retry-on-parse-failure (still not justified; the schema + tolerance handle
  the observed class).
- A full JSON-schema library or validation on the engine side; Ollama owns
  the constraint, the engine owns the recovery.
