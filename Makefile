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

.PHONY: build test test-race test-integration integration-images vet lint check vuln tidy tidy-check fuzz cover run clean hooks bench probe-micro probe probe-judge probe-anchor probe-report probe-bundle probe-reconcile jobpod-image install soak

build:
	go build $(GO_TAGS) -o bin/athanor ./cmd/athanor

# Job Pod base image (M7-T7). Build once per machine; set job_pod.image to
# $(JOBPOD_IMAGE) in config.yaml. Hardened at run time by the pod manager.
JOBPOD_IMAGE ?= athanor-jobpod:latest
jobpod-image:
	podman build -t $(JOBPOD_IMAGE) -f deploy/jobpod.Containerfile deploy

# Install the binary (and, optionally, a headless service unit): `make install`
# or `sh scripts/install.sh --service` (M7-T7/T8).
install:
	sh scripts/install.sh

# M7-T9: the 24h soak harness. Human-run; see docs/soak-m7.md. Repeats the
# quality-probe workload (which samples RSS/DB/disk to soak.csv) for SOAK_HOURS.
SOAK_OUT ?= spikes/m3-t7-probe/results/soak
soak:
	SOAK_OUT=$(SOAK_OUT) sh scripts/soak.sh

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
PROBE_OUT ?= spikes/m3-t7-probe/results

probe-micro:
	go run $(GO_TAGS) ./spikes/m3-t7-probe run -only local-first-essay,md2html-readme,todo-list -out $(PROBE_OUT)

probe:
	go run $(GO_TAGS) ./spikes/m3-t7-probe run -out $(PROBE_OUT)

probe-judge:
	go run $(GO_TAGS) ./spikes/m3-t7-probe judge -out $(PROBE_OUT)

# F4-T4: score the human-anchor set and report per-judge reliability + rank
# agreement. Needs the judge models pulled locally.
probe-anchor:
	go run $(GO_TAGS) ./spikes/m3-t7-probe anchor -out $(PROBE_OUT)/anchor

probe-report:
	go run $(GO_TAGS) ./spikes/m3-t7-probe report -out $(PROBE_OUT)

# Repair results.json rows the live collector missed (code-goal outcomes land
# ~10s after the terminal transition). Run after `probe`, before `probe-report`.
probe-reconcile:
	go run $(GO_TAGS) ./spikes/m3-t7-probe reconcile -out $(PROBE_OUT)

probe-bundle:
	go run $(GO_TAGS) ./spikes/m3-t7-probe bundle -out $(PROBE_OUT)

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
