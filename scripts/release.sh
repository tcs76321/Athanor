#!/usr/bin/env sh
# Build a host-native Athanor release tarball + SHA256SUMS.
#
# This is the F8 first slice: the binary is built with the same CGO/tag
# discipline as `make build`, packaged with its config example, and checksummed.
# It is deliberately host-native: CGO + tree-sitter + sqlite3 make cross-
# compilation a separate toolchain decision, so build each OS/arch on its own
# runner (or pass GOOS/GOARCH only where a C toolchain exists).
#
# Usage:
#   sh scripts/release.sh [version]
#   VERSION=0.0.0 sh scripts/release.sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
VERSION=${VERSION:-${1:-$(git -C "$ROOT" describe --tags --always --dirty)}}
OS=${GOOS:-$(go env GOOS)}
ARCH=${GOARCH:-$(go env GOARCH)}
DIST=${DIST:-$ROOT/dist}
NAME="athanor-$VERSION-$OS-$ARCH"
OUT="$DIST/$NAME"

cd "$ROOT"
mkdir -p "$DIST"
rm -rf "$OUT"
mkdir -p "$OUT"

echo "release: building $NAME"
CGO_ENABLED=1 go build -tags sqlite_fts5 \
    -ldflags "-X main.version=$VERSION" \
    -o "$OUT/athanor" ./cmd/athanor

cp "$ROOT/config.example.yaml" "$OUT/"
cp "$ROOT/LICENSE" "$OUT/"
cp "$ROOT/scripts/install-release.sh" "$OUT/"

tar -czf "$DIST/$NAME.tar.gz" -C "$DIST" "$NAME"
rm -rf "$OUT"

sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

# One SHA256SUMS per directory; append on repeated builds, replacing an old
# line for the same asset.
SUMS="$DIST/SHA256SUMS"
TMP="$SUMS.tmp"
if [ -f "$SUMS" ]; then
    grep -v "  $NAME.tar.gz$" "$SUMS" > "$TMP" || true
else
    : > "$TMP"
fi
printf '%s  %s\n' "$(sha256 "$DIST/$NAME.tar.gz")" "$NAME.tar.gz" >> "$TMP"
mv "$TMP" "$SUMS"

echo "release: $DIST/$NAME.tar.gz"
echo "release: $SUMS"
