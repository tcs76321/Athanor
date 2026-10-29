# Athanor development shortcuts.
#
# CGO is REQUIRED for all builds: internal/store uses mattn/go-sqlite3 so
# sqlite-vec can be loaded at runtime later (see docs/sqlite-setup.md and
# docs/adr/0003). Never run plain `go build` / `go test` directly; use these
# targets so CGO_ENABLED is set consistently.

CGO_ENABLED = 1
export CGO_ENABLED

# sqlite_fts5 compiles FTS5 into the mattn/go-sqlite3 amalgamation
# (docs/sqlite-setup.md; ADR-0026 §1). Every build and test invocation
# carries it: the MCE retrieval index (migration 0013) is an FTS5 virtual
# table, and store.CheckFTS5 fails loudly at boot without the tag, so a
# tag-less binary can never silently run.
GO_TAGS = -tags sqlite_fts5

.PHONY: build test test-race test-integration integration-images vet lint check vuln tidy tidy-check fuzz cover cover-check run clean hooks bench probe-micro probe probe-judge probe-anchor probe-report probe-bundle probe-reconcile probe-measure jobpod-image core-image core-smoke bench-core-focused install soak soak-mini soak-ladder

build:
	go build $(GO_TAGS) -o bin/athanor ./cmd/athanor

# Job Pod base image (M7-T7). Build once per machine; set job_pod.image to
# $(JOBPOD_IMAGE) in config.yaml. Hardened at run time by the pod manager.
JOBPOD_IMAGE ?= athanor-jobpod:latest
jobpod-image:
	podman build -t $(JOBPOD_IMAGE) -f deploy/jobpod.Containerfile deploy

# Core Pod image (ADR-0069; M9-T5). The build context is the repo root (a
# multi-stage build compiles the daemon inside the image). At run time the image
# holds the Podman socket to spawn Job Pods and exposes the loopback UI on 7420;
# multi-arch is assembled with a manifest (see deploy/core.Containerfile).
CORE_IMAGE ?= athanor-core:latest
VERSION ?= 0.0.0-dev
core-image:
	podman build -t $(CORE_IMAGE) --build-arg VERSION=$(VERSION) -f deploy/core.Containerfile .

# Core Pod smoke (P1): boot the supported runtime, wait for health, exercise
# one metadata-only project create and the UI, then tear down. No Ollama and no
# Job Pod are required. CI runs this after building both images.
core-smoke:
	sh scripts/core-smoke.sh

# Install the binary (and, optionally, a headless service unit): `make install`
# or `sh scripts/install.sh --service` (M7-T7/T8).
install:
	sh scripts/install.sh

# M7-T9: the 24h soak harness. Human-run; see docs/soak-m7.md. Repeats the
# quality-probe workload (which samples RSS/DB/disk to soak.csv) for SOAK_HOURS.
SOAK_OUT ?= tools/bench/results/soak
soak:
	SOAK_OUT=$(SOAK_OUT) sh scripts/soak.sh

# ADR-0073 mini soak: a 1-hour validation run on the frozen build. It exercises
# the whole harness end to end — the quality workload plus the telemetry,
# leaderboard, and conformance-scenario readouts — with one crash-recovery
# injection. The model is the fast 9B by default; the judge is cross-family.
# Use a FRESH MINI_SOAK_OUT for a clean run (a reused dir resumes from its
# deadline + metrics). See docs/soak-m7.md.
MINI_SOAK_OUT   ?= tools/bench/results/mini-soak
MINI_SOAK_MODEL ?= ornith9b
MINI_SOAK_JUDGE ?= granite4.2:3b
MINI_SOAK_HOURS ?= 1
MINI_SOAK_KILL  ?= 20m
# MINI_SOAK_TIMEOUT bounds one job (the soak's -timeout). A degenerate model can
# otherwise spend 45m on a single task and blow a short run's budget.
MINI_SOAK_TIMEOUT ?= 45m
# Benchmark version root. v2 is the 0.0.0 hard H/X corpus; use
# BENCH_DIR=eval/bench for the v1 pilot.
BENCH_DIR ?= eval/bench/v2
# MINI_SOAK_ONLY narrows the corpus to a comma-separated task list (empty = all).
MINI_SOAK_ONLY  ?=
# MINI_SOAK_PASSES caps full corpus passes (empty = until the deadline);
# 1 = one full slice. MINI_SOAK_ANCHOR narrows to an anchor YAML.
MINI_SOAK_PASSES ?=
MINI_SOAK_ANCHOR ?=
MINI_SOAK_ARM    ?= full
MINI_SOAK_CORPUS ?= $(BENCH_DIR)/tasks.yaml
soak-mini:
	go run $(GO_TAGS) ./tools/bench soak \
		-model $(MINI_SOAK_MODEL) -arm $(MINI_SOAK_ARM) -corpus $(MINI_SOAK_CORPUS) \
		-judge-model $(MINI_SOAK_JUDGE) -hours $(MINI_SOAK_HOURS) \
		-timeout $(MINI_SOAK_TIMEOUT) \
		-kill-after $(MINI_SOAK_KILL) $(if $(MINI_SOAK_ONLY),-only $(MINI_SOAK_ONLY)) \
		$(if $(MINI_SOAK_PASSES),-passes $(MINI_SOAK_PASSES)) \
		$(if $(MINI_SOAK_ANCHOR),-anchor-tasks $(MINI_SOAK_ANCHOR)) \
		-out $(MINI_SOAK_OUT)

