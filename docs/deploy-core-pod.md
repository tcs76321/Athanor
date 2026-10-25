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

> **Installer shortcut (M9-P1).** `sh scripts/install.sh` builds the Job Pod
> and Core images if missing, writes a Core Pod config with host-visible
> `job_pod.token_host_dir` and `job_pod.scratch_host_dir`, starts the
> container, and waits for `/healthz`.
> `--service` writes a unit that supervises the existing container. The manual
> steps below remain the reference for what the installer does and for
> debugging.

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
  image: "athanor-jobpod:latest"   # the Job Pod base image
  # The machine-visible token base. Job Pods are separate containers, so the
  # remote podman must see this path on the machine; the Core writes the same
  # files through its token bind mount. See "Run" below.
  token_host_dir: "/Users/you/.config/athanor/tokens"
  scratch_host_dir: "/Users/you/.config/athanor/scratch"
```

`athanor doctor` reports the shape and warns if `ollama_url` does not match it
(M9-T7). Run it inside the container with the mounted config:

```bash
podman exec athanor-core athanor doctor -config /etc/athanor/config.yaml
```

## Run

State and workspace are volumes (never baked in). The Podman socket is mounted
so the Core can create Job Pods. On SELinux hosts (the Fedora CoreOS podman
machine) the socket mount needs `--security-opt label=disable`: the Core is the
trusted container, and this does **not** relax Job Pod hardening.

The daemon listens on the container wildcard (`serve -container`); the host
publishes only loopback, so the container boundary is the trust boundary.

```bash
SOCK=$(podman machine ssh 'echo $XDG_RUNTIME_DIR')/podman/podman.sock   # macOS
# Linux: SOCK="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/podman/podman.sock"
mkdir -p "$HOME/.config/athanor/tokens" "$HOME/.config/athanor/scratch"

podman run -d --name athanor-core --security-opt label=disable \
  -p 127.0.0.1:7420:7420 \
  -v "$SOCK:/run/podman/podman.sock" \
  -v athanor-state:/var/lib/athanor/state \
  -v "$HOME/.config/athanor/tokens:/var/lib/athanor/state/tokens" \
  -v "$HOME/.config/athanor/scratch:/var/lib/athanor/state/scratch" \
  -v athanor-workspace:/var/lib/athanor/workspace \
  -v "$HOME/.config/athanor/config.yaml:/etc/athanor/config.yaml:ro" \
  athanor-core:0.0.0
```

The image bakes `CONTAINER_HOST=unix:///run/podman/podman.sock`, so the Core's
`podman` client talks to the machine socket rather than a local container store.
If you run an image built before that change, add
`-e CONTAINER_HOST=unix:///run/podman/podman.sock`.

Platform notes:

- **Linux:** the rootless socket is typically `$XDG_RUNTIME_DIR/podman/podman.sock`
  (or `run/user/<uid>/podman/podman.sock`). The token bind path is a normal host
  path.
- **macOS (Apple Silicon):** the Core Pod runs inside the `podman machine`; the
  socket path is the machine's (`podman machine ssh 'echo $XDG_RUNTIME_DIR'`).
  - **The state database must be a named volume.** A host bind for
    `state/` corrupts SQLite/WAL over virtiofs (observed as
    `file is not a database`); named volumes are machine-local and safe.
  - **The token and scratch directories must be host binds**, because Job Pods
    are separate containers and the machine must be able to `stat` and mount
    the per-job token and repository snapshot sources. Keep only
    `<state>/tokens` and `<state>/scratch` on their binds; never the database.
  - Set `job_pod.token_host_dir` and `job_pod.scratch_host_dir` to the **host**
    paths of those binds (the paths the Podman machine sees), not container
    paths.

## Verify

```bash
curl -s http://127.0.0.1:7420/healthz     # {status, version, uptime, goroutines}
podman exec athanor-core athanor doctor -config /etc/athanor/config.yaml
```

Watch that Job Pods come and go without orphans (`podman ps -a | grep athanor-job-`),
and confirm a Job Pod has no access to `/run/podman/podman.sock`.

The image also ships the airlock scanners: **ClamAV** (with the signature
database baked at build time) and **YARA**. Without them, workspace egress is
fail-closed (`scanner:clamav:absent`) even when the job itself succeeds.

For the benchmark, build the probe image and run it **inside** the Core Pod
(the state volume is not host-readable on macOS):

```bash
make bench-full        # builds/refreshes the images, config, Core Pod, probe
./scripts/bench-core.sh stop
```

The probe (`athanor-probe`) reads `/var/lib/athanor/state`, calls the daemon at
`127.0.0.1:7420`, and writes the results tree to the host-mounted `/bench`
(`BENCH_OUT/out`).

## Security boundary

The socket is the **Core's**. It must never be mounted into a Job Pod — the
structural gate (`TestGateG2JobPodArgvCannotEscape`) and the M2 behavioural probe
enforce this. Treat a change to the Core's socket mount as a §31.3 audit item.
