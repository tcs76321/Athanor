# ADR 0012 — `format: "json"` for the security persona + parser consolidation

**Status:** Accepted · **Date:** 2026-09-01 · **Refs:** ARCHITECTURE §13.1, §19; ROADMAP M3-T2 carry-over; `docs/m3-t1-plan.md:128–136`

## Context

The §13.1 Dialectical Loop contract requires the `security` persona at Temperature 0.0 to emit structured JSON in two phases:

- **Phase 3 (Evaluating)** — for every candidate, a verdict with `{passed, score, failed_tests, missing_criteria, security_issues, style_issues, better_than_previous, confidence, summary}`.
- **Phase 6 (Comparing)** — a verdict with `{winner: "new"|"previous"|"none", confidence, reasons, missing_requirements}`.

The prompts in `internal/engine/evaluate.go:218–226` and `internal/engine/compare.go:185` end with the explicit instruction `Output JSON only: {...}`, but the prompts cannot *enforce* that — the LLM is statistical, not contractual. A model that wanders into prose (or wraps the JSON in a code fence, or appends a trailing line) breaks the parser. The current parsers cope by manual brace scanning:

- `parseEvalVerdict` (`internal/engine/evaluate.go:269–324`) finds the first `{`, then walks character-by-character tracking brace depth and string-escape state, returns an error if no closing `}` is found.
- `parseComparisonVerdict` (`internal/engine/compare.go:211–268`) is a near-clone — same logic, different target struct.

