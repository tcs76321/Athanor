#!/usr/bin/env sh
# Run a focused subset of the frozen benchmark inside the Core Pod (the
# supported runtime, ADR-0078). This is a diagnostic run shape, not the full
# benchmark: it builds the Core + probe images, starts a fresh state volume,
# runs one pass over BENCH_ONLY, and stops the container.
#
# Required: BENCH_OUT (a writable output dir). Optional:
#   BENCH_MODEL (default ornith9b), BENCH_JUDGE (default granite4.2:3b),
#   BENCH_ONLY (comma-separated task ids), BENCH_HOURS (default 1.5),
#   BENCH_TIMEOUT (default 20m), BENCH_STATE_VOLUME.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
OUT=${BENCH_OUT:?set BENCH_OUT to a writable results directory}
MODEL=${BENCH_MODEL:-ornith9b}
JUDGE=${BENCH_JUDGE:-granite4.2:3b}
ONLY=${BENCH_ONLY:-x-code-repo-ratelimit,x-code-bugfix-pipeline,x-code-interpreter,x-code-diff3,x-code-threadpool,x-code-sqlite-migration}
HOURS=${BENCH_HOURS:-1.5}
TIMEOUT=${BENCH_TIMEOUT:-20m}
VOLUME=${BENCH_STATE_VOLUME:-athanor-bench-focused-state}
PROBE_OUT=${BENCH_PROBE_OUT:-/bench/out-focused}

mkdir -p "$OUT"
case "$OUT" in
    /*) ;;
    *) OUT=$(CDPATH= cd -- "$OUT" && pwd) ;;
esac

stop() {
    BENCH_OUT="$OUT" "$ROOT/scripts/bench-core.sh" stop >/dev/null 2>&1 || true
}
trap stop EXIT INT TERM

BENCH_OUT="$OUT" BENCH_MODEL="$MODEL" BENCH_JUDGE="$JUDGE" \
    make -C "$ROOT" bench-image bench-config

# A focused run must start from a clean state volume; a reused volume resumes
# old rows and makes the delta ambiguous.
podman volume rm -f "$VOLUME" >/dev/null 2>&1 || true

mkdir -p "$OUT/out-focused"
BENCH_OUT="$OUT" BENCH_STATE_VOLUME="$VOLUME" "$ROOT/scripts/bench-core.sh" start
BENCH_OUT="$OUT" BENCH_STATE_VOLUME="$VOLUME" BENCH_PROBE_OUT="$PROBE_OUT" \
    "$ROOT/scripts/bench-core.sh" probe \
    -corpus eval/bench/v2/tasks.yaml -model "$MODEL" -arm full \
    -judge-model "$JUDGE" -passes 1 -hours "$HOURS" -timeout "$TIMEOUT" \
    -only "$ONLY"

stop
trap - EXIT INT TERM
echo "bench-core-focused: results at $OUT$PROBE_OUT"