# A time-boxed ladder across every matrix model: one soak per model (single-slot
# residency) into a common tree, then one merged report. A short per-model budget
# yields a PARTIAL pass per model — enough for the comparison leaderboard, not a
# full-corpus result. The soak deadline is soft (it finishes the in-flight task),
# so budget ~1.3x; kill injection is OFF so models see identical conditions.
# Use a FRESH SOAK_LADDER_OUT for a clean ladder (a reused tree resumes per model).
SOAK_LADDER_HOURS  ?= 0.5
SOAK_LADDER_OUT    ?= tools/bench/results/ladder
SOAK_LADDER_ONLY   ?=
# One job is bounded so a degenerate model cannot consume the whole per-model
# budget on a single task (the 0.0.0 ladder saw a 33-minute verdict loop).
SOAK_LADDER_TIMEOUT ?= 10m
SOAK_LADDER_MODELS ?= granite3b gemmae4b granite8b ornith9b gemma12b qwen27b granite30b nemotron30b muse30b ornith35b
soak-ladder:
	@for m in $(SOAK_LADDER_MODELS); do \
		if [ -f "$(SOAK_LADDER_OUT)/$$m/complete" ]; then \
			echo "=== ladder $$m: already complete, skipping ==="; continue; \
		fi; \
		echo "=== ladder $$m ($(SOAK_LADDER_HOURS)h) ==="; \
		$(MAKE) soak-mini MINI_SOAK_MODEL=$$m MINI_SOAK_HOURS=$(SOAK_LADDER_HOURS) \
			MINI_SOAK_KILL=0 MINI_SOAK_TIMEOUT=$(SOAK_LADDER_TIMEOUT) \
			MINI_SOAK_ONLY=$(SOAK_LADDER_ONLY) \
			MINI_SOAK_OUT=$(SOAK_LADDER_OUT)/$$m || exit 1; \
	done
	$(MAKE) probe-report PROBE_OUT=$(SOAK_LADDER_OUT)

# Corpus freeze (ADR-0075). The digest covers tasks.yaml, memory-scripts.yaml,
# human-review.yaml, and every fixture — not the README or reference.yaml.
# Discipline, not CI: a mismatch means the frozen set changed, which is a new
# benchmark version, not an edit. `make corpus-digest` re-pins deliberately.
corpus-check:
	@./scripts/corpus-digest.sh check

corpus-digest:
	@./scripts/corpus-digest.sh write

# v2 (0.0.0 hard H/X corpus). v1 remains checkable with `make corpus-check`.
corpus-check-v2:
	@./scripts/corpus-digest.sh check eval/bench/v2

corpus-digest-v2:
	@./scripts/corpus-digest.sh write eval/bench/v2

# The 6-item smoke subset (docs/frozen-corpus-v1.md §8) for engine-surface
# changes. A fresh output dir by default; set SOAK_SMOKE_OUT to keep the run.
SOAK_SMOKE_OUT  ?= tools/bench/results/smoke
SOAK_SMOKE_ONLY ?= code-bank-atomic,code-expr-eval,x-code-interpreter,doc-cited-brief,text-level-rewrite,data-json-normalize
soak-smoke:
	$(MAKE) soak-mini MINI_SOAK_ONLY=$(SOAK_SMOKE_ONLY) MINI_SOAK_OUT=$(SOAK_SMOKE_OUT)

