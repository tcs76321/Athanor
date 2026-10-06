# ADR 0058 — Code acceptance gates: require_tests_for_code and require_documentation_for_code

**Status:** Accepted — lands with F5 · **Date:** 2026-10-06 · **Refs:**
ARCHITECTURE §6.2, §9.3, §13.1, §14, §19, §29; ROADMAP F5;
`internal/verify`, `internal/engine/verify.go`

## Context

Three `execution` flags were declared and defaulted to `true` but never
consulted, and their comments claimed they would "become effective in
M6/M7":

- `require_tests_for_code`
- `require_documentation_for_code`
- `compare_before_accept`

M6 and M7 shipped; the flags remained inert. That is a truthfulness defect:
an operator who set one to `false` saw no behavior change, and one who left
it `true` got none of the guarantee the name promises. Separately, F4 made
deterministic verification the load-bearing acceptance signal (ADR-0045), so
the verifier registry is the natural home for code gates.

## Decision

**Implement the two code gates as decisive verifiers (F5).**

- `require_tests_for_code` (default true): in `internal/verify`, a
  code-archetype candidate with no test run is a **hard failure** when the
  flag is set, instead of falling through to the LLM judge. A test run that
  happened and failed was already a hard failure (F4-T0a); this closes the
  "no run at all" gap.
- `require_documentation_for_code` (default true): a new `docsVerifier`
  applies to code candidates and is decisive when the flag is set. It is a
  **presence** check, not a quality judgment: it passes when the content
  contains a recognized documentation construct — a docstring (a line
  beginning with `"""`/`'''` and a body of ≥ 8 non-space characters), a
  comment block (`#`, `//`, `///`, `/*`, `*`, `--`, `;`, `%`) at the top of
  the file (≥ 2 lines with real text) or immediately above a declaration
  keyword, or a markdown `## ` section.
- The engine resolves `execution.require_*_for_code` into `verify.Input` in
  `verifyCandidate`, so both `phaseEvaluate` and `phaseCompare` enforce the
  gates through the existing `HardDecisive()`/reward-hacking logic. No new
  acceptance path is introduced.
- When a gate is false, its verifier is absent (tests) or non-applied (docs),
  and behavior is exactly pre-F5.

**Retire `compare_before_accept`.**

ARCHITECTURE §8.2 makes `synthesizing → comparing` mandatory, and Gate G3
("no artifact is accepted without passing deterministic evaluation and
comparison") turns that into a security invariant. A flag that disables
comparison cannot be honored. The field is removed from `Execution`,
`defaults.go`, `config.example.yaml`, and the ARCHITECTURE §29 reference.
Config parsing is strict (`KnownFields(true)`), so a pre-F5 config that sets
`compare_before_accept` fails loudly at load rather than being silently
ignored. This is a deliberate breaking change under the pre-MVP
tip-of-main support policy.

## Consequences

- Code projects that previously accepted an untested or undocumented
  candidate now fail the gate and route through reflection; if no candidate
  passes, the job fails (or escalates per §7.2). This is the flag's intent;
  operators who do not want it set the flag `false`.
- The gates reuse the F4 verifier machinery, so the reward-hacking guard
  (ADR-0046) already protects them: an LLM `new` cannot override a failed
  gate.
- The documentation check can under-detect an exotic house style; it is a
  documented, small recognized set, and the flag can be disabled without a
  code change.
- Existing engine tests that used a generic fake model response disable the
  gates in the test env (`newEnvWithCfg`); the gate behavior is covered by
  dedicated tests (`internal/verify/docs_test.go`,
  `internal/engine/verification_gates_test.go`).

## Alternatives rejected

- **Keep the flags as documented no-ops.** That is the bug this ADR fixes.
- **Delete all three flags.** The two code gates are genuinely useful and
  now implementable; only `compare_before_accept` is impossible.
- **A heuristic documentation *quality* judge.** Non-deterministic and
  overlapping the LLM judge; the gate is deliberately a presence check, and
  quality remains the judge's job.
- **A deprecation window that warns but still ignores a set flag.** The
  project is pre-MVP and supports only tip-of-main; a loud load error is
  simpler and more honest than a silent warning.

## Not in scope

- A documentation *artifact* step in synthesis (a separate `document` output
  per §14). This gate is a content-presence check on the code candidate.
- Data/media archetype verifiers (still deferred, ROADMAP §7).
