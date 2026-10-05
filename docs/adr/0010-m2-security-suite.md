# ADR 0010 — M2-T6 security test suite: argv regression test + behavioral probes

**Status:** Accepted · **Date:** 2026-08-30 · **Refs:** ROADMAP M2-T6;
ARCHITECTURE §21, §31.2; ADR-0007, ADR-0008, ADR-0009

## Context

M2 (Container Spine) is complete except for the security test
suite. The five M2 tasks that landed before T6 (T1 spike, T2
manager, T3 per-job tokens, T4 internal API + tool envelope, T5
strict teardown + orphan cleanup) together produce a Job Pod with
the §21.2 hardening flags. The M2 exit gate (Gate G2) currently
consists of structural tests in `internal/gate/gate_g2_test.go`:
text-search assertions that the internal API does not import
`internal/llm`, that token comparison is constant-time, and that
every `/internal/v1/` route is wrapped in `authMiddleware`. Those
tests prove the *source* has the right shape; they do not prove
the *runtime* denies what it is supposed to deny.

The ROADMAP M2-T6 line is the behavioral complement:

> Security test suite: pod→internet denied, pod→Ollama denied,
> pod→podman.sock denied, pod→host FS denied, credential absence
> verified. All attempts fail from inside the pod; failures logged
> as security events.

The implicit Gate G2 final rule (ROADMAP §3, line 71) is
behavioral:

> No code executes outside a hardened Job Pod; pods have no
> network, no Podman socket, no host FS beyond approved mounts.

A gate whose structural half exists and behavioral half does not is
incomplete: a refactor that accidentally dropped `--network none`
from the argv would pass every CI test today, then break Gate G2
silently the next time someone runs a pod. This ADR records the
design decisions for closing that gap.

## Decision

### D1. Two-layer suite, same as the rest of the project

T6 ships two layers that mirror the existing pattern (ADR-0008 D6
for the internal API, ADR-0009 D6 for the tool envelope):

1. **A structural argv test in `internal/gate/gate_g2_test.go`.**
   Runs on every `go test ./...` push. Asserts the joined-argv
   string from `buildArgs` (linux + darwin) does not contain any
   escape vector. Catches argv regressions in code review and
   blocks them in CI.
2. **Five behavioral probes in
   `internal/jobpod/security_test.go`** behind
   `//go:build integration`. Opt-in via
   `ATHANOR_RUN_INTEGRATION=1`, mirroring the existing
   `make test-integration` target. Brings up a real pod, runs a
   probe script, asserts the probe fails. Catches runtime
   regressions that the structural test cannot (a `--network none`
   typo that is structurally correct but rejected by podman, for
   example).

The structural test is the durable CI backstop; the integration
suite is the human-run proof. Both must be present for Gate G2 to
be complete.

### D2. Build the argv the same way the manager does

The structural test calls `buildArgs` directly with a representative
`Spec` (image, command, tokenDir). It does not shell out to
`podman` and does not require any external tooling. The behavioral
probes also use `buildArgs` output, then hand it to a one-shot
`podman run --rm` that exits with the probe's output captured.
This keeps the test in lock-step with production: a refactor that
changes the argv is reflected in both layers automatically.

### D3. Probe scripts are `sh -c` strings, not Go binaries

