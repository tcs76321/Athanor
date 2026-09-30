# M2 Container Spine — Demo Script (Gate G2)

This is the executable proof for **Gate G2**: code that the agent
runs lives only inside a hardened Job Pod, and the pod has no
usable path to the network, the podman socket, the host
filesystem, or any credentials.

M2 consists of six tasks (T1–T6). This document covers T6 (the
security test suite) and the evidence behind Gate G2. The first
five tasks (rootless podman spike, Job Pod manager, per-job
tokens, internal API with tool envelope, strict teardown) are
proved by the M2 implementation plus the existing structural
tests in `internal/gate/gate_g2_test.go`.

## What Gate G2 proves

| Property | Structural proof (CI) | Behavioral proof (local) |
|---|---|---|
| Internal API depends on no LLM client | `TestGateG2NoLLMDependency` | — |
| Bearer-token compare is constant-time | `TestGateG2ConstantTimeComparePresent` | `TestAuthMiddleware_*` in `internal/internalapi` |
| Every `/internal/v1/` route is wrapped in `authMiddleware` | `TestGateG2InternalAPIRoutesGoThroughMiddleware` | `TestAuthMiddleware_WrappedOnEveryRoute` |
| Tool envelope cannot be bypassed in `execute_code` / `run_tests` | `TestGateG2ToolEnvelopeBypassImpossible` | `TestExecuteCode_*` in `internal/internalapi` |
| Job Pod argv cannot enable a pod escape | `TestGateG2JobPodArgvCannotEscape` (M2-T6) | `TestBuildArgs_PackageFlagsCannotBypassHardening` |
| Pod cannot reach the internet, Ollama, podman.sock, host FS, or secrets at runtime | — | `TestSecurity_*` (M2-T6, this document) |

The structural half runs in CI on every push. The behavioral
half is opt-in via `make test-integration` (a real `podman` is
required).

## Prerequisites

