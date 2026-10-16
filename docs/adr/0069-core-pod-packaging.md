# ADR 0069 — Core Pod OCI packaging

**Status:** Accepted · **Date:** 2026-10-15 · **Refs:**
ARCHITECTURE §3 (topology), §21.2 (Job Pod hardening), §21.8 (loopback), §32
(deployment); [ADR-0056](0056-packaging.md) (host-binary packaging, amended);
[ADR-0066](0066-release-scope-soak.md) (0.0.0 scope); ROADMAP M7-T7/T8;
[deploy-core-pod.md](../deploy-core-pod.md); `SECURITY.md`; Gate G1
(`internal/gate/gate_test.go`)

## Context

ARCHITECTURE §3 draws a **Core Pod** (core · gateway · scanner · ui) that spawns
ephemeral Job Pods. The code, however, ships one Go binary run as a **host
binary** (the daemon embeds all four concerns as packages, not processes), and
[ADR-0056](0056-packaging.md) deferred the Core Pod image. That leaves the
documented topology half-realized, and it makes "install Athanor elsewhere"
mean "build from source and drop a binary."

For 0.0.0 we want (a) the documented topology to be true, and (b) a reproducible
deploy for Linux and macOS (Apple Silicon first). This ADR resolves the Core Pod
image and the trust boundary it introduces.

## Decision

**1. Publish a Core Pod OCI image containing the single `athanor` daemon.** The
"Core Pod" is one container running one process — core/gateway/scanner/ui are
Go packages inside it, not four services. The image adds a Podman **client**
and a tiny init (`tini`); multi-arch `linux/arm64` (primary, Apple Silicon) and
`linux/amd64`; base `debian-slim` (the binary is CGO-heavy: `mattn/go-sqlite3`
and the tree-sitter C grammars).

**2. The Core Pod spawns Job Pods by shelling out to the `podman` client over a
mounted Podman socket** (podman-in-podman). Gate G1 forbids importing
`libpod`, so CLI-over-socket is the mechanism. The trust boundary is stated
explicitly:

- The Core Pod is **trusted** and holds the socket.
- Job Pods are **untrusted** and must never receive the socket (or its path) —
  §3.1. A structural gate test asserts the Job Pod `run` argv and mounts never
  include the Podman socket path.

**3. The Ollama URL is packaging-aware.** The host-binary shape reaches Ollama
at `http://127.0.0.1:11434`; a Core Pod reaches the host at
`http://host.containers.internal:11434` (the container→host alias). The built-in
default stays host-binary-correct; the image/deploy sets the container value.
`athanor doctor` probes whichever is configured and reports the shape.

**4. State and workspace are volumes.** `state/` and `workspace/` are named
volumes or bind mounts; the daemon binds **loopback inside the container**
(§21.8); the UI/API is reached via a published loopback port or SSH forwarding.

**5. The host-binary shape remains supported.** The systemd user unit / launchd
agent from ADR-0056 stay; the Core Pod is an alternative for container-native
and fresh installs. `scripts/install.sh` gains a container path.

**6. macOS caveat.** On Apple Silicon the Core Pod runs inside the `podman
machine` VM; bind mounts from the mac are VM-crossing and slower than VM-local
volumes. Documented, not solved.

## Consequences

- ARCHITECTURE §3 becomes true: the Core Pod is the shipped default for fresh
  installs, with the host binary as the development/alternative shape.
- A new **socket surface** exists inside the Core Pod. It is exactly the §3.1
  trust split (trusted Core, untrusted Jobs), but it is reviewed here, covered
  by a gate test, and must be part of the §31.3 audit.
- The Ollama default is now **context-dependent**; the config default (host
  `127.0.0.1`) and the image override (`host.containers.internal`) must stay
  consistent, and doctor must report which shape is running.
- The fresh-install demo (<15 min, Gate G7) is run for **both** shapes on Linux
  and macOS.
- SQLite state on a bind mount from the mac host is discouraged (WAL over a
  VM-crossing mount); the docs steer macOS users to a VM-local volume.

## Alternatives rejected

- **Host binary only (status quo).** Leaves §3 unrealized and distribution
  awkward; the goal is a reproducible deploy.
- **Core Pod importing `libpod`.** Forbidden by Gate G1; CLI-over-socket is the
  sanctioned mechanism.
- **Four Core processes (core/gateway/scanner/ui).** The codebase is one binary;
  splitting it adds orchestration for no benefit and contradicts the "Core is
  the sole router" simplicity.
- **Dynamic Podman socket discovery via the default path only.** Fragile across
  podman versions and machines; the socket path is configured explicitly.
