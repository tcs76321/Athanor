#!/usr/bin/env sh
# fresh-core.sh — wipe a Core Pod and start a clean first-use instance.
#
# The supported runtime is the Core Pod. This script is for development and
# repeated first-use walkthroughs: it stops the container, backs up (by
# default) the host config/scratch/state and the Podman volumes, removes them,
# recreates a minimal Core config, and starts a clean Core Pod.
#
# It never touches a repository directory (default ~/athanor/repos).
#
# Usage:
#   scripts/fresh-core.sh --yes
#   scripts/fresh-core.sh --yes --no-start
#   scripts/fresh-core.sh --yes --no-backup
#
# Then open http://127.0.0.1:7420/ui/welcome

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
NAME="${ATHANOR_CORE_NAME:-athanor-clean-core}"
STATE_VOL="${ATHANOR_STATE_VOLUME:-athanor-clean-state}"
WORK_VOL="${ATHANOR_WORKSPACE_VOLUME:-athanor-clean-workspace}"
DIR="${ATHANOR_FRESH_DIR:-$HOME/athanor-clean}"
CONFIG="$DIR/core-config.yaml"
ADDR="${ATHANOR_ADDR:-127.0.0.1:7420}"
REPOS="${REPOS_DIR:-$HOME/athanor/repos}"
CORE_IMAGE="${CORE_IMAGE:-athanor-core:latest}"
JOBPOD_IMAGE="${JOBPOD_IMAGE:-athanor-jobpod:latest}"
BACKUP_ROOT="${ATHANOR_FRESH_BACKUPS:-$HOME/athanor-fresh-backups}"

YES=0
NO_BACKUP=0
NO_START=0
KEEP_LEGACY=0

usage() {
    cat <<'EOF'
usage: scripts/fresh-core.sh [--yes] [--no-backup] [--no-start] [--keep-legacy]

  --yes          do not prompt (required for non-interactive use)
  --no-backup    delete instead of copying state to ~/athanor-fresh-backups
  --no-start     reset everything but do not launch the Core Pod
  --keep-legacy  keep the older athanor-core container/volumes and
                 ~/.config/athanor config

Environment overrides:
  ATHANOR_CORE_NAME, ATHANOR_STATE_VOLUME, ATHANOR_WORKSPACE_VOLUME,
  ATHANOR_FRESH_DIR, ATHANOR_ADDR, REPOS_DIR, CORE_IMAGE, JOBPOD_IMAGE,
  ATHANOR_FRESH_BACKUPS
EOF
}

for arg in "$@"; do
    case "$arg" in
        --yes) YES=1 ;;
        --no-backup) NO_BACKUP=1 ;;
        --no-start) NO_START=1 ;;
        --keep-legacy) KEEP_LEGACY=1 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "fresh-core: unknown argument $arg" >&2; usage >&2; exit 2 ;;
    esac
done

if ! command -v podman >/dev/null 2>&1; then
    echo "fresh-core: podman is required" >&2
    exit 1
fi
if [ ! -x "$ROOT/bin/athanor" ]; then
    echo "fresh-core: $ROOT/bin/athanor is missing; run 'make build'" >&2
    exit 1
fi
if ! podman image exists "$CORE_IMAGE" >/dev/null 2>&1; then
    echo "fresh-core: Core image $CORE_IMAGE is missing; run 'make core-image'" >&2
    exit 1
fi
if ! podman image exists "$JOBPOD_IMAGE" >/dev/null 2>&1; then
    echo "fresh-core: Job Pod image $JOBPOD_IMAGE is missing; run 'make jobpod-image'" >&2
    exit 1
fi

if [ "$YES" -ne 1 ]; then
    cat <<EOF
fresh-core will remove:
  container   $NAME
  volumes     $STATE_VOL, $WORK_VOL
  host dirs   $DIR

Repositories under $REPOS are never touched.
EOF
    if [ "$KEEP_LEGACY" -eq 0 ]; then
        cat <<EOF
