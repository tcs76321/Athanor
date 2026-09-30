# ADR 0024 — Engine ↔ Job Pod execution dispatch (completes M2-T4)

**Status:** Accepted · **Date:** 2026-09-30 · **Refs:** ARCHITECTURE §21.2, §21.3, §23.6, §25, §31.2; ROADMAP M2-T4, M2-T4b; ADR-0007 (rootless Podman lifecycle), ADR-0008 (per-job tokens), ADR-0009 (engine drives; pod is the worker), ADR-0014 (evaluation-phase move), ADR-0019 (dependency inversion), ADR-0021 (Core-executed `context_swap`).

## Context

The ROADMAP marks M2-T4 done with the acceptance criterion *"Disallowed tools rejected and logged; results streamed back with exit codes/output."* Two of those clauses are real; two are not. Verified in-tree on 2026-09-30:

- `internal/internalapi/exec.go` — `handleExecuteCode`, `handleRunTests`, and `handleLint` parse the body, enforce the per-job envelope through `a.tools.EnvelopeFor`, append the audit rows, and then return **501 Not Implemented**. The dispatch was deferred as "M2-T4 commit 4" and never landed; `internal/internalapi/exec_test.go` (`TestExecuteCode_PassesEnvelopeCheck_Returns501` and the `run_tests` mirror) asserts the stub.
- `cmd/athanor/serve.go` constructs `podMgr` and calls only `Sweep`. **No production code calls `jobpod.Manager.Start`**, so no Job Pod is ever created.
- Consequence: for a `code`-archetype job, `engine.evaluateCandidate` → `runCodeInPod` → `runner.RunCode` → `HTTPClient.post` → `TokenFor(jobID)` returns `jobpod.ErrNotFound` (the manager's map is empty), and the `evaluating` phase fails.
- `docs/demo-m2.md` already records that the Gate G2 pytest clause is "a capability statement about the M2-T4 internal API surface, not a T6 deliverable" and was "owned by M3-T2"; M3-T2 shipped the `lint` route (also 501) but not the dispatch. M3-T1/T2 moved the pod sub-steps into `evaluating` (ADR-0014) without adding a pod lifecycle.

Two facts the dispatch must reconcile:

1. **`--network=none` vs. a pod that calls the daemon.** `internal/jobpod/args_common.go` hardcodes `--network none`, so a Job Pod cannot reach the daemon's loopback internal API. M2-T3/ADR-0008 describe the pod as the internal API's client; that direction is not usable under the §21.2 default network policy. ADR-0009 D1 already answers the resulting question — *"The engine is the orchestrator… The pod is the worker: it receives the call, runs the code, and returns {exit_code, stdout, stderr, duration_ms}. The pod never pulls work on its own."* — so execution is a **push** from the Core into the pod, not a pull.
2. **The engine already authenticates as the job.** ADR-0009 D5 makes the engine "just another client of the loopback internal API," and `internal/internalapi/runner/httpclient.go` retrieves the per-job bearer token via `TokenFor` on every call. That plumbing presupposes a **live pod** (a token exists only while the pod does); a model with no live pod leaves the runner nothing to present.

## Decision

### 1. Execution model — the Core pushes work into the job's pod (D1)

Tool execution is host→pod: the daemon runs the requested command *inside* the job's container; the pod never calls out. This is the only model compatible with `--network=none`, and it is ADR-0009 D1's stated shape. The `pod → /internal/v1/` direction (job context, heartbeat, log) remains a declared surface for a future restricted-internal network; it is not a dependency of M2-T4b.

### 2. Pod lifetime — one pod per job, `podman exec` per tool call (D2)

A Job Pod is started when a job first needs one, lives until the job reaches a terminal state, then is stopped. Tool calls run as `podman exec` against that running pod. Rationale:

- It matches the existing token/runner plumbing (Context fact 2): the pod's token exists, so `TokenFor` succeeds and the runner authenticates the hop.
- §21.2/§21 "one per execution, destroyed after" — a job is an execution.
- The M2-T5 supervisor, teardown, and startup `Sweep` already target per-job containers, so this reuses proven machinery.

A **one-shot `podman run` per call** was considered and rejected for M2-T4b: it removes the live pod that the runner's `TokenFor` depends on, forcing a redesign of (or a second credential for) the engine→API hop, and it breaks the "install once, run many" semantics a test run needs (a `run_tests` call is expected to see what `execute_code` just produced). The one-shot form stays available as a future hardening option once the credential question has an answer.

### 3. Delivery — code on stdin; commands are closed defaults (D3)

- `execute_code`: the source is delivered to `podman exec -i <pod> python -` on **stdin**, so the code never appears in argv or the process table. `internal/jobpod.Client` gains an optional stdin channel to support this.
- `run_tests` / `lint`: the command is an argv token from a **closed default set** (`pytest -q`, `ruff check .`); the M2-T4 handlers already validate the shape. A per-task command override is a separate decision.

### 4. Hop topology — keep the engine → internal API hop (D4)

The engine continues to call the daemon's own `/internal/v1/jobs/{id}/{execute_code,run_tests,lint}` routes through `runner.HTTPClient`. The handler remains the single enforcement point for the envelope (Gate G2). The 501s are replaced by dispatch through a new **`PodExecutor`** interface owned by `internalapi`; the production adapter lives in `cmd/athanor` over `jobpod` (the ADR-0019 inversion already used for `ToolGateway`, `ContextSwapper`, `TokenStore`, and `ToolEnvLookup`). `internalapi` still imports neither `internal/jobpod` nor `internal/llm`.

### 5. Token semantics (D5)

The per-job token authenticates the engine's hop to the internal API, exactly as ADR-0009 D5 specified. Because a `--network=none` pod cannot present it, the token's *pod→daemon* use is dormant; this ADR records that honestly rather than pretending the M2-T3 diagram is live. The token's generation, mount, and revocation are unchanged.

### 6. Image contract — enforce the fail-fast the docs already claim (D6)

`internal/config/config.go` and `config.example.yaml` both state that `serve.go` fails fast when `job_pod.image` is empty; it does not. M2-T4b.4 adds the check — a daemon that cannot name a Job Pod image cannot dispatch. The base image must provide a POSIX shell, `sleep`, and the language toolchain the envelope admits; building/packaging that image is M7-T7, and the plan records it as an operator precondition.

**Implemented (M2-T4b.4), with one correction.** A literal boot-time fail-fast would break the shipped M1-T7 acceptance ("fresh clone → running daemon"): the built-in defaults leave `job_pod.image` empty, and `TestExampleConfigMatchesDefaults` pins the example to those defaults, so an unconditional refusal would make a config-less daemon unbootable. The delivered behavior is therefore a boot **warning** plus a dispatch-time refusal (`internalapi.ErrExecNotConfigured` → 503): a daemon that only runs LLM phases still boots, and a tool call with no image fails loudly with an actionable message. The `config.go` and `config.example.yaml` comments were corrected to match.

### 7. Gate coverage (D7)

- **Gate G1** — unchanged: the only `os/exec` remains `cmd/athanor/jobpod_client.go`; `podman exec` rides the existing `jobpod.Client`.
- **Gate G2** — preserved: `internalapi` gains no `internal/llm` import; every `/internal/v1/` route still goes through `authMiddleware`; each exec handler still references `a.tools.EnvelopeFor`. The `podman exec` argv is built in `internal/jobpod` and covered by a new arm of `TestGateG2JobPodArgvCannotEscape`, so an exec-time escape (host bind-mount, `--privileged`, `--network=host`) is a build break.
- **Behavioral** — an `ATHANOR_RUN_INTEGRATION=1` probe runs `execute_code` + `run_tests` end-to-end against a real rootless pod.

## Consequences

**Positive**

- The §25 tool loop is finally live: a `code`-archetype job runs its candidates in a hardened pod and the exit code reaches the verdict.
- One dispatch seam, one auth check, one envelope check — the containment story stays single-point.
- Recovery becomes coherent: §23.6 resume re-ensures the pod before the code sub-steps instead of failing on `ErrNotFound`.

**Negative / accepted**

- A pod now lives for the duration of a job rather than only during a call, so a long job holds one container open. Bounded by M2-T5 teardown and the per-job resource limits.
- `execute_code` and `run_tests` are serialized through one live pod; parallel multi-candidate pod execution (an M3 idea) needs more than one pod per job and is out of scope.
- The ROADMAP's M2-T4 "Done" claim is corrected: it was an incomplete end state until M2-T4b lands.

## Not in M2-T4b

- Pod→daemon callbacks / a restricted-internal network mode (the token's dormant use).
- Parallel / multi-pod execution per job.
- The Job Pod base image build and packaging (M7-T7) and the `athanor doctor` image/model checks (M7-T5).
- A per-task command override for `run_tests` / `lint`.

## Implemented (M2-T4b.2–T4b.5)

`internal/jobpod` gained `Exec` (one `podman exec` per tool call, argv in
`args_exec.go`, code on stdin via `Client.RunStdin`; a non-zero command
exit is an `ExecResult`, not an error). `internal/internalapi` replaced
its three 501 stubs with a `PodExecutor` dispatch and typed errors
(`ErrNoPod`→404, `ErrPodNotRunning`→409, `ErrExecNotConfigured`→503).
`cmd/athanor` holds the two adapters (`podExecutorAdapter`,
`podLifecycleAdapter`) over `jobpod.Manager`, and `internal/engine` gained
the nilable `PodLifecycle` seam that ensures the pod before the `code`
sub-steps and stops it at terminal state. Gate G2 gained the exec-argv arm
`TestGateG2ExecArgvCannotEscape`.

## Implementation note

`docs/m2-t4b-plan.md` is the execution plan; this ADR is the decision record. If implementation reality disagrees, the plan changes first and this ADR gains an "Implemented" note (the ADR-0021/0022/0023 pattern).
