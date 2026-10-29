#!/bin/sh
# Core Pod benchmark runtime (F1/F7).
#
# The Core Pod is the only supported runtime. The probe runs INSIDE it: the
# state volume is machine-local (SQLite/WAL over a host bind is not safe on
# macOS), and the probe reaches the daemon at 127.0.0.1:7420. The results tree
# is written to the host-mounted /bench so the host can read the report.
#
# Usage:
#   BENCH_OUT=<dir> BENCH_CONFIG=<dir>/core-config.yaml ./scripts/bench-core.sh start
#   BENCH_OUT=<dir> ./scripts/bench-core.sh probe -corpus eval/bench/tasks.yaml ...
#   ./scripts/bench-core.sh stop
#   ./scripts/bench-core.sh status
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
out=${BENCH_OUT:-$root/tools/bench/results/bench-v1}
# Podman treats a relative -v source as a named volume, and volume names cannot
# contain slashes. Normalize the output/config paths before any mount.
mkdir -p "$out"
case "$out" in
/*) ;;
*) out=$(CDPATH= cd -- "$out" && pwd) ;;
esac
config=${BENCH_CONFIG:-$out/core-config.yaml}
case "$config" in
/*) ;;
*) config=$(CDPATH= cd -- "$(dirname -- "$config")" && pwd)/$(basename -- "$config") ;;
esac
state_vol=${BENCH_STATE_VOLUME:-athanor-bench-state}
image=${PROBE_IMAGE:-athanor-bench:latest}
name=${BENCH_CORE_NAME:-athanor-bench-core}
probe_out=${BENCH_PROBE_OUT:-/bench/out}
bench_dir=${BENCH_DIR:-eval/bench/v2}

machine_sock() {
	if [ "$(uname -s)" = Darwin ]; then
		printf '%s/podman/podman.sock\n' "$(podman machine ssh 'printf %s "$XDG_RUNTIME_DIR"')"
	else
		printf '%s/podman/podman.sock\n' "${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
	fi
}

wait_health() {
	i=0
	while [ "$i" -lt 60 ]; do
		if curl -fsS --max-time 2 http://127.0.0.1:7420/healthz >/dev/null 2>&1; then
			return 0
		fi
		i=$((i + 1))
		sleep 1
	done
	echo "bench-core: Core Pod did not become healthy on 127.0.0.1:7420" >&2
	return 1
}

case "${1:-}" in
start)
	if [ ! -f "$config" ]; then
		echo "bench-core: missing config $config (run make bench-config)" >&2
		exit 1
	fi
	mkdir -p "$out/tokens" "$out/scratch" "$out/workspace" "$out/out"
	podman volume inspect "$state_vol" >/dev/null 2>&1 ||
		podman volume create "$state_vol" >/dev/null
	sock=$(machine_sock)
	# A start is a fresh boot of the Core; the state volume is preserved. Do not
	# run this while a probe is in flight.
	podman rm -f "$name" >/dev/null 2>&1 || true
	podman run -d --name "$name" --security-opt label=disable \
		-p 127.0.0.1:7420:7420 \
		-v "$sock:/run/podman/podman.sock" \
		-v "$config:/etc/athanor/config.yaml:ro" \
		-v "$state_vol:/var/lib/athanor/state" \
		-v "$out/tokens:/var/lib/athanor/state/tokens" \
		-v "$out/scratch:/var/lib/athanor/state/scratch" \
		-v "$out/workspace:/var/lib/athanor/workspace" \
		-v "$out:/bench" \
		-v "$root:/bench/repo:ro" \
		"$image" >/dev/null
	wait_health
	echo "bench-core: Core Pod healthy at http://127.0.0.1:7420"
	;;
probe)
	shift
	podman exec -w /bench/repo "$name" /usr/local/bin/athanor-probe soak \
		-external -addr 127.0.0.1:7420 -state-dir /var/lib/athanor/state \
		-out "$probe_out" "$@"
	;;
memory)
	shift
	podman exec -w /bench/repo "$name" /usr/local/bin/athanor-probe memory \
		-scripts "$bench_dir/memory-scripts.yaml" -addr 127.0.0.1:7420 \
		-state-dir /var/lib/athanor/state -out "$probe_out" "$@"
	;;
stop)
	podman stop --time 20 "$name"
	;;
status)
	curl -fsS --max-time 2 http://127.0.0.1:7420/healthz
	;;
*)
	echo "usage: $0 start|probe|stop|status" >&2
	exit 2
	;;
esac
