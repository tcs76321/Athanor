#!/bin/sh
# Aider Polyglot (Exercism) smoke — custom Athanor adapter, Python subset.
# Not the official Aider leaderboard. The label is the official test suite run
# in a fresh container after Athanor exits.
#
# Usage: scripts/bench-aider-polyglot.sh [model] [arm] [slugs]
set -eu

model=${1:-bonsai27b}
arm=${2:-single}
slugs=${3:-two-fer,leap,pangram,isogram,raindrops,rna-transcription,acronym,hamming}
source=/private/var/folders/t2/4g71x2ds2w56qdx6bnf80sx40000gn/T/opencode/exercism-python/exercises/practice
run="aider-$(date +%Y%m%d-%H%M%S)-$model-$arm"
work="$PWD/.bench-aider/$run"
ollama=${BENCH_OLLAMA:-http://host.containers.internal:11435}
hours=${AIDER_HOURS:-2}

mkdir -p "$work"
python3 tools/bench/aider_polyglot.py prepare \
	--source "$source" --work "$work" --run "$run" --slugs "$slugs"

make bench-config BENCH_ARM="$arm" BENCH_MODEL="$model" BENCH_JUDGE=granite4.2:8b \
	BENCH_OUT="$work" BENCH_CONFIG="$work/config.yaml" BENCH_OLLAMA="$ollama" \
	BENCH_ALLOW=sqlite.org >/dev/null

BENCH_OUT="$work" BENCH_CONFIG="$work/config.yaml" \
	BENCH_STATE_VOLUME="athanor-$run" PROBE_IMAGE=athanor-bench:latest \
	BENCH_OLLAMA="$ollama" ./scripts/bench-core.sh start
BENCH_OUT="$work" BENCH_PROBE_OUT=/bench/out BENCH_OLLAMA="$ollama" \
	./scripts/bench-core.sh probe -corpus "/bench/repo/.bench-aider/$run/corpus.yaml" \
	-model "$model" -arm "$arm" -suite aider -passes 1 -hours "$hours" -timeout 10m
BENCH_OUT="$work" ./scripts/bench-core.sh stop || true

python3 tools/bench/aider_polyglot.py score \
	--work "$work" --artifacts "$work/out/artifacts.json" \
	--out "$work/report"

outdir=${AIDER_OUT:-$work}
mkdir -p "$outdir"
if [ "$outdir" != "$work" ]; then
	cp "$work/report.md" "$outdir/report.md"
	cp "$work/report.json" "$outdir/report.json"
fi

echo "aider: report at $outdir/report.md"
