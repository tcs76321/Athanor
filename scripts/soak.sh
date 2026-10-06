#!/usr/bin/env sh
# Athanor M7-T9 soak harness.
#
# Repeats the quality-probe workload (which samples RSS / DB size / disk to
# soak.csv and enforces per-job and consecutive-error budgets) until
# SOAK_HOURS have elapsed, then aggregates. This is a long-running, human-run
# checkpoint — see docs/soak-m7.md for prerequisites and acceptance.
set -eu

HOURS="${SOAK_HOURS:-24}"
OUT="${SOAK_OUT:-spikes/m3-t7-probe/results/soak}"

echo "soak: target ${HOURS}h, results under ${OUT}"
mkdir -p "$OUT"

START=$(date +%s)
END=$((START + HOURS * 3600))
i=0
LAST=""
while [ "$(date +%s)" -lt "$END" ]; do
    i=$((i + 1))
    LAST="$OUT/iter-$i"
    echo "soak: iteration $i at $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    if ! go run ./spikes/m3-t7-probe run -out "$LAST"; then
        echo "soak: iteration $i failed; continuing (the runner enforces its own budgets)" >&2
    fi
done

if [ -n "$LAST" ]; then
    go run ./spikes/m3-t7-probe report -out "$LAST" || true
fi
echo "soak: complete after $i iteration(s)"
