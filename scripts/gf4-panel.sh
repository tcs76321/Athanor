#!/bin/sh
# G-F4 model-panel driver.
#
# Runs the G-F4 generation matrix across the model roster, STRICTLY SERIALLY
# and RESUMABLY, under a global wall budget. Each generator is one `run`
# invocation with a cross-family in-engine judge. The core generator
# (ornith9b) runs first with the full 10-goal set and dialectical runs=3 (for
# T-c stability); the other nine run a fixed 6-goal panel with runs=1; a drift
# re-check of the core runs last (detects 28h system drift).
#
# Safety: refuses to start if a probe run is already active; skips a generator
# whose results.json already exists (resume); stops cleanly when the budget is
# spent; every generator carries -max-wall and -min-free-gb guards.
#
# Usage:
#   BUDGET_HOURS=24 OLLAMA=http://127.0.0.1:11435 sh scripts/gf4-panel.sh
set -eu

OUT=${OUT:-spikes/m3-t7-probe/results/gf4}
OLLAMA=${OLLAMA:-http://127.0.0.1:11435}
BUDGET_HOURS=${BUDGET_HOURS:-24}
PANEL_GOALS="local-first-essay,onboarding-email,book-collection,fibonacci,md2html-readme,sunrise-alarm-brief"
CORE=ornith9b

BUDGET_SECS=$((BUDGET_HOURS * 3600))
START=$(date +%s)

minutes_left() { echo $(( (BUDGET_SECS - ($(date +%s) - START)) / 60 )); }

if pgrep -f 'm3-t7-probe run' >/dev/null 2>&1; then
	echo "gf4-panel: a probe run is already active; refusing to start" >&2
	exit 1
fi

run_gen() {
	label=$1
	judge=$2
	runs=$3
	goals=$4
	if [ -f "$OUT/$label/dialectical/results.json" ] && [ -f "$OUT/$label/single/results.json" ]; then
		echo "gf4-panel: $label already complete — skipping"
		return 0
	fi
	if [ "$(minutes_left)" -le 0 ]; then
		echo "gf4-panel: budget exhausted before $label"
		return 1
	fi
	echo "gf4-panel: $label (judge=$judge runs=$runs goals=${goals:-all}) [$(minutes_left) min left]"
	set -- -models "$label" -dialectical-runs "$runs" -judge-mode verifier \
		-judge-model "$judge" -ollama "$OLLAMA" -out "$OUT" \
		-max-wall 5h -min-free-gb 5
	if [ -n "$goals" ]; then
		set -- "$@" -only "$goals"
	fi
	go run -tags sqlite_fts5 ./spikes/m3-t7-probe run "$@"
}

# Core first: full 10-goal set, dialectical runs=3 (T-a/b/c).
run_gen "$CORE" "gemma4:12b-mlx" 3 "" || true

# Panel: fixed 6-goal set, runs=1. "label|judge|runs".
for entry in \
	"granite3b|gemma4:12b-mlx|1" \
	"gemmae4b|granite4.2:8b|1" \
	"granite8b|gemma4:12b-mlx|1" \
	"gemma12b|granite4.2:8b|1" \
	"qwen27b|gemma4:12b-mlx|1" \
	"granite30b|gemma4:12b-mlx|1" \
	"nemotron30b|gemma4:12b-mlx|1" \
	"muse30b|gemma4:12b-mlx|1" \
	"ornith35b|gemma4:12b-mlx|1"
do
	label=${entry%%|*}
	rest=${entry#*|}
	judge=${rest%%|*}
	runs=${rest##*|}
	run_gen "$label" "$judge" "$runs" "$PANEL_GOALS" || break
done

# Drift re-check: core again, panel goals, runs=1, separate out dir.
DRIFT_OUT="${OUT}-drift"
if [ "$(minutes_left)" -gt 0 ] && [ ! -f "$DRIFT_OUT/$CORE/dialectical/results.json" ]; then
	echo "gf4-panel: drift re-check of $CORE [$(minutes_left) min left]"
	go run -tags sqlite_fts5 ./spikes/m3-t7-probe run \
		-models "$CORE" -dialectical-runs 1 -judge-mode verifier -judge-model gemma4:12b-mlx \
		-ollama "$OLLAMA" -out "$DRIFT_OUT" -only "$PANEL_GOALS" \
		-max-wall 2h -min-free-gb 5
fi

echo "gf4-panel: done"
