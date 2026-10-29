#!/usr/bin/env sh
# Install a prebuilt Athanor release with SHA-256 verification.
#
# This is the F8 install-on-ramp. It downloads one tarball and SHA256SUMS,
# verifies the checksum, installs the host CLI, writes a Core Pod config, checks
# the images exist, and starts the supported runtime. Model pulls remain an
# explicit operator action (they are large); the script prints the next step.
#
# Usage:
#   sh install-release.sh -version 0.0.0 -base-url https://example/releases
#   sh install-release.sh -version 0.0.0 -pull-images
set -eu

PREFIX=${PREFIX:-/usr/local/bin}
CONFIG_DIR=${CONFIG_DIR:-$HOME/.config/athanor}
VERSION=""
BASE_URL=${ATHANOR_RELEASE_BASE:-}
PULL_IMAGES=0
CHECK_ONLY=0
CORE_IMAGE=${CORE_IMAGE:-athanor-core:latest}
JOBPOD_IMAGE=${JOBPOD_IMAGE:-athanor-jobpod:latest}
OS=${GOOS:-$(uname -s | tr '[:upper:]' '[:lower:]')}
ARCH=${GOARCH:-$(uname -m)}
case "$ARCH" in
    x86_64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
esac

while [ $# -gt 0 ]; do
    case "$1" in
        -version) VERSION=$2; shift 2 ;;
        -base-url) BASE_URL=$2; shift 2 ;;
        -pull-images) PULL_IMAGES=1; shift ;;
        -check-only) CHECK_ONLY=1; shift ;;
        -prefix) PREFIX=$2; shift 2 ;;
        *) echo "usage: $0 -version <v> -base-url <url> [-pull-images] [-prefix <dir>]" >&2; exit 2 ;;
    esac
done
if [ -z "$VERSION" ] || [ -z "$BASE_URL" ]; then
    echo "install-release: -version and -base-url are required" >&2
    exit 2
fi

TMP=$(mktemp -d "${TMPDIR:-/tmp}/athanor-release.XXXXXX")
trap 'rm -rf "$TMP"' EXIT INT TERM
ASSET="athanor-$VERSION-$OS-$ARCH.tar.gz"

echo "install-release: downloading $ASSET"
curl -fsSL "$BASE_URL/$ASSET" -o "$TMP/$ASSET"
curl -fsSL "$BASE_URL/SHA256SUMS" -o "$TMP/SHA256SUMS"

line=$(grep "  $ASSET$" "$TMP/SHA256SUMS" || true)
if [ -z "$line" ]; then
    echo "install-release: $ASSET is not listed in SHA256SUMS" >&2
    exit 1
fi
want=$(printf '%s\n' "$line" | awk '{print $1}')
if command -v sha256sum >/dev/null 2>&1; then
    got=$(sha256sum "$TMP/$ASSET" | awk '{print $1}')
else
    got=$(shasum -a 256 "$TMP/$ASSET" | awk '{print $1}')
fi
if [ "$want" != "$got" ]; then
    echo "install-release: checksum mismatch for $ASSET" >&2
    exit 1
fi
if [ "$CHECK_ONLY" = "1" ]; then
    echo "install-release: $ASSET verified"
    exit 0
fi

mkdir -p "$TMP/unpack"
tar -xzf "$TMP/$ASSET" -C "$TMP/unpack"
BIN=$(find "$TMP/unpack" -type f -name athanor | head -1)
if [ -z "$BIN" ]; then
    echo "install-release: no athanor binary in $ASSET" >&2
    exit 1
fi
mkdir -p "$PREFIX"
install -m 0755 "$BIN" "$PREFIX/athanor"

mkdir -p "$CONFIG_DIR"
if [ ! -f "$CONFIG_DIR/config.yaml" ]; then
    "$PREFIX/athanor" init -core -out "$CONFIG_DIR/config.yaml" \
        -jobpod-image "$JOBPOD_IMAGE" >/dev/null
fi

if ! podman image exists "$CORE_IMAGE"; then
    if [ "$PULL_IMAGES" = "1" ]; then
        podman pull "$CORE_IMAGE"
    else
        echo "install-release: missing $CORE_IMAGE — pull it or rerun with -pull-images" >&2
        exit 1
    fi
fi
if ! podman image exists "$JOBPOD_IMAGE"; then
    if [ "$PULL_IMAGES" = "1" ]; then
        podman pull "$JOBPOD_IMAGE"
    else
        echo "install-release: missing $JOBPOD_IMAGE — pull it or rerun with -pull-images" >&2
        exit 1
    fi
fi

"$PREFIX/athanor" start -skip-doctor -config "$CONFIG_DIR/config.yaml" \
    -core-image "$CORE_IMAGE" >/dev/null
"$PREFIX/athanor" doctor -config "$CONFIG_DIR/config.yaml" || true

echo "install-release: installed $VERSION to $PREFIX/athanor"
echo "next: pull persona models with: ollama pull <model>"
echo "UI: http://127.0.0.1:7420/ui"
