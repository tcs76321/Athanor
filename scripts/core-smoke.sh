#!/usr/bin/env sh
# Core Pod smoke (P1). Boots the supported runtime from the built image,
# waits for /healthz, exercises one external-API create (metadata only), and
# checks the UI. It deliberately does not need Ollama: the goal is the
# container, config, state volume, HTTP surface, and teardown, not inference.
#
# Usage:
#   make core-image jobpod-image
#   make core-smoke
#
# The container name and volumes are unique to this run, so a developer's
# athanor-core install is never touched.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
BIN="${BIN:-$ROOT/bin/athanor}"
CORE_IMAGE="${CORE_IMAGE:-athanor-core:latest}"
JOBPOD_IMAGE="${JOBPOD_IMAGE:-athanor-jobpod:latest}"
NAME="athanor-smoke-$$"
STATE_VOL="athanor-smoke-state-$$"
WORK_VOL="athanor-smoke-work-$$"
ADDR="127.0.0.1:7420"
TMP=$(mktemp -d "${TMPDIR:-/tmp}/athanor-smoke.XXXXXX")

cleanup() {
    podman stop --time 10 "$NAME" >/dev/null 2>&1 || true
    podman rm -f "$NAME" >/dev/null 2>&1 || true
    podman volume rm -f "$STATE_VOL" "$WORK_VOL" >/dev/null 2>&1 || true
    rm -rf "$TMP"
}
on_exit() {
    status=$?
    if [ "$status" -ne 0 ]; then
        echo "core-smoke: container log (last 60 lines):" >&2
        podman logs --tail 60 "$NAME" >&2 2>&1 || true
    fi
    cleanup
    exit "$status"
}
trap on_exit EXIT INT TERM

if [ ! -x "$BIN" ]; then
    echo "core-smoke: missing $BIN (run make build)" >&2
    exit 1
fi
if ! podman image exists "$CORE_IMAGE"; then
    echo "core-smoke: missing $CORE_IMAGE (run make core-image)" >&2
    exit 1
fi
if ! podman image exists "$JOBPOD_IMAGE"; then
    echo "core-smoke: missing $JOBPOD_IMAGE (run make jobpod-image)" >&2
    exit 1
fi

mkdir -p "$TMP/tokens" "$TMP/scratch"
"$BIN" init -core -out "$TMP/config.yaml" \
    -token-host-dir "$TMP/tokens" -scratch-host-dir "$TMP/scratch" \
    -jobpod-image "$JOBPOD_IMAGE" >/dev/null

"$BIN" start -skip-doctor -config "$TMP/config.yaml" \
    -core-image "$CORE_IMAGE" -container-name "$NAME" \
    -state-volume "$STATE_VOL" -workspace-volume "$WORK_VOL" \
    -addr "$ADDR"

health=$(curl -fsS --max-time 5 "http://$ADDR/healthz")
case "$health" in
    *'"status":"ok"'*) ;;
    *) echo "core-smoke: unexpected /healthz: $health" >&2; exit 1 ;;
esac

# A metadata-only project exercises the API, config, and state volume without
# requiring a model or a Job Pod. start:false is load-bearing here.
created=$(curl -fsS --max-time 10 -X POST "http://$ADDR/projects" \
    -H 'Content-Type: application/json' \
    -d '{"name":"core-smoke","archetype":"text","goal":"Smoke the Core Pod HTTP surface without starting work.","start":false}')
case "$created" in
    *'"id"'*) ;;
    *) echo "core-smoke: project create failed: $created" >&2; exit 1 ;;
esac

ui=$(curl -fsS --max-time 5 "http://$ADDR/ui")
case "$ui" in
    *"Athanor"*) ;;
    *) echo "core-smoke: /ui did not render" >&2; exit 1 ;;
esac

echo "core-smoke: ok"
