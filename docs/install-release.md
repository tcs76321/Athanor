# Installing a built release (F8 first slice)

The supported runtime is the **Core Pod** ([ADR-0078](adr/0078-core-pod-only-runtime.md)).
This page covers the prebuilt-binary path; the source-clone path remains
`scripts/install.sh`.

## Build a release

```bash
make release VERSION=0.0.0
```

This writes:

```text
dist/athanor-0.0.0-<os>-<arch>.tar.gz
dist/SHA256SUMS
```

The tarball contains the host CLI, `config.example.yaml`, `LICENSE`, and
`install-release.sh`. It is **host-native**: CGO (SQLite, tree-sitter) makes
cross-compilation a separate toolchain decision. Build each OS/arch on its own
runner, or pass `GOOS`/`GOARCH` only where a C toolchain exists.

## Install from a release

```bash
sh install-release.sh \
  -version 0.0.0 \
  -base-url https://example.invalid/athanor/releases
```

The installer:

1. downloads `athanor-0.0.0-<os>-<arch>.tar.gz` and `SHA256SUMS`;
2. verifies the SHA-256 before unpacking;
3. installs the binary (default `/usr/local/bin/athanor`);
4. writes `~/.config/athanor/config.yaml` with `athanor init -core` if absent;
5. checks (or pulls, with `-pull-images`) the Core and Job Pod images;
6. starts the Core Pod and runs `athanor doctor` (advisory).

`-check-only` verifies the download and exits; it is the release smoke used in
tests. Model pulls remain explicit — the script prints the `ollama pull` step
rather than downloading tens of GB silently.

## What is not in this slice

- Signatures. SHA-256 detects corruption, not a compromised release host; a
  signature/`cosign`/`minisign` step is a 1.0.0 distribution item.
- Registry publishing. The image names are operator-provided; the installer can
  pull them but does not push or host them.
- Auto-update/rollback. Both are 1.0.0 distribution work.
