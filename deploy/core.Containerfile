# Athanor Core Pod image (ADR-0069; ARCHITECTURE §3, §32).
#
# One container running the single `athanor` daemon — core/gateway/scanner/ui
# are Go packages inside it, not separate processes. The Core is trusted and
# holds the Podman socket (mounted at run time) so it can spawn Job Pods; Job
# Pods never see that socket (§3.1). Build with `make core-image`.
#
# Build context is the repo root. The image is arch-agnostic; build per platform
# and assemble a manifest for multi-arch:
#   podman build --platform linux/arm64 -t athanor-core:0.0.0-arm64 -f deploy/core.Containerfile .
#   podman build --platform linux/amd64 -t athanor-core:0.0.0-amd64 -f deploy/core.Containerfile .
#   podman manifest create athanor-core:0.0.0
#   podman manifest add athanor-core:0.0.0 athanor-core:0.0.0-arm64
#   podman manifest add athanor-core:0.0.0 athanor-core:0.0.0-amd64
#
# Run (macOS/Apple Silicon) — the Podman socket and state/workspace volumes are
# mounted; the daemon binds loopback inside the container and the UI is published
# on the host loopback:
#   podman run --rm -p 127.0.0.1:7420:7420 \
#     -v "$XDG_RUNTIME_DIR/podman/podman.sock:/run/podman/podman.sock" \
#     -v athanor-state:/var/lib/athanor/state \
#     -v athanor-workspace:/var/lib/athanor/workspace \
#     athanor-core:0.0.0
#
# The Core Pod reaches a host Ollama at http://host.containers.internal:11434
# (set inference.ollama_url accordingly; the host-binary default is 127.0.0.1).

# --- build stage ---
FROM golang:1.27.2-bookworm AS build
WORKDIR /src
# CGO is required (mattn/go-sqlite3) and FTS5 is a build tag, so a C toolchain
# and the sqlite_fts5 tag are mandatory (see DEVELOPMENT.md).
RUN apt-get update \
 && apt-get install -y --no-install-recommends gcc libc6-dev \
 && rm -rf /var/lib/apt/lists/*
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=0.0.0-dev
RUN CGO_ENABLED=1 go build -tags sqlite_fts5 \
      -ldflags "-X main.version=${VERSION}" \
      -o /out/athanor ./cmd/athanor

# --- runtime stage ---
# debian-slim: the binary is CGO-heavy (sqlite3 + tree-sitter C grammars), so a
# glibc base is safer than musl. `podman` provides the client the Core shells out
# to (over the mounted socket) to create Job Pods; `tini` is the init.
FROM debian:bookworm-slim

# clamav + yara are the airlock's fail-closed scanners. Without them every
# workspace egress is blocked (`scanner:clamav:absent`); `freshclam` bakes the
# signature DB at build time so a fresh Core Pod can scan without a network.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tini podman git clamav yara \
 && rm -rf /var/lib/apt/lists/* \
 && freshclam --quiet

# The Core Pod's podman client must talk to the mounted machine socket, not to
# a local (container) store. Without this the client tries overlay-over-overlayfs
# and fails; the socket is the runtime boundary (ADR-0069).
ENV CONTAINER_HOST=unix:///run/podman/podman.sock

COPY --from=build /out/athanor /usr/local/bin/athanor
COPY config.example.yaml /etc/athanor/config.example.yaml
# known-issues M13: bake the Core-Pod runtime config the CMD loads, so a fresh
# container points Ollama at host.containers.internal instead of the host-binary
# default (127.0.0.1, unreachable inside the container).
COPY deploy/core-config.yaml /etc/athanor/config.yaml

# State and workspace are volumes (never baked into the image). The daemon binds
# loopback inside the container (§21.8); publish 7420 on the host loopback.
VOLUME ["/var/lib/athanor/state", "/var/lib/athanor/workspace"]
EXPOSE 7420

# The daemon binds the container wildcard; the host publishes only loopback
# (127.0.0.1:7420). The `-container` flag is the explicit exception to §21.8;
# the host binary still refuses a non-loopback bind. The Podman socket the Core
# uses to spawn Job Pods is mounted at run time:
#   -v /run/podman/podman.sock:/run/podman/podman.sock
# Job Pods are separate containers and never receive this mount.

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/athanor"]
CMD ["serve", "-container", "-addr", "0.0.0.0:7420", "-config", "/etc/athanor/config.yaml", "-state-dir", "/var/lib/athanor/state"]
