#!/usr/bin/env sh
# Athanor installer (ROADMAP M7-T7/T8; ADR-0078).
#
# The supported runtime is the Core Pod. This script installs the host CLI
# client, ensures the Job Pod and Core images exist, writes a Core Pod config,
# and starts the container. `--host` keeps the old host-binary service path as
# an explicit development escape hatch; it is never the supported runtime.
set -eu

PREFIX="${PREFIX:-/usr/local/bin}"
CONFIG_DIR="${CONFIG_DIR:-$HOME/.config/athanor}"
STATE_DIR="${STATE_DIR:-$HOME/.local/state/athanor}"
CORE_IMAGE="${CORE_IMAGE:-athanor-core:latest}"
JOBPOD_IMAGE="${JOBPOD_IMAGE:-athanor-jobpod:latest}"
CONTAINER="${CONTAINER:-athanor-core}"
INSTALL_SERVICE=0
HOST_MODE=0
NO_BUILD=0
for arg in "$@"; do
    case "$arg" in
        --service) INSTALL_SERVICE=1 ;;
        --host) HOST_MODE=1 ;;
        --no-build) NO_BUILD=1 ;;
        *) echo "usage: $0 [--service] [--host] [--no-build]" >&2; exit 2 ;;
    esac
done

# repo root is the parent of this script's directory
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
TOKEN_DIR="$CONFIG_DIR/tokens"

if ! command -v podman >/dev/null 2>&1; then
    echo "athanor: podman is required (rootless mode)" >&2
    exit 1
fi
PODMAN=$(command -v podman)

# write_host_service installs the legacy host-binary unit (development only).
write_host_service() {
    podman_bin=$1
    if [ "$(uname -s)" = "Darwin" ]; then
        target="$HOME/Library/LaunchAgents"
        mkdir -p "$target"
        sed -e "s#__BINARY__#$PREFIX/athanor#g" \
            -e "s#__CONFIG__#$CONFIG_DIR/config.yaml#g" \
            -e "s#__STATE__#$STATE_DIR#g" \
            "$ROOT/deploy/com.athanor.agent.plist" > "$target/com.athanor.agent.plist"
        echo "installed launchd agent: $target/com.athanor.agent.plist"
    else
        target="$HOME/.config/systemd/user"
        mkdir -p "$target"
        sed -e "s#__BINARY__#$PREFIX/athanor#g" \
            -e "s#__CONFIG__#$CONFIG_DIR/config.yaml#g" \
            -e "s#__STATE__#$STATE_DIR#g" \
            "$ROOT/deploy/athanor.service" > "$target/athanor.service"
        echo "installed systemd user unit: $target/athanor.service"
    fi
}

# write_core_service installs a unit that starts the existing Core container.
write_core_service() {
    podman_bin=$1
    if [ "$(uname -s)" = "Darwin" ]; then
        target="$HOME/Library/LaunchAgents"
        mkdir -p "$target"
        sed -e "s#__PODMAN__#$podman_bin#g" \
            -e "s#__CONTAINER__#$CONTAINER#g" \
            -e "s#__LOG__#$CONFIG_DIR/core.log#g" \
            "$ROOT/deploy/com.athanor.core.plist" > "$target/com.athanor.core.plist"
        echo "installed launchd agent: $target/com.athanor.core.plist"
    else
        target="$HOME/.config/systemd/user"
        mkdir -p "$target"
        sed -e "s#__PODMAN__#$podman_bin#g" \
            -e "s#__CONTAINER__#$CONTAINER#g" \
            "$ROOT/deploy/athanor-core.service" > "$target/athanor.service"
        echo "installed systemd user unit: $target/athanor.service"
    fi
}

# 1. Build and install the host CLI (client + development commands).
if [ ! -x "$PREFIX/athanor" ] || [ "${REBUILD:-0}" = "1" ]; then
    ( cd "$ROOT" && make build )
    mkdir -p "$PREFIX"
    install -m 0755 "$ROOT/bin/athanor" "$PREFIX/athanor"
fi

mkdir -p "$CONFIG_DIR" "$TOKEN_DIR"
chmod 700 "$TOKEN_DIR"

if [ "$HOST_MODE" = "1" ]; then
    mkdir -p "$STATE_DIR"
    if [ ! -f "$CONFIG_DIR/config.yaml" ]; then
        "$PREFIX/athanor" init -out "$CONFIG_DIR/config.yaml"
    fi
    if [ "$INSTALL_SERVICE" = "1" ]; then
        write_host_service "$PODMAN"
    fi
    echo "installed athanor (host development mode) to $PREFIX/athanor"
    echo "next: $PREFIX/athanor start -host -config $CONFIG_DIR/config.yaml -state-dir $STATE_DIR"
    exit 0
fi

# 2. Build the images if missing (skip with --no-build).
if [ "$NO_BUILD" = "0" ]; then
    if ! podman image exists "$JOBPOD_IMAGE"; then
        ( cd "$ROOT" && make jobpod-image )
    fi
    if ! podman image exists "$CORE_IMAGE"; then
        ( cd "$ROOT" && make core-image )
    fi
fi
if ! podman image exists "$JOBPOD_IMAGE"; then
    echo "athanor: missing Job Pod image $JOBPOD_IMAGE (run make jobpod-image)" >&2
    exit 1
fi
if ! podman image exists "$CORE_IMAGE"; then
    echo "athanor: missing Core image $CORE_IMAGE (run make core-image)" >&2
    exit 1
fi

# 3. Write a Core Pod config on first install.
if [ ! -f "$CONFIG_DIR/config.yaml" ]; then
    "$PREFIX/athanor" init -core -out "$CONFIG_DIR/config.yaml" \
        -token-host-dir "$TOKEN_DIR" -jobpod-image "$JOBPOD_IMAGE"
fi

# 4. Start (or reuse) the Core Pod. Doctor is run next; it may report
#    missing persona models, which the operator pulls with `ollama pull`.
"$PREFIX/athanor" start -skip-doctor -config "$CONFIG_DIR/config.yaml" \
    -core-image "$CORE_IMAGE" -container-name "$CONTAINER"
"$PREFIX/athanor" doctor -config "$CONFIG_DIR/config.yaml" || true

# 5. Optional headless unit. It starts the existing container rather than
#    re-running podman with a second set of mounts.
if [ "$INSTALL_SERVICE" = "1" ]; then
    write_core_service "$PODMAN"
fi

echo "installed athanor to $PREFIX/athanor"
echo "Core Pod log: podman logs $CONTAINER"
echo "stop: podman stop $CONTAINER"
