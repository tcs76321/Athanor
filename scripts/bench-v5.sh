#!/bin/sh
# v5 benchmark runner — one generator at a time, resumable.
#
# Usage: scripts/bench-v5.sh <model-label> [root]
#   model-label: bonsai27b | gemma12b
#   root:        results root (default tools/bench/results/v5)
#
# Layout:
#   <root>/<model>/soak/<suite>/<arm>/out/...
#   <root>/<model>/cycle/cycle-<model>.json
#   <root>/<model>/quality-<arm>-scores.json
#
# Each (suite, arm) gets its own Core Pod state volume so a failed run cannot
# contaminate the next. `out/complete` makes a rerun skip finished suites.
set -eu

model=${1:?usage: bench-v5.sh <bonsai27b|gemma12b> [root]}
root=${2:-tools/bench/results/v5}
dir="$root/$model"
corpus=eval/bench/v5/v5.yaml
ollama=${BENCH_OLLAMA:-http://host.containers.internal:11435}
hours=${BENCH_V5_HOURS:-4}
timeout=${BENCH_V5_TIMEOUT:-40m}

mkdir -p "$dir"

run_suite() {
	suite=$1
	arm=$2
	passes=$3
	out="$dir/soak/$suite/$arm"
	if [ -f "$out/out/complete" ]; then
		echo "bench-v5: $model/$suite/$arm already complete"
		return 0
	fi
	mkdir -p "$out"
	cfg="$out/config.yaml"
	echo "=== v5 $model $suite/$arm ==="
	make bench-config \
		BENCH_ARM="$arm" BENCH_MODEL="$model" BENCH_JUDGE=granite4.2:8b \
		BENCH_OUT="$out" BENCH_CONFIG="$cfg" \
		BENCH_OLLAMA="$ollama" BENCH_ALLOW=sqlite.org >/dev/null
	BENCH_OUT="$out" BENCH_CONFIG="$cfg" \
		BENCH_STATE_VOLUME="athanor-v5-$model-$suite-$arm" \
		PROBE_IMAGE=athanor-bench:latest BENCH_OLLAMA="$ollama" \
		./scripts/bench-core.sh start
	BENCH_OUT="$out" BENCH_PROBE_OUT=/bench/out BENCH_OLLAMA="$ollama" \
		./scripts/bench-core.sh probe -corpus "$corpus" \
		-model "$model" -arm "$arm" -judge-model granite4.2:8b \
		-suite "$suite" -passes "$passes" -hours "$hours" -timeout "$timeout"
	BENCH_OUT="$out" ./scripts/bench-core.sh stop || true
}

# single arm: breadth + safety + memory + quality baseline + utility baseline
for suite in foundation agency safety memory quality utility; do
	passes=1
	[ "$suite" = agency ] && passes=2
	run_suite "$suite" single "$passes"
done

# The cycle arm runs against the utility/single Core? That Core is stopped by
# run_suite, so run the cycles in their own lifecycle.
if [ ! -f "$dir/cycle/complete" ]; then
	mkdir -p "$dir/cycle"
	cfg="$dir/cycle/config.yaml"
	make bench-config BENCH_ARM=single BENCH_MODEL="$model" BENCH_JUDGE=granite4.2:8b \
		BENCH_OUT="$dir/cycle" BENCH_CONFIG="$cfg" BENCH_OLLAMA="$ollama" \
		BENCH_ALLOW=sqlite.org >/dev/null
	BENCH_OUT="$dir/cycle" BENCH_CONFIG="$cfg" \
		BENCH_STATE_VOLUME="athanor-v5-$model-cycle" \
		PROBE_IMAGE=athanor-bench:latest BENCH_OLLAMA="$ollama" \
		./scripts/bench-core.sh start
	BENCH_OUT="$dir/cycle" BENCH_PROBE_OUT=/bench/out \
		./scripts/bench-core.sh cycle -corpus "$corpus" -suite utility \
		-model "$model" -timeout "$timeout"
	BENCH_OUT="$dir/cycle" ./scripts/bench-core.sh stop || true
	touch "$dir/cycle/complete"
fi

# full arm: the quality ceiling and non-inferiority comparison
for suite in foundation agency quality; do
	passes=1
	[ "$suite" = agency ] && passes=2
	run_suite "$suite" full "$passes"
done

# Offline two-judge quality scores for each quality arm.
for arm in single full; do
	arts="$dir/soak/quality/$arm/out/artifacts.json"
	if [ -f "$arts" ] && [ ! -f "$dir/quality-$arm-scores.json" ]; then
		CGO_ENABLED=1 go run -tags sqlite_fts5 ./tools/bench quality \
			-in "$arts" -rubric eval/bench/v3/quality-rubric.yaml \
			-judges "granite4.2:8b,mimo-v2.6:9b-mxfp8" \
			-ollama http://localhost:11434 \
			-out "$dir/quality-$arm-scores.json" || true
	fi
done

# Pairwise quality is the v5 claim instrument. The pointwise scores above are
# retained only as reference.
single_arts="$dir/soak/quality/single/out/artifacts.json"
full_arts="$dir/soak/quality/full/out/artifacts.json"
if [ -f "$single_arts" ] && [ -f "$full_arts" ] && [ ! -f "$dir/quality-pairwise.json" ]; then
	CGO_ENABLED=1 go run -tags sqlite_fts5 ./tools/bench quality-pairwise \
		-single "$single_arts" -full "$full_arts" -corpus "$corpus" \
		-judges "granite4.2:8b,mimo-v2.6:9b-mxfp8" \
		-ollama http://localhost:11434 \
		-out "$dir/quality-pairwise.json" || true
fi

# External validation track: official Exercism tests, custom Athanor adapter.
# Kept separate from the frozen claim suites; the report names the track version.
# The external track uses the single arm: it is the affordable, comparable shape
# (the full arm exceeded the 2h budget at 6/8 exercises).
if [ ! -f "$dir/aider/complete" ]; then
	mkdir -p "$dir/aider"
	AIDER_OUT="$dir/aider" BENCH_OLLAMA="$ollama" \
		scripts/bench-aider-polyglot.sh "$model" single \
		two-fer,leap,pangram,isogram,raindrops,rna-transcription,acronym,hamming
	touch "$dir/aider/complete"
fi

python3 eval/bench/v5/analyze.py -root "$dir" -model "$model" \
	-json "$dir/v5-report.json" -md "$dir/v5-report.md"

echo "bench-v5: $model done — $dir/v5-report.md"
