# ADR 0092 — Benchmark labels are test outcomes, not engine accept

**Status:** Accepted — implemented · **Date:** 2026-10-29 ·
**Refs:** [ADR-0075](0075-frozen-corpus-compass.md) (frozen benchmark),
[ADR-0073](0073-soak-validation-harness.md) (soak harness),
[ADR-0083](0083-repository-working-set-pack.md) (working-set pack);
`tools/bench/report.go`, `tools/bench/collect.go`, `tools/bench/runner.go`;
`docs/probes/bench-v2-findings.md`, `docs/probes/bench-xcode-p0.md`.

## Context

ADR-0075 §4 requires the headline to use the **frozen external** checks/tests,
never the engine's own verifier or judge. The v2 implementation did not meet
that rule for code tasks. `jobPassed` fell back to engine acceptance whenever a
row had no `checks` block, and code tasks have no `checks` block:

```go
return m.State == "completed" && m.Winner == "new"
```

The consequence showed up in the 2026-10-25 full v2 run. Its headline was
30/41 (73%), with X-code at 0/6. Those six rows are six **engine failures**, not
six recorded test failures. The results contain no per-candidate test outcome,
so a reader cannot tell whether a candidate failed its frozen test or passed it
and the engine failed later. Three of the six rows had an evaluation score of
1.0, but that is the LLM rubric score, not the test result (`evaluate.go`
overrides `PassedTests` for a hard verifier but not `Score`).

A focused 2026-10-29 X-code diagnostic then produced **4/6 on the host dev
runtime and 3/6 on the Core Pod** after the P0 working-set, test-command,
module-name, scratch-mount, and synthesis-fallback fixes. The frozen v2 baseline
and the current tree therefore disagreed, and the docs repeated the stale
number as if it were current.

Two smaller measurement defects compounded the ambiguity:

1. Non-code checks scored the **accepted** artifact. The best-candidate fallback
   ran only when the engine accepted nothing, so a rejected candidate that
   passed the checks could be hidden.
2. Two v2 data tasks declared their `json_schema` under `eval/bench/fixtures`
   (v1). The v2 digest pins the v2 copies, but the runtime read the v1 path, so
   an edit to v1 could change a v2 run without changing the v2 digest.

The engine already records the missing evidence. `evaluateCandidate` emits a
`tests_ran` event per code candidate with `passed` and `command`
(`internal/engine/evaluate.go`), and `evaluation_records.passed_tests` carries
the hard-verifier result. The probe simply did not use either.

## Decision

1. **A code row is labeled by the recorded frozen-test outcome.** The probe
   reads the job's `tests_ran` events. `code_test_passed` is true when any
   candidate's frozen test passed; `code_test_total` / `code_test_passed_count`
   preserve the candidate-level evidence. `jobPassed` uses that label for code
   rows.
2. **Engine acceptance is reported separately.** `engine_accepted` is
   `completed && winner == new`; it never determines the benchmark label. A
   test-passing candidate the engine rejected is a system decision, not a
   capability failure, and is surfaced in the decision-diagnostics table.
3. **Legacy rows keep the old label, visibly; new rows do not.** A row without
   a `tests_ran` event and without `label_version` falls back to engine
   acceptance, so historical `results.json` still re-renders. Rows collected
   under `label_version: adr-0092` with no test event are labeled
   `code_test_missing` and fail the code label — a missing test is not an
   engine accept. The report counts legacy accepts and missing tests in
   separate columns instead of silently mixing them with test-labeled rows.
4. **Non-code checks always score the best candidate.** The engine's accepted
   artifact is kept for judge packets; the deterministic checks run on
   `bestCandidateArtifact`, the highest-scoring proposal. This is the ADR-0075
   §4 rule applied to the artifact the system actually produced, not only to
   the artifact it accepted.
5. **Corpus fixture and schema paths resolve version-locally.** When a declared
   path is a sibling under `eval/bench/` and a copy exists under the corpus
   version root, the loader rewrites it to that copy. The frozen YAML and its
   digest are unchanged; v2 now reads its own pinned schemas instead of v1's.
   v3 must correct the two v2 declarations in the YAML.
6. **The reports say what they mean.** The headline and leaderboard describe
   code rows as "recorded code test" and show engine accept separately. The
   diagnostics section is titled "benchmark check/test passed but not
   accepted". The docs reserve "external" for the non-code checks; a code test
   executed by the system under test is a recorded frozen-test outcome, not yet
   an independent re-run.

## Consequences

- A code row can now pass with `state=failed` (test passed, engine did not
  accept) or fail with `state=completed` (engine accepted, recorded test
  failed). Both are informative and both are visible.
- The 2026-10-25 headline remains a historical baseline. It must not be quoted
  as the current capability number; a full post-P0 rerun replaces it.
- The label is still produced by the engine's Job Pod. The next integrity step
  is a probe-owned, disposable-pod scorer that re-runs the frozen `test_command`
  on the best candidate, so the code label no longer depends on the system
  under test at all. That is a separate capability (and a containment decision),
  not a prerequisite for removing the accept/test conflation here.
- The v2 schema path erratum is documented in `docs/frozen-corpus-v2.md`; v3
  corrects the YAML. No task text, check, or fixture content changes.
