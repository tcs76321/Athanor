# F4 — micro-run results

The F4 close-out re-runs the M3-T7 micro set (`make probe-micro`: 3 goals ×
both arms) after the measurement repair and verification changes. This file
records run 1 (pre-correction) and run 2 (post-correction).

Environment: Apple M2 Max, 32 GB; Ollama 0.35.1; generator `ornith-1.5:9b`
(`e5df7dcdd8a2`); judges `gemma4:12b-mlx`, `granite4.2:3b`.

## Run 1 — 2026-10-05 (`results/f4micro`, pre-correction)

| goal | archetype | single | dialectical | dialectical diversity |
|---|---|---|---|---|
| local-first-essay | text | failed 0.00 | failed 0.00 (3/3) | 0.81–0.88 |
| md2html-readme | document | completed 1.00 | completed 1.00 (3/3) | 0.69–0.79 |
| todo-list | code | failed 0.00 | failed 0.00 (3/3) | 0.68–0.86 |

Mean dialectical diversity **0.781** (single 0.000). 0 orphan pods.

### Root causes (from the state DB events)

- **`todo-list` (code)** — every candidate failed the *real* test with
  `ModuleNotFoundError`: the generator emitted the module as prose inside
  markdown fences, so writing it to `/tmp/solution.py` produced invalid
  Python. This is the previously-masked code signal finally appearing — a
  packaging gap, not a logic defect. Reflection correctly diagnosed it twice
  and then exhausted its budget.
- **`local-first-essay` (text)** — the decisive *structural* verifier failed
  every candidate on `paragraph count N != 3`. It counted headings/titles as
  paragraphs (e.g. `4 != 3` for a title + three paragraphs), and the task's
  "exactly three paragraphs … a one-sentence conclusion" is itself ambiguous
  about whether the conclusion is a fourth paragraph.
- **`md2html-readme` (document)** — the section-name structural check passed
  and the job completed; the existing rubric still scores it 1.00 (no
  discrimination).

## Corrections applied after run 1

1. `internal/verify`: `Verdict.Hard` — only objective verifiers (code
   tests/lint) decide acceptance or trigger the reward-hacking guard; the
   text `structure` parser advises via the prompt but does not override the
   LLM judge. ([ADR-0045](../adr/0045-verification-first-selection.md)
   addendum.)
2. `countParagraphs` ignores heading-only blocks (a title is not a
   paragraph).
3. The `code` archetype's divergence and synthesis prompts require the raw
   module (`codeOnlyInstruction`), so the pod imports real code.

## Run 2 — 2026-10-05 (`results/f4micro2`, post-correction)

_Pending: this run is executing._

## What the run does and does not establish

- **Does**: the code arm now produces a real pass/fail signal (the instrument
  is repaired); diversity is real (~0.78); the loop reflects on and diagnoses
  its own packaging failure.
- **Does not**: establish that the LLM judge can rank (the anchor run shows it
  saturates), nor that code acceptance is achievable without the prompt fix —
  which run 2 tests.
