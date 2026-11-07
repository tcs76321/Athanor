# ADR 0126 — Runtime evidence: Core Pod is the headline, host runs are smoke

**Status:** Accepted · **Date:** 2026-11-07 · **Refs:** ADR-0075, ADR-0078,
ADR-0092; ARCHITECTURE §32; `tools/bench`, `scripts/bench-core.sh`, `Makefile`

## Context

The supported runtime is the Core Pod. The benchmark already has a Core Pod
path, but host-binary runs remain easy to produce and easy to confuse with a
headline result. A recent document rerun used the host binary because host
Ollama was loopback-only, and the result tree did not record the runtime. A
reader cannot tell a diagnostic from a measurement.

## Decision

### 1. Every run writes `run.json`

A benchmark run records runtime (`core-pod` or `host`), Core image and digest,
Ollama version, model digests, host class, context/cast policy, slot count,
git revision, and timestamps next to its results.

### 2. Headline results require the Core Pod

- `bench-full` and the anchor/leaderboard path require a Core Pod preflight and
  `runtime=core-pod`.
- `report`, `leaderboard`, and `evidence` refuse a non-Core-Pod row for a
  headline, anchor, or leaderboard aggregate; host rows are labelled
  `diagnostic`.
- The host path is renamed `bench-host-smoke`: six tasks, isolated output,
  never written into a headline results directory.

### 3. Preflight is explicit

`scripts/bench-core.sh preflight` checks health, Ollama reachability from
inside the Core, the embedding model, the Podman socket, mounts, the state
volume, and disk. A failure prints the remediation and stops.

### 4. Frozen corpora are never edited

v2 remains the 0.0.0 instrument. v3 is authored separately and frozen at
1.0.0. A host run may be used for harness development, but it is not evidence.

## Consequences

- A headline number is reproducible: runtime, image, models, and commit are
  recorded with it.
- Host development remains fast and honest; it cannot masquerade as a result.
- The preflight makes the host-Ollama reachability problem explicit instead of
  silently falling back to the host binary.
