# Deploying the Core Pod (M9-T8)

The **Core Pod** is the container-native Athanor deployment
([ADR-0069](adr/0069-core-pod-packaging.md)): one image running the single
`athanor` daemon. It is trusted and holds the Podman socket so it can spawn
Job Pods; Job Pods never receive that socket (§3.1, Gate G2).

The Core Pod is the **only supported runtime**
([ADR-0078](adr/0078-core-pod-only-runtime.md)). The **host binary** is a client
and a development/testing tool; its `serve`/`go run` mode is an unsupported
escape hatch. This guide is the supported deployment.

## Prerequisites

- Rootless Podman. On macOS/Apple Silicon, a `podman machine` must be running.
- Ollama on the host with the persona models pulled.
- The **Job Pod** base image built once: `make jobpod-image`.
- Build the Core image from a clone: `make core-image` (multi-stage; the C toolchain,
  `sqlite_fts5`, and the version stamp are handled inside the build).

## Multi-arch (Apple Silicon first)

`make core-image` builds for the host platform. To publish arm64 + amd64:

```bash
podman build --platform linux/arm64 -t athanor-core:0.0.0-arm64 -f deploy/core.Containerfile .
podman build --platform linux/amd64 -t athanor-core:0.0.0-amd64 -f deploy/core.Containerfile .
podman manifest create athanor-core:0.0.0
podman manifest add athanor-core:0.0.0 athanor-core:0.0.0-arm64
podman manifest add athanor-core:0.0.0 athanor-core:0.0.0-amd64
```

## Config

The image's Ollama URL must point at the **host** through the container alias:
a Core Pod reaches a host Ollama at `http://host.containers.internal:11434`
(the host-binary default is `127.0.0.1`). Write a config for the container:

```yaml
# ~/.config/athanor/config.yaml (mounted in)
inference:
  ollama_url: "http://host.containers.internal:11434"

job_pod:
  image: "athanor-jobpod:latest"   # the Job Pod base image (host podman store)
```

`athanor doctor` reports the shape and warns if `ollama_url` does not match it
(M9-T7).

## Run

State and workspace are volumes (never baked in). The Podman socket is mounted
so the Core can create Job Pods; the UI binds loopback inside the container.

```bash
podman run --rm \
  -p 127.0.0.1:7420:7420 \
  -v "$XDG_RUNTIME_DIR/podman/podman.sock:/run/podman/podman.sock" \
  -v athanor-state:/var/lib/athanor/state \
  -v athanor-workspace:/var/lib/athanor/workspace \
  -v "$HOME/.config/athanor/config.yaml:/etc/athanor/config.yaml:ro" \
  athanor-core:0.0.0
```

Platform notes:

- **Linux:** the rootless socket is typically `$XDG_RUNTIME_DIR/podman/podman.sock`
  (or `run/user/<uid>/podman/podman.sock`).
- **macOS (Apple Silicon):** the Core Pod runs inside the `podman machine`; the
  socket is reached via the machine. Bind mounts from the mac are VM-crossing and
  slower than VM-local volumes — prefer named volumes for `state/` and
  `workspace/`, in particular for the SQLite database (WAL over a VM-crossing
  mount is discouraged).

## Verify

```bash
curl -s http://127.0.0.1:7420/healthz     # {status, version, uptime, goroutines}
podman exec <core> athanor doctor         # reports "Core Pod (container)" shape
```

Watch that Job Pods come and go without orphans (`podman ps -a | grep athanor-job-`),
and confirm a Job Pod has no access to `/run/podman/podman.sock`.

## Security boundary

The socket is the **Core's**. It must never be mounted into a Job Pod — the
structural gate (`TestGateG2JobPodArgvCannotEscape`) and the M2 behavioural probe
enforce this. Treat a change to the Core's socket mount as a §31.3 audit item.
