# ADR 0113 — Read-only observation tool for repair

**Status:** Accepted · **Date:** 2026-11-06 · **Refs:**
ARCHITECTURE §25; ADR-0080 (engine-executed tools), ADR-0082 (repository
working set), ADR-0112 (clarification); `internal/toolenvelope`,
`internal/internalapi`, `internal/engine`, `cmd/athanor`

## Context

ADR-0080 deliberately gave the engine, not the model, control over tools:
the model cannot emit a tool call, so it cannot hallucinate one or widen the
containment surface. That was the right containment decision, but it caps the
repair loop. When a test fails, the model gets the failure tail and the
candidate bytes; it cannot inspect the repository file the failure points at.
The engine already has the repository snapshot (`/workspace/repo`, ADR-0082)
and a bounded self-refine loop. The missing piece is a **read-only
observation** the model can request.

## Decision

Add a **closed, read-only `read_file` tool** and one bounded observation step
in self-refine.

- **Tool contract.** `read_file(path, max_bytes)` is a new member of the §25
  closed set. Path is relative to `/workspace/repo`; absolute paths, `..`
  traversal, NUL bytes, and non-repo roots are rejected. The executor runs
  `cat -- <resolved>` as a direct argv (no shell), so a path cannot inject a
  command. The response is capped (8 KiB default, 16 KiB server ceiling) and
  reports truncation.
- **Envelope.** The route is `/internal/v1/jobs/{id}/read_file`, wrapped in the
  existing auth middleware and the per-job tool envelope. A code task's
  default tools now include `read_file`; a non-code task and the daemon-wide
  default do not.
- **One request, not a loop.** After a failed evaluation, before the self-refine
  rewrite, the model may answer one JSON question:
  `{"read_file":"relative/path"}` or `{"read_file":null}`. The engine executes
  at most one read, writes the content to the job notebook as an
  `observation`, and then performs the ordinary rewrite. There is no
  model-driven multi-step tool loop, no write, no execute, and no network.
- **Best-effort.** A malformed request, a disallowed path, or a read failure is
  audited and ignored; it never fails the job.
- **Opt-out.** `execution.policy.observation_tools` defaults to true and can
  be disabled; the engine then behaves exactly as before.

## Consequences

- The repair loop gains the one observation that matters most: the model can
  see the file a test failure points at, instead of guessing from a 2 KB
  stderr tail.
- Containment is unchanged: the pod is still `--network=none`, the repo mount
  is read-only, the path is engine-validated, and the tool cannot write or
  execute. The engine still decides when the tool runs.
- The tool is a template for a later, larger observation loop (read a second
  file, list a directory, inspect a diff) and for HITL-gated write tools. It
  does not introduce those yet.
- Cost: one extra LLM call per self-refine attempt when enabled. The call is
  bounded by the refining phase budget and the opt-out.

## Alternatives rejected

- **A general model-driven tool loop now.** It is the larger unlock, but it
  needs a tool-call protocol, per-action budget, prompt-injection handling, and
  a much wider envelope review. This ADR ships the smallest observation that
  closes a real repair gap.
- **Let the model read arbitrary host paths.** It would break §3.1 and §21.3.
  The repo snapshot is the only approved source.
- **Use `sh -c cat "$path"`.** A shell would make a model-controlled path a
  command-injection surface. Direct argv is the containment.