The brace scanners are correct but they are doing work that Ollama can do at the wire layer. The Ollama `/api/chat` API supports a `format` parameter; setting it to `"json"` instructs Ollama to constrain the response to valid JSON (model-side, via the model's grammar), so the response comes back parseable as JSON without any wrapping or preambles.

Verified at the wire level: `internal/llm/client.go:65–70` defines `chatRequest` with exactly four fields — `Model`, `Messages`, `Stream`, `Options` (with `Temperature` and `NumCtx`). There is no `Format` field; the `Request` type at `client.go:32–39` is equally minimal. Ollama's documented `format` parameter is the missing piece.

The M3-T1 close-out already flagged the `comparison` winner string normalization as a polish item (`docs/m3-t1-plan.md:128–136`): an unknown `winner` value is silently downgraded to `"none"`, which can fail a job that should have been accepted. The brace scanners are an adjacent risk: a model that emits a single non-JSON character between the `{` and the `}` still produces a parse error, and the current behavior is to fail the phase (a job that should have been completed becomes `failed`).

## Decision

Two coordinated changes:

1. **Add `Format` to `llm.Request` and `chatRequest`, propagate it on the wire.**
2. **Consolidate the two brace-scanner parsers into a single `parseVerdictJSON[T]` helper that takes the target struct as a type parameter and uses `encoding/json` directly when the response is clean JSON, falling back to the brace scan only when the response is non-JSON wrapping (a code fence or a preamble).**

### D1. The wire change

`internal/llm/client.go`:

- Add `Format string \`json:"format,omitempty\"\`` to `chatRequest` (after `Stream`, before `Options` — matches Ollama's documented order).
- Add `Format string` to `Request`.
- In `Chat`, set `chatRequest.Format = req.Format` when `req.Format != ""`.

`internal/engine/phases.go` `call()` is the central call site; it builds the `llm.Request` for every phase. The `evaluating` and `comparing` calls set `Format: "json"`; all other phases leave it empty. The `call()` signature gains one parameter (or a struct field on the engine), but the simpler change is two literal `llm.Request{...}` builds at the two call sites that pass `Format: "json"`.

The other call sites (`phasePlan`, `phaseDivergeN`, `phaseReflect`, `phaseSynthesize`) do not change. They produce prose and do not need the JSON constraint.

### D2. The parser consolidation

A new file `internal/engine/verdict.go` holds:

```go
// parseVerdictJSON decodes a structured-JSON verdict from the security
// persona into dst, which must be a pointer to a struct with `json:`
// tags. When the response is clean JSON it is passed straight to
// encoding/json. When the response has wrapping (a code fence, a
// preamble, a trailing line), the helper falls back to brace scanning
// and decodes the inner object. The first path is the common case
// after ADR-0012; the second is the defense-in-depth fallback for
// non-conforming models and Ollama versions that ignore `format`.
func parseVerdictJSON[T any](content string, dst *T) error { ... }
```

The two existing parsers (`parseEvalVerdict`, `parseComparisonVerdict`) become one-liners:

```go
var v evalVerdict
if err := parseVerdictJSON(resp.Content, &v); err != nil { ... }
```

The `parseComparisonVerdict` caller does the winner-string normalization *after* the helper returns — that part is not parser logic, it's verdict semantics, and it stays in `phaseCompare` (where M3-T3 will extract it into `DecideWinner`).

### D3. The fallback path is preserved

The brace scanner is the fallback. Some Ollama versions ignore `format` for certain model families; some models emit `null` or `[]` for empty fields in a way `encoding/json` accepts but `format=json` may not. The fallback is the existing logic, copied once and parameterized.

A test asserts both paths:

- `TestParseVerdictJSON_CleanJSON` — `{"winner":"new","confidence":0.9,"reasons":[],"missing_requirements":[]}` decodes without brace scanning.
- `TestParseVerdictJSON_WrappedInCodeFence` — `` ```json\n{...}\n``` `` decodes via the brace scan fallback.
- `TestParseVerdictJSON_ProsePreamble` — `"Here is my verdict:\n{...}\n"` decodes via the brace scan fallback.
- `TestParseVerdictJSON_NoJSONObject` — `"I cannot evaluate this candidate."` returns an error.
- `TestParseVerdictJSON_Unterminated` — `{"winner": "new"` returns an error.

### D4. Behavior change in `phaseEvaluate` and `phaseCompare`

Today: a non-JSON response from the security persona fails the phase (`return evaluation.Record{}, fmt.Errorf("parsing security verdict: %w", err)`) and the job transitions to `failed`.

After this ADR: the same response still fails the phase. The wire-level `format: "json"` reduces the *probability* of a non-JSON response to near zero (in the M1-T8 probe the gemma4 model emitted clean JSON in 5/5 cases; the failure mode today is code fences and trailing newlines, both of which `format=json` eliminates). The fallback path is the same logic; the change is in the *common* path, which goes from "scan then unmarshal" to "unmarshal only."

### D5. Wire-compat with Ollama versions

Ollama's `format` parameter was added in 0.3.0 (2024). Every supported release since accepts it. The `Format` field is `omitempty` — older Ollama versions that ignore the field see the same request they did before. A pre-0.3.0 Ollama with a model that emits clean JSON today still parses; a model that wraps its JSON in prose will fall through to the brace-scan fallback. No regression.

### D6. The 3rd wire knob

`chatRequest` now has five fields: `Model`, `Messages`, `Stream`, `Format`, `Options`. The `Format` field is empty for every non-evaluating, non-comparing call. The `Format` field is set to `"json"` exactly for the two judgment phases — `evaluating` and `comparing` — both of which run on the `security` persona at T=0.0. No other phase needs it.

## Consequences

- The probability of a verdict-parse failure drops from "model-dependent" to "Ollama version < 0.3.0 or model family that ignores format." The M1-T8 probe's 5/5 clean-JSON rate is the lower bound for the *post-ADR* rate on `gemma4:12b-mlx`.
- The two near-clone parsers collapse into one. The M3-T1 close-out polish item (winner-string normalization) becomes a one-line check after the helper returns, not a per-parser concern.
- The `format: "json"` setting does not change the `security` persona's Temperature 0.0. The deterministic-judgment invariant is unaffected; the JSON constraint is orthogonal to the temperature.
- The CLI, the in-tree spike, and the engine tests are unchanged at the wire level. The Ollama `fakeOllama` in `internal/llm/client_test.go` ignores `format` (the test handler echoes whatever it received), so the existing tests pass.
- A new test corpus of wrapped vs. clean responses exercises both paths. The pre-existing lenient-wrapping tolerance is preserved as the fallback, not removed.
- `internal/llm` does not gain a dependency; `encoding/json` is already imported. No new Go module changes; honors the AGENTS.md "two deps" policy.

## Not in M3

- A model-side grammar definition (Ollama's `format` parameter is the simplest knob; a full GBNF grammar is post-M7).
- Strict verification that every other phase produces prose (not just trusting the prompts). M3-T7's quality probe #2 will measure this empirically; if any non-judgment phase wanders into JSON, that's a prompt-fix follow-up.
- An automatic retry on parse failure. Today a parse failure is a hard error; an automatic retry would compound the cost and is not justified by the post-ADR failure rate.

## Forward references

- M3-T2 (Evaluation phase) lands this ADR's wire change in the same commit as the §19 evaluation rubric; the parser consolidation lands in a follow-up commit because it's a refactor, not a behavior change.
- M3-T7 (quality probe #2) measures the post-ADR parse-failure rate on ~10 tasks across the 27B/9B model set.

## Implementation status (M3-T7.1, 2026-10-05)

D2 (parser consolidation) landed with M3-T2 (`internal/engine/parse_verdict.go`).
The D1 wire change — `Format` on `llm.Request`/`chatRequest`, set to `"json"`
for `evaluating` and `comparing` — was **not** landed at M3-T2 and is
implemented here, together with the M3-T7.1 seed knob:

- `Inference.JSONFormat` (`json_format`, default true) gates the wire
  change, so the M3-T7 probe can run a controlled format ablation.
- `Inference.JudgmentSeed` (`judgment_seed`, default `"off"`) may pin a
  derived seed on Temperature-0 calls; the resolved seed and format are
  recorded in every `llm_call` audit row. Divergence is never seeded
  (a shared seed would collapse its candidates).
- `llm.Request` gains `Seed *int64` and `Format string`.

The brace-scan fallback (D3) is unchanged and remains the defense for
models or Ollama versions that ignore `format`.
