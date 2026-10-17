# Athanor benchmark probe image (F1/F7).
#
# The Core Pod owns the state volume; on macOS that volume is inside the Podman
# machine and cannot be read reliably by the host (SQLite/WAL over virtiofs is
# not safe). The probe therefore runs INSIDE the Core Pod's namespace:
#
#   podman exec -w /bench/repo athanor-core athanor-probe soak -external ...
#
# It reads /var/lib/athanor/state directly, calls the Core at 127.0.0.1:7420,
# and writes the results tree to the host-mounted /bench. The probe is a
# development/measurement binary, not part of the product surface.
FROM golang:1.27.2-bookworm AS build
WORKDIR /src
RUN apt-get update \
 && apt-get install -y --no-install-recommends gcc libc6-dev \
 && rm -rf /var/lib/apt/lists/*
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -tags sqlite_fts5 -o /out/athanor-probe ./spikes/m3-t7-probe

FROM athanor-core:latest
COPY --from=build /out/athanor-probe /usr/local/bin/athanor-probe
