#!/usr/bin/env sh
# Athanor install (ROADMAP M7-T7/T8). Installs the single binary and, with
# --service, a headless service unit (systemd user unit on Linux, launchd
# agent on macOS). The Core runs as a host binary; execution happens in
# rootless Podman Job Pods built from deploy/jobpod.Containerfile.
set -eu

PREFIX="${PREFIX:-/usr/local/bin}"
CONFIG_DIR="${CONFIG_DIR:-$HOME/.config/athanor}"
STATE_DIR="${STATE_DIR:-$HOME/.local/state/athanor}"
INSTALL_SERVICE=0
for arg in "$@"; do
    case "$arg" in
        --service) INSTALL_SERVICE=1 ;;
        *) echo "usage: $0 [--service]" >&2; exit 2 ;;
    esac
done

# repo root is the parent of this script's directory
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

if [ ! -x "$PREFIX/athanor" ] || [ "${REBUILD:-0}" = "1" ]; then
    ( cd "$ROOT" && make build )
    mkdir -p "$PREFIX"
    install -m 0755 "$ROOT/bin/athanor" "$PREFIX/athanor"
fi

mkdir -p "$CONFIG_DIR" "$STATE_DIR"
if [ ! -f "$CONFIG_DIR/config.yaml" ]; then
    "$PREFIX/athanor" init -out "$CONFIG_DIR/config.yaml"
fi

if [ "$INSTALL_SERVICE" = "1" ]; then
    if [ "$(uname -s)" = "Darwin" ]; then
        TARGET="$HOME/Library/LaunchAgents"
        mkdir -p "$TARGET"
        sed -e "s#__BINARY__#$PREFIX/athanor#g" \
            -e "s#__CONFIG__#$CONFIG_DIR/config.yaml#g" \
            -e "s#__STATE__#$STATE_DIR#g" \
            "$ROOT/deploy/com.athanor.agent.plist" > "$TARGET/com.athanor.agent.plist"
        echo "installed launchd agent: $TARGET/com.athanor.agent.plist"
        echo "load it with: launchctl load $TARGET/com.athanor.agent.plist"
    else
        TARGET="$HOME/.config/systemd/user"
        mkdir -p "$TARGET"
        sed -e "s#__BINARY__#$PREFIX/athanor#g" \
            -e "s#__CONFIG__#$CONFIG_DIR/config.yaml#g" \
            -e "s#__STATE__#$STATE_DIR#g" \
            "$ROOT/deploy/athanor.service" > "$TARGET/athanor.service"
        echo "installed systemd user unit: $TARGET/athanor.service"
        echo "enable it with: systemctl --user enable --now athanor"
    fi
fi

echo "installed athanor to $PREFIX/athanor"
echo "next: $PREFIX/athanor doctor && $PREFIX/athanor start"
