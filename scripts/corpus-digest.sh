#!/bin/sh
# Corpus digest — pins the frozen benchmark task set (ADR-0075).
#
# The digest covers the machine-readable task set and every fixture: the three
# YAML files (tasks, memory scripts, human review) and eval/bench/fixtures/.
# It deliberately excludes README.md (documentation) and reference.yaml
# (validation asset): editing prose or a reference solution is not a benchmark
# change, but editing a task, check, or fixture is.
#
# Usage: scripts/corpus-digest.sh write|check
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

digest_file=eval/bench/CORPUS.digest

hash_file() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1"
	else
		sha256sum "$1"
	fi
}

manifest() {
	find eval/bench -type f \
		! -name 'CORPUS.digest' \
		! -name 'reference.yaml' \
		\( -name '*.yaml' -o -path 'eval/bench/fixtures/*' \) \
		| LC_ALL=C sort \
		| while IFS= read -r f; do
			hash_file "$f"
		done
}

case "${1:-check}" in
write)
	manifest >"$digest_file"
	echo "wrote $digest_file"
	;;
check)
	if [ ! -f "$digest_file" ]; then
		echo "missing $digest_file (run: make corpus-digest)" >&2
		exit 1
	fi
	tmp=$(mktemp)
	trap 'rm -f "$tmp"' EXIT
	manifest >"$tmp"
	if diff -u "$digest_file" "$tmp" >/dev/null; then
		echo "corpus digest OK"
	else
		echo "corpus digest mismatch — the frozen set changed (cut a new version):" >&2
		diff -u "$digest_file" "$tmp" >&2 || true
		exit 1
	fi
	;;
*)
	echo "usage: $0 write|check" >&2
	exit 2
	;;
esac
