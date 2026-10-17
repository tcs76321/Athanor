# ADR 0078 — The Core Pod is the only supported runtime

**Status:** Accepted · **Date:** 2026-10-17 · **Refs:**
[ADR-0069](0069-core-pod-packaging.md) (Core Pod packaging),
[ADR-0056](0056-packaging.md) (host-binary packaging),
[ADR-0075](0075-frozen-corpus-compass.md) (frozen-corpus soak),
[ADR-0076](0076-continuous-evidence.md) (continuous evidence);
ARCHITECTURE §3, §3.1, §32.

## Context

There are two ways to run the daemon: the **host binary** (`athanor serve`, the
`go run` dev path, `install.sh`) and the **Core Pod** (the OCI image, ADR-0069).
Two runtimes drift: packaging, isolation, the Ollama URL shape, state/volume
handling, and the soak all diverge. A soak that measures the host binary does
not measure what a user runs. And a supported host runtime keeps the host's
toolchain, CGO, and filesystem in the trust and support boundary.

The operator's target is the full pod architecture for all real use and for the
controlled study.

## Decision

**1. One supported daemon runtime: the Core Pod.** The daemon runs only in the
Core Pod ([ADR-0069](0069-core-pod-packaging.md)). The host binary remains a
**client** and a **development/testing tool**; its `serve`/`go run` mode is an
explicitly unsupported development escape hatch, never shipped, never measured,
never invoked by the installer or a soak.

**2. Start/install target the Core Pod.** `athanor start` and the installer
ensure the Core Pod image exists and the container is running (podman run or a
quadlet), publish the UI on loopback, and verify `/healthz`. They do not start
an in-process daemon.

**3. The controlled study measures the shipped artifact.** The frozen-corpus
soak ([ADR-0075](0075-frozen-corpus-compass.md)) runs the Core Pod, with the
state directory on a volume the probe can read. Host-binary soaks are not
evidence.

**4. Host-facing commands are split explicitly.**
- **Client commands** (project, goal, job, artifacts, hitl, push, digest,
  strategy, freeze/unfreeze) run on the host against the loopback API.
- **Host-inspection commands** (`doctor`) check the host prerequisites and the
  Core's health/shape; `backup`/`restore` operate on the Core's state volume
  (via exec or an API); `models` uses the Core's configured Ollama URL.

**5. Development stays host-side.** `go test`/`vet`/`build`, unit and
integration tests, and a `go run` debugging loop are unaffected and remain the
fast path. "Only runtime" constrains what is **supported and measured**, not how
the code is developed.

**6. Named volumes, not VM-crossing mounts, for state.** State/workspace are
volumes; on macOS the SQLite/WAL files must not live on a VM-crossing bind mount
(see `docs/deploy-core-pod.md`).

## Consequences

- One artifact, one topology, one support story; the installer, doctor, soak,
  and docs all describe the same thing.
- The host binary's `serve` path is demoted in docs and CI; it may be removed
  later once the dev loop has a container-friendly replacement.
- `athanor start`, `backup`/`restore`, and `models` need rework for the
  container boundary.
- **Nested Podman on macOS is the main risk**: the Core holds the socket and
  spawns Job Pods inside `podman machine`. Verify the socket path, volume
  strategy, and Job Pod teardown before declaring the host path retired.
- The soak harness/probe must start or attach to a Core Pod and read the state
  volume; that is an implementation task.

## Alternatives rejected

- **Two supported runtimes.** Guarantees drift and a soak that measures the
  wrong artifact.
- **Host-only.** Loses the isolation, reproducibility, and packaging the
  project is built around.
- **Container-only development.** Slows the tight loop for no support benefit.