# Benchmark run shapes (eval/bench/protocol.yaml). The full run is ONE canonical
# persona set, one pass, <=12h. The anchor is the short onboarding/leaderboard
# screen (12 tasks x pinned roster), also <=12h. The leaderboard is an
# aggregation of anchor runs, not the headline. Both verify the model digests
# first; a changed model is a changed benchmark, not a sloppy run.
BENCH_MODEL        ?= ornith9b
BENCH_JUDGE        ?= granite4.2:3b
BENCH_OUT          ?= tools/bench/results/bench-v2
BENCH_CONFIG       ?= $(BENCH_OUT)/core-config.yaml
BENCH_STATE_VOLUME ?= athanor-bench-v2-state
PROBE_IMAGE        ?= athanor-bench:latest
BENCH_HOURS        ?= 12
BENCH_TIMEOUT      ?= 45m
BENCH_ANCHOR_HOURS ?= 1
BENCH_ANCHOR_MODELS ?= granite3b gemmae4b granite8b ornith9b gemma12b qwen27b granite30b nemotron30b muse30b ornith35b

bench-verify:
	go run $(GO_TAGS) ./tools/bench verify-models -protocol $(BENCH_DIR)/protocol.yaml

# The Core Pod image plus the probe binary (deploy/bench.Containerfile). The
# final image is the Core image with /usr/local/bin/athanor-probe added.
bench-image: core-image
	podman build -t $(PROBE_IMAGE) -f deploy/bench.Containerfile .

# Generate the canonical Core Pod config. Always regenerated: a model change
# must never silently reuse a stale config.
bench-config:
	@mkdir -p $(BENCH_OUT)
	CGO_ENABLED=1 go run $(GO_TAGS) ./tools/bench config \
		-model $(BENCH_MODEL) -arm full -judge-model $(BENCH_JUDGE) \
		-ollama http://host.containers.internal:11434 -addr 127.0.0.1:7420 \
		-jobpod-image athanor-jobpod:latest \
		-token-host-dir $(abspath $(BENCH_OUT))/tokens \
		-scratch-host-dir $(abspath $(BENCH_OUT))/scratch -soak > $(BENCH_CONFIG)

# The full benchmark on the shipped runtime: ONE canonical persona set, one
# pass, <=12h hard cap. The probe runs inside the Core Pod and reads the named
# state volume; the results tree lands in BENCH_OUT/out on the host.
bench-full: bench-verify bench-image bench-config
	@if [ -f "$(BENCH_OUT)/out/complete" ]; then \
		echo "=== full benchmark already complete: $(BENCH_OUT)/out ==="; exit 0; \
	fi
	@if [ -d "$(BENCH_OUT)/out" ] && [ -n "$$(ls -A $(BENCH_OUT)/out 2>/dev/null)" ]; then \
		echo "=== incomplete full run at $(BENCH_OUT)/out; remove it to restart ===" >&2; exit 1; \
	fi
	@BENCH_OUT=$(BENCH_OUT) BENCH_CONFIG=$(BENCH_CONFIG) BENCH_STATE_VOLUME=$(BENCH_STATE_VOLUME) \
		PROBE_IMAGE=$(PROBE_IMAGE) ./scripts/bench-core.sh start
	@BENCH_OUT=$(BENCH_OUT) BENCH_PROBE_OUT=/bench/out \
		./scripts/bench-core.sh probe -corpus $(BENCH_DIR)/tasks.yaml \
		-model $(BENCH_MODEL) -arm full -judge-model $(BENCH_JUDGE) \
		-passes 1 -hours $(BENCH_HOURS) -timeout $(BENCH_TIMEOUT)

bench-core-stop:
	@BENCH_OUT=$(BENCH_OUT) ./scripts/bench-core.sh stop

bench-core-status:
	@BENCH_OUT=$(BENCH_OUT) ./scripts/bench-core.sh status

# Focused Core Pod diagnostic: one pass over BENCH_ONLY (default: the six
# X-code tasks), fresh state volume, images rebuilt. Set BENCH_OUT to keep the
# results; the default is gitignored.
BENCH_FOCUSED_OUT ?= tools/bench/results/bench-core-focused
bench-core-focused:
	BENCH_OUT=$(BENCH_FOCUSED_OUT) sh scripts/bench-core-focused.sh

