# ADR 0081 — A code project's tasks carry the execution tools by default

**Status:** Accepted · **Date:** 2026-10-24 · **Refs:**
ARCHITECTURE §6.2 (code archetype), §25 (tool interface), §21.2 (pod
hardening); [ADR-0009](0009-engine-pod-wiring.md) (tool envelope),
[ADR-0024](0024-engine-pod-dispatch.md) (pod dispatch),
[ADR-0058](0058-verification-gates.md) (tests-required acceptance),
[ADR-0080](0080-engine-executed-tools.md) (engine-executed tools).

## Context

The daemon-wide default is `job_pod.default_tools: []`. That is the right
containment default: no code, network, or memory operation runs unless the
operator opts in.

But a `code` project needs `execute_code` (stage the candidate),
`run_tests` (run the project's real test command), and `lint`. With the empty
default and `execution.require_tests_for_code: true`, a code project created by
the documented flow could never accept a candidate: the test route answers 403,
the engine treats it as a soft skip, and the acceptance gate rejects every
candidate. Nothing said the operator had to grant the tools.

## Decision

1. **A `code` project's tasks get `execute_code`, `run_tests`, and `lint` as a
   per-task `allowed_tools_json` default**, set when the task is created
   (`project.Create`, `goal submit`, and `CreateDAG`).
2. **The daemon-wide default stays `[]`.** Other archetypes remain tool-free
   unless the operator grants tools. Network tools (`fetch_url`,
   `search_web`) and context/memory tools (`context_swap`, `query_memory`)
   remain explicit per-task decisions.
3. **A task-level override still wins.** `EnvelopeFor` is unchanged; an
   operator who wants a different set edits the task/config.
4. **Containment is unchanged.** These three tools already run inside the
   hardened Job Pod; granting them to a code task does not widen the pod's
   capabilities, network, or mounts.

## Consequences

- The documented first-use flow works: a default `code` project runs its real
  test command and can be accepted.
- The safe daemon default is preserved for every non-code project; no global
  network/tool opening.
- `CreateDAG` tasks inherit the same default, so decomposed code work is not
  silently tool-less.
- The prompts now list the operations the engine will actually run, which makes
  the ADR-0080 wording accurate.

## Alternatives rejected

- **Set `job_pod.default_tools` globally for everyone.** Widens every project
  (including text/research) and hides the decision in config.
- **Keep empty and document a manual step.** Leaves the flagship archetype
  broken by default; the first-use doc already had to warn about it.
- **Make `require_tests_for_code` false by default.** Weakens a deterministic
  acceptance gate to work around a configuration gap.