- Go 1.27+ with CGO (see [DEVELOPMENT.md](../DEVELOPMENT.md))
- A working rootless podman. macOS:
  [`podman-machine`](https://podman.io/docs/installation) on
  applehv. Linux: podman 4.x+ with cgroups v2.
- The `alpine:3.20` image (the spike's image; the suite pulls
  it on first run if it is not already present).

## The suite (≈30 seconds on a warm podman-machine)

```bash
# Build (paranoia: confirm the binary still compiles after a
# fresh clone)
make build

# Run the security suite. ATHANOR_RUN_INTEGRATION=1 is the
# gate; the Makefile target sets it for you.
make test-integration
```

Expected output (truncated):

```
=== RUN   TestSecurity_PodCannotReachInternet
    security_test.go:226: internet exit=1 elapsed=124ms err=exit status 1 stdout=wget_exit=1
--- PASS: TestSecurity_PodCannotReachInternet (0.14s)
=== RUN   TestSecurity_PodCannotReachOllama
    security_test.go:254: ollama exit=1 elapsed=5.137s err=exit status 1 stdout=mac=1
        lin=1
--- PASS: TestSecurity_PodCannotReachOllama (5.15s)
=== RUN   TestSecurity_PodCannotReachPodmanSocket
    security_test.go:274: podman_sock exit=1 elapsed=106ms err=exit status 1 stdout=sock_seen=NO
--- PASS: TestSecurity_PodCannotReachPodmanSocket (0.12s)
=== RUN   TestSecurity_PodCannotReadHostFS
    security_test.go:305: host_fs exit=1 elapsed=137ms err=exit status 1 stdout=touch_exit=1
        touch: /usr/share/probe: Read-only file system

        mount_grep=0
        overlay on / type overlay (ro,context=...,relatime,lowerdir=...)
--- PASS: TestSecurity_PodCannotReadHostFS (0.15s)
=== RUN   TestSecurity_PodHasNoCredentials
    security_test.go:344: credentials exit=1 elapsed=110ms err=exit status 1 stdout=grep_env=no_match
        athanor_mount=absent
--- PASS: TestSecurity_PodHasNoCredentials (0.12s)
PASS
ok  	github.com/tcs76321/athanor/internal/jobpod	7.015s
```

Each probe is one `podman run --rm` of an `alpine:3.20` container
with the §21.2 hardening flags. The probe script runs inside
the pod and exits 1 on its own (so a successful probe is a
non-zero podman exit code combined with the right stdout
markers — `wget_exit=1`, `mac=1 lin=1`, `sock_seen=NO`, etc.).

The numbers above are from the 2026-08-30 reference run on
macOS 14 / podman 5.8.2 / podman-machine applehv. Linux
behavior is expected to be similar; the seccomp caveat
(ADR-0007) means the macOS run does not exercise kernel
seccomp, and the `--security-opt seccomp=runtime/default` flag
on Linux is the one place the suite's evidence is platform-
asymmetric. This is documented in ADR-0007 and ADR-0010
("Not in M2-T6").

## What each probe proves

### 1. `TestSecurity_PodCannotReachInternet`

Inside the pod: `wget -q -T 2 -O - http://1.1.1.1/`. With
`--network=none` (set by `args_common.go` line 22) the
connection is refused; `wget` exits non-zero; the probe exits
1. The test asserts `wget_exit=1` appears in stdout. A green
result means the pod has no usable IP path to the public
internet.

### 2. `TestSecurity_PodCannotReachOllama`

Two targets: `host.containers.internal` (the macOS /
podman-machine alias for the host) and `10.0.2.2` (the
slirp4netns default gateway on Linux rootless). Both must
fail. The probe emits `mac=N lin=M`; the test asserts neither
value is 0. A green result means the pod cannot reach the
local Ollama server (the agent's own LLM). A pod that could
reach Ollama could call the model directly and bypass the
internal-API surface.

### 3. `TestSecurity_PodCannotReachPodmanSocket`

The probe runs `[ -e /var/run/podman/podman.sock ]`. The
file does not exist inside the pod because no bind mount
exposes it. The test asserts `sock_seen=NO` in stdout. A
green result means a pod cannot start a sibling container
via the host's podman API. (The flag `--security-opt
no-new-privileges` and the absence of any socket bind mount
together enforce this.)

### 4. `TestSecurity_PodCannotReadHostFS`

Two checks:

- `touch /usr/share/probe` must fail with
  `Read-only file system`. The `--read-only` flag (set in
  `args_common.go` line 16) is what enforces this. A green
  result means the pod cannot write to the rootfs.
- `mount | grep ' on / '` must show an `overlay` or
  `rootfs` mount. A host-bind would show the host's main
  device. A green result means the pod's `/` is a
  container-private overlay, not the host's filesystem.

### 5. `TestSecurity_PodHasNoCredentials`

The probe runs `env | grep -Ei 'token|key|secret|ollama|openai|anthropic'`.
A match means a secret leaked into the pod's environment.
The test asserts `grep_env=no_match` (i.e. the grep exited 1
because nothing matched). The probe also checks for
`/run/athanor`; the manager only bind-mounts the token dir
when a job has a token, and the probe explicitly clears
`TokenDir=""`. A green result means neither env nor the
token mount path is leaking.

## Audit trail

Every probe appends one row to the `events` table with
category `podman` (the §28.1 closed set's pod-surface
category) and a data payload of:

```
{
  "probe": "<name>",
  "script": "<the inside-pod command>",
  "exit_code": 1,
  "result": "pass" | "fail",
  "elapsed_ms": 137
}
```

A failing probe produces the same row with `result: "fail"`
and `level: warn` (instead of `info` for pass). Operators can
query the audit log:

```sql
SELECT ts, data_json FROM events
 WHERE category = 'podman'
   AND json_extract(data_json, '$.probe') = 'internet'
 ORDER BY id DESC LIMIT 5;
```

The choice to co-opt `podman` rather than add a new `security`
category to the §28.1 closed set is recorded in ADR-0010 D5.
The events table has the `data_json` field to carry a
`subcategory` later if M7-T3 (alarms) needs a finer filter.

## What the suite does NOT prove

Per ADR-0010 "Not in M2-T6":

- **Linux seccomp enforcement.** ADR-0007 documents that
  rootless macOS/podman-machine has no kernel seccomp
  available; the macOS run does not exercise the
  `--security-opt seccomp=runtime/default` flag. The
  structural argv test (`TestGateG2JobPodArgvCannotEscape`)
  asserts the flag is present on Linux; the behavioral
  end-to-end check is platform-asymmetric and documented.
- **Host-kernel escape.** The pod is rootless and has no
  privilege to attempt kernel CVEs. The host kernel is
  assumed patched (operator's responsibility per §21.1).
- **The `pytest` clause in Gate G2** (ROADMAP §3, gate
  table: "a pytest suite runs inside a pod and returns
  results") was a deferred capability at M2-T6 time. It is now
  end-to-end: M2-T4b ([ADR-0024](adr/0024-engine-pod-dispatch.md))
  replaced the internal API's 501 stubs with a real `PodExecutor`
  dispatch over `podman exec`, and the engine starts/stops a
  per-job pod around the `code`-archetype sub-steps. The route-level
  behavior is unit-tested (`internal/internalapi/exec_test.go`); the
  pod-in-the-loop behavior needs a real `podman`, so it is exercised
  by the opt-in `ATHANOR_RUN_INTEGRATION=1` probes like the rest of
  this document.

## Where things live

| Concern | Location |
|---|---|
| Job Pod manager | `internal/jobpod` |
| Hardening flag construction | `internal/jobpod/args_common.go` (cross-platform), `args_linux.go`, `args_darwin.go` |
| Per-job tokens | `internal/jobpod/token.go` + `internal/internalapi/middleware.go` |
| Internal API | `internal/internalapi` (loopback-only, bearer-token auth) |
| Tool envelope | `internal/toolenvelope` (per-task `allowed_tools_json` override, migration 0006) |
| Structural gate tests | `internal/gate/gate_g2_test.go` |
| Behavioral security probes | `internal/jobpod/security_test.go` (gated by `ATHANOR_RUN_INTEGRATION`) |
| Podman CLI client | `cmd/athanor/jobpod_client.go` (the only `os/exec` in the project, per Gate G1) |
| ADRs | `docs/adr/0007-podman-lifecycle.md`, `0008-job-pod-tokens.md`, `0009-engine-pod-wiring.md`, `0010-m2-security-suite.md` |
| Plan | `docs/m2-t6-plan.md` |