The behavioral probes use `sh -c` because `alpine:3.20` ships
with `/bin/sh` and `wget` is preinstalled in that image (the
spike's choice, ADR-0007). The probe's inside-pod command is a
single string the test passes via `podman run --rm`; the test
asserts on the captured stdout/stderr. No container images need
to be built; no scratch images need to be pushed; no Go test
binary needs to be cross-compiled. The probes run in seconds on
the same `alpine:3.20` image the production manager uses.

### D4. Test results are recorded in the audit log regardless of outcome

Every probe calls `store.AppendEvent` with category `podman`
(see D5) before asserting. A failing probe produces the audit
event first, then fails the test. The acceptance criterion
"failures logged as security events" is satisfied: the
`events` table has a row for every probe, pass or fail, with the
probe name, the observed output, and the verdict. The test
harness uses an in-memory SQLite store (per
`internal/store/store_test.go`'s existing pattern) so the
integration suite does not require a configured `state/`
directory.

### D5. Use the existing `podman` category, do not extend the closed set

The closed set of event-log categories in
`internal/config/load.go` (line 20, §28.1) does not contain
`security`. The T6 events could either:

- (a) widen the closed set to add `security`, requiring changes to
  `config.Categories`, `config.example.yaml`, the validator, the
  default category list, and the logging tests; or
- (b) co-opt an existing category whose semantics already cover the
  pod surface.

T6 uses (b): category `podman`. The events the suite emits are
literally about the pod surface (argv, mount flags, probe output),
and `podman` is already the category the manager and the spike use
for podman-related events. The "security events" wording in the
ROADMAP refers to *significance*, not to a literal `security`
column; an event under category `podman` with a
`probe: "internet"` data field and a `result: "fail"` flag is a
security event for any human reader.

If a future need arises to filter the audit log by "security
events" generically (e.g. for M7-T3 alarms), a view or a
sub-category can be added without changing the schema. The events
table already supports the data field for that future
classification. T6 deliberately does not preempt that work.

### D6. The M2 Gate G2 pytest clause is owned by M3-T2, not T6

The Gate G2 line also reads "a pytest suite runs inside a pod
and returns results" (ROADMAP §3, gate table). That clause is a
capability statement about the M2-T4 internal API surface, not
a test-suite deliverable. M2-T4 already proves the
`run_tests` route works (the engine exercises it; the
`internalapi/exec_test.go` covers the handler). The "pytest
inside a pod" capability is owned by M3-T2 (Dialectical Loop:
"Evaluation phase: … test runs in Job Pod"), which exercises
the route end-to-end with a real test runner.

T6 records this ownership in the demo doc and the M2-boundary
plan review; it does not add the pytest test.

### D7. No new dependencies

The suite uses the existing `internal/store` event log,
`internal/jobpod` manager surface, and the `podman` CLI that
ADR-0007 already approved. No new go.mod entries. The
`alpine:3.20` image is the same one the spike validated.
Per AGENTS.md, "Adding a dependency is a project decision, not
an agent decision"; this task is consistent with that policy
because it adds none.

## Consequences

- The structural test is the CI-enforced backstop for argv
  containment. The integration suite is the human-run proof.
  Together they form Gate G2's complete coverage.
- A future M2 refactor that accidentally widens the argv (e.g.
  adds `--net=slirp4netns` to debug a networking issue, forgets
  to remove it) fails the structural test in CI and never reaches
  `main`. The integration suite catches any argv that *looks*
  safe but is rejected at the podman level.
- The `events` table gains five new categories of records during
  `make test-integration`. These are not noise: each one records
  a probe name, the inside-pod script, the observed output, and
  a pass/fail flag. A future operator inspecting the audit log
  can see "the security suite ran, every probe passed" (or, if
  it didn't, which probe failed and why).
- The `podman` category is now used for two event types: manager
  lifecycle events and M2-T6 security probes. This is acceptable
  for now (both are "things that happened at the podman
  surface") but a future ADR may split the category if the event
  volume warrants it. T6 does not preempt that decision.
- The five behavioral probes run sequentially under
  `make test-integration`. Total wall time budget: under 60
  seconds on a warm podman-machine (each probe is a single
  `podman run --rm` against `alpine:3.20` with no network
  setup). If a probe runs longer than 10 seconds the harness
  cancels the pod and fails the probe.

## Not in M2-T6

- Linux seccomp enforcement probing. ADR-0007 documents that
  rootless macOS/podman-machine has no kernel seccomp; the
  integration suite runs on whatever the developer has. The
  structural argv test asserts the `--security-opt
  seccomp=/usr/share/containers/seccomp.json` flag is present on
  Linux; the macOS argv test asserts it is absent (matching
  `args_darwin.go`'s documented no-op).
- Host-kernel escape probing. The pod is rootless and has no
  privilege to attempt kernel CVEs; the assumption is that the
  host kernel is patched (operator responsibility per §21.1).
- A "security" event-log category. D5 explains why.
- The pytest-in-a-pod Gate G2 clause. D6 explains why.
