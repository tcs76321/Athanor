# Athanor development shortcuts.
#
# CGO is REQUIRED for all builds: internal/store uses mattn/go-sqlite3 so
# sqlite-vec can be loaded at runtime later (see docs/sqlite-setup.md and
# docs/adr/0003). Never run plain `go build` / `go test` directly; use these
# targets so CGO_ENABLED is set consistently.

CGO_ENABLED = 1
export CGO_ENABLED

.PHONY: build test test-race test-integration integration-images vet lint check tidy run clean hooks bench

build:
	go build -o bin/athanor ./cmd/athanor

run:
	go run ./cmd/athanor

test:
	go test ./...

test-race:
	go test -race ./...

# Integration tests are opt-in (gated by ATHANOR_RUN_INTEGRATION=1 inside
# the test files). They shell out to a real `podman` binary and, for the
# gateway probes, reach the real internet, so they are NOT run by
# `make check` and never block the fast CI jobs.
#
# Two packages are covered:
#   internal/jobpod   — the five M2 hardening probes + the M2-T4b exec probe
#   internal/gateway  — the two M4-T8 gateway probes (default-deny + allowlisted)
test-integration:
	ATHANOR_RUN_INTEGRATION=1 go test -race -count=1 ./internal/jobpod/... ./internal/gateway/...

# Pull the images the integration probes need. The security probes use
# alpine:3.20; the M2-T4b exec probe uses python:3.12-alpine (ADR-0024 §6).
# A probe that silently pulls a large image on every run is a slow
# surprise, so the images are pulled once, explicitly, by this target.
integration-images:
	podman pull alpine:3.20
	podman pull python:3.12-alpine

vet:
	go vet ./...

lint:
	golangci-lint run --timeout=5m

# Engine throughput baseline. Runs the M1 full-chain benchmark 10x
# against a fake Ollama (no network). The result is the comparison
# point for M3's multi-candidate evaluation: M3 should be slower per
# job (N candidates per phase) but the per-phase cost should remain
# in the same order of magnitude. Numbers from the first run are
# recorded in docs/benchmarks/engine-m1.txt.
bench:
	go test -bench=. -benchtime=10x -run=^$$ ./internal/engine/

# Aggregate gate; run before pushing. The pre-push hook also calls this.
check: lint vet test-race

tidy:
	go mod tidy

clean:
	rm -rf state backups

# Install the pre-push hook so CI lint/vet failures surface locally before
# the push lands. Bypass with: git push --no-verify
hooks:
	bash scripts/install-hooks.sh