# The four memory sequences. The three correction/insight scripts are runnable;
# the compaction script is reported as partial until the filler driver lands.
# The Core Pod must already be up (do not run `start` during a full slice).
bench-memory:
	@BENCH_OUT=$(BENCH_OUT) BENCH_PROBE_OUT=/bench/out-memory BENCH_DIR=$(BENCH_DIR) \
		./scripts/bench-core.sh memory

# Host-binary development path. Unsupported as a measurement runtime (ADR-0078);
# use it for harness work only.
bench-full-host: bench-verify
	@if [ -f "$(BENCH_OUT)/full/complete" ]; then \
		echo "=== full benchmark already complete: $(BENCH_OUT)/full ==="; exit 0; \
	fi
	@if [ -d "$(BENCH_OUT)/full" ]; then \
		echo "=== incomplete full run at $(BENCH_OUT)/full; remove it to restart ===" >&2; exit 1; \
	fi
	$(MAKE) soak-mini MINI_SOAK_MODEL=$(BENCH_MODEL) MINI_SOAK_JUDGE=$(BENCH_JUDGE) \
		MINI_SOAK_HOURS=$(BENCH_HOURS) MINI_SOAK_KILL=0 \
		MINI_SOAK_TIMEOUT=$(BENCH_TIMEOUT) MINI_SOAK_PASSES=1 \
		MINI_SOAK_OUT=$(BENCH_OUT)/full

# Host-binary onboarding/leaderboard screen. The Core Pod version is one model
# per Core restart (single-slot); this stays for quick dev screens.
bench-anchor: bench-verify
	@for m in $(BENCH_ANCHOR_MODELS); do \
		if [ -f "$(BENCH_OUT)/anchor/$$m/complete" ]; then \
			echo "=== anchor $$m: already complete, skipping ==="; continue; \
		fi; \
		if [ -d "$(BENCH_OUT)/anchor/$$m" ]; then \
			echo "=== incomplete anchor $$m; remove $(BENCH_OUT)/anchor/$$m to restart ===" >&2; exit 1; \
		fi; \
		echo "=== anchor $$m (one pass, $(BENCH_ANCHOR_HOURS)h cap) ==="; \
		$(MAKE) soak-mini MINI_SOAK_MODEL=$$m MINI_SOAK_JUDGE=$(BENCH_JUDGE) \
			MINI_SOAK_HOURS=$(BENCH_ANCHOR_HOURS) MINI_SOAK_KILL=0 \
			MINI_SOAK_TIMEOUT=$(BENCH_TIMEOUT) MINI_SOAK_PASSES=1 \
			MINI_SOAK_ARM=single \
			MINI_SOAK_ANCHOR=$(BENCH_DIR)/anchor.yaml \
			MINI_SOAK_OUT=$(BENCH_OUT)/anchor/$$m || exit 1; \
	done
	$(MAKE) probe-report PROBE_OUT=$(BENCH_OUT)/anchor

run:
	go run $(GO_TAGS) ./cmd/athanor

test:
	go test $(GO_TAGS) ./...

test-race:
	go test -race $(GO_TAGS) ./...

# Integration tests are opt-in (gated by ATHANOR_RUN_INTEGRATION=1 inside
# the test files). They shell out to a real `podman` binary and, for the
# gateway probes, reach the real internet, so they are NOT run by
# `make check` and never block the fast CI jobs.
#
# Two packages are covered:
#   internal/jobpod   — the five M2 hardening probes + the M2-T4b exec probe
#   internal/gateway  — the two M4-T8 gateway probes (default-deny + allowlisted)
test-integration:
	ATHANOR_RUN_INTEGRATION=1 go test -race $(GO_TAGS) -count=1 ./internal/jobpod/... ./internal/gateway/...

# Pull the images the integration probes need. The security probes use
# alpine:3.20; the M2-T4b exec probe uses python:3.12-alpine (ADR-0024 §6).
# A probe that silently pulls a large image on every run is a slow
# surprise, so the images are pulled once, explicitly, by this target.
integration-images:
	podman pull alpine:3.20
	podman pull python:3.12-alpine

vet:
	go vet $(GO_TAGS) ./...

lint:
	golangci-lint run --timeout=5m --build-tags sqlite_fts5