It will also remove the legacy athanor-core container, the athanor-state and
athanor-workspace volumes, and $HOME/.config/athanor.
EOF
    else
        echo "Legacy athanor-core, its volumes, and ~/.config/athanor are kept."
    fi
    printf 'Continue? [y/N] '
    read -r answer || answer=""
    case "$answer" in
        y|Y|yes|YES) ;;
        *) echo "fresh-core: cancelled"; exit 1 ;;
    esac
fi

stamp=$(date +%Y%m%dT%H%M%S)
backup="$BACKUP_ROOT/$stamp"
if [ "$NO_BACKUP" -eq 0 ]; then
    mkdir -p "$backup"
    echo "fresh-core: backing up to $backup"
fi

backup_dir() {
    src=$1
    name=$2
    [ -e "$src" ] || return 0
    if [ "$NO_BACKUP" -eq 0 ]; then
        cp -a "$src" "$backup/$name" 2>/dev/null || true
    fi
}

backup_volume() {
    vol=$1
    podman volume inspect "$vol" >/dev/null 2>&1 || return 0
    if [ "$NO_BACKUP" -eq 0 ]; then
        podman volume export "$vol" > "$backup/$vol.tar" 2>/dev/null || rm -f "$backup/$vol.tar"
    fi
}

backup_dir "$DIR" "athanor-clean"
backup_dir "$ROOT/state" "repo-state"
if [ "$KEEP_LEGACY" -eq 0 ]; then
    backup_dir "$HOME/.config/athanor" "dotconfig-athanor"
fi
backup_volume "$STATE_VOL"
backup_volume "$WORK_VOL"
if [ "$KEEP_LEGACY" -eq 0 ]; then
    backup_volume "athanor-state"
    backup_volume "athanor-workspace"
fi

# Stop and remove the container before its volumes.
podman rm -f "$NAME" >/dev/null 2>&1 || true
if [ "$KEEP_LEGACY" -eq 0 ]; then
    podman rm -f athanor-core >/dev/null 2>&1 || true
fi

podman volume rm -f "$STATE_VOL" "$WORK_VOL" >/dev/null 2>&1 || true
if [ "$KEEP_LEGACY" -eq 0 ]; then
    podman volume rm -f athanor-state athanor-workspace >/dev/null 2>&1 || true
fi

rm -rf "$DIR"
rm -rf "$ROOT/state"
if [ "$KEEP_LEGACY" -eq 0 ]; then
    rm -rf "$HOME/.config/athanor"
fi

mkdir -p "$DIR/state" "$DIR/tokens" "$DIR/scratch" "$REPOS"
cat > "$CONFIG" <<EOF
version: 2

inference:
  ollama_url: "http://host.containers.internal:11434"

job_pod:
  image: "$JOBPOD_IMAGE"
  token_host_dir: "$DIR/tokens"
  scratch_host_dir: "$DIR/scratch"
EOF

echo "fresh-core: removed the old container, volumes, and host state"
echo "fresh-core: fresh config $CONFIG"

if [ "$NO_START" -eq 1 ]; then
    echo "fresh-core: --no-start; launch with:"
    echo "  $ROOT/bin/athanor start -config $CONFIG -state-dir $DIR/state -state-volume $STATE_VOL -workspace-volume $WORK_VOL -container-name $NAME -addr $ADDR -skip-model-audit -repos-dir $REPOS"
    exit 0
fi

"$ROOT/bin/athanor" start \
    -config "$CONFIG" \
    -state-dir "$DIR/state" \
    -state-volume "$STATE_VOL" \
    -workspace-volume "$WORK_VOL" \
    -container-name "$NAME" \
    -addr "$ADDR" \
    -skip-model-audit \
    -repos-dir "$REPOS"

url="http://$ADDR/ui/welcome"
case "$ADDR" in
    0.0.0.0:*) url="http://127.0.0.1:${ADDR##*:}/ui/welcome" ;;
esac
echo ""
echo "fresh-core: clean first-use instance is ready"
echo "  $url"
if [ "$NO_BACKUP" -eq 0 ]; then
    echo "  backup: $backup"
fi