# Vulnerability scan (F3-T1). Requires govulncheck:
#   go install golang.org/x/vuln/cmd/govulncheck@latest
# Deliberately NOT part of `make check`: it downloads the vulnerability
# database and is slow, so it runs as its own CI job instead.
vuln:
	govulncheck ./...

# Engine throughput baseline. Runs the M1 full-chain benchmark 10x
# against a fake Ollama (no network). The result is the comparison
# point for M3's multi-candidate evaluation: M3 should be slower per
# job (N candidates per phase) but the per-phase cost should remain
# in the same order of magnitude. Numbers from the first run are
# recorded in docs/benchmarks/engine-m1.txt.
bench:
	go test $(GO_TAGS) -bench=. -benchtime=10x -run=^$$ ./internal/engine/

# M3-T7 quality probe (docs/probes/m3-t7-quality-probe.md). The runner
# manages the daemon lifecycle itself, so it needs the daemon binary
# (make build) and a running Ollama. Override the results dir with
# PROBE_OUT=/path. `probe-micro` is the fast 3-goal (text/document/code)
# set; `probe` is the full 10-goal matrix; `probe-judge` scores the
# collected packets with the third-party judges; `probe-report` aggregates.
PROBE_OUT ?= tools/bench/results

probe-micro:
	go run $(GO_TAGS) ./tools/bench run -only local-first-essay,md2html-readme,todo-list -out $(PROBE_OUT)

probe:
	go run $(GO_TAGS) ./tools/bench run -out $(PROBE_OUT)

probe-judge:
	go run $(GO_TAGS) ./tools/bench judge -out $(PROBE_OUT)

# F4-T4: score the human-anchor set and report per-judge reliability + rank
# agreement. Needs the judge models pulled locally.
probe-anchor:
	go run $(GO_TAGS) ./tools/bench anchor -out $(PROBE_OUT)/anchor

# M9-T2b: reduce a results tree + anchor run into onboard measurements.json
# (with Samples), consumable by `athanor models -qualification`.
probe-measure:
	go run $(GO_TAGS) ./tools/bench measure -out $(PROBE_OUT)

probe-report:
	go run $(GO_TAGS) ./tools/bench report -out $(PROBE_OUT)

# Repair results.json rows the live collector missed (code-goal outcomes land
# ~10s after the terminal transition). Run after `probe`, before `probe-report`.
probe-reconcile:
	go run $(GO_TAGS) ./tools/bench reconcile -out $(PROBE_OUT)

probe-bundle:
	go run $(GO_TAGS) ./tools/bench bundle -out $(PROBE_OUT)

# Short fuzz pass (F3-T7) over the security-sensitive parsers. The seed
# corpora also run as ordinary tests under `make test` (Go executes each
# seed when -fuzz is absent), so CI still exercises the inputs; this
# target adds real mutation time. Run a target longer locally when
# touching the corresponding parser.
fuzz:
	go test $(GO_TAGS) -run=^$$ -fuzz=FuzzResolve -fuzztime=10s ./internal/airlock/paths/
	go test $(GO_TAGS) -run=^$$ -fuzz=FuzzParseVerdictJSON -fuzztime=10s ./internal/engine/

# Coverage report (F3-T7). Deliberately no hard floor yet: the value is
# the report while the security-critical packages stabilize. Inspect with
# `go tool cover -html=cover.out`.
cover:
	go test $(GO_TAGS) -coverprofile=cover.out ./...
	go tool cover -func=cover.out | tail -1

# Per-package coverage floor for the security-critical packages (F5). The
# whole-tree number hides a regression in one weakly-covered package; this
# gate pins conservative floors on the packages where a hole matters most.
# Run by the `coverage` CI job and asserted by internal/ci.
cover-check:
	sh scripts/cover-check.sh

# Aggregate gate; run before pushing. The pre-push hook also calls this.
check: lint vet test-race

tidy:
	go mod tidy

# tidy-check fails when go.mod/go.sum would change under `go mod tidy`
# (F3-T1). CI runs it so an untidy module file cannot land silently.
tidy-check:
	go mod tidy
	git diff --exit-code -- go.mod go.sum

clean:
	rm -rf state backups

# Install the pre-push hook so CI lint/vet failures surface locally before
# the push lands. Bypass with: git push --no-verify
hooks:
	bash scripts/install-hooks.sh
