# m5-t1-division — Division-Strategy Spike

M5-T1 (ROADMAP): a timeboxed spike comparing three strategies for
losslessly dividing files into dormant chunks (ARCHITECTURE §10.1):

| Strategy | Name in code | Approach |
|---|---|---|
| A | `tree-sitter` | `github.com/tree-sitter/go-tree-sitter` v0.25.0 + grammar modules (Go, Python, JavaScript); chunks at named-root-child start bytes |
| B | `pure-go` | zero new deps: `go/parser` for Go, indentation-aware scanner for Python, brace-depth scanner for JavaScript |
| C | `header-regex` | zero deps, naive structural-header regexes (also covers Markdown) |

All three must satisfy the byte-partition contract (P1–P5 in
`division.go`'s doc comment). The property test is
`roundtrip_test.go`; the decision-matrix numbers come from
`TestMetricsReport` (`go test -v -run TestMetricsReport`) and
`BenchmarkDivide`.

## Corpus

- `testdata/go/` — real Go sources copied from this repository
  (`internal/engine`, `internal/gateway`, `internal/ids`,
  `internal/store`).
- `testdata/python/`, `testdata/javascript/`, `testdata/markdown/` —
  hand-written fixtures shaped like real code (declaration-shaped lines
  inside docstrings/template literals/code fences punish naive
  splitters). Hand-written rather than vendored to avoid third-party
  licensing surface in a throwaway spike.
- `testdata/edge/` — empty file, missing trailing newline, unicode,
  very long lines, CRLF.
- `testdata/broken/` — deliberate syntax errors; property P5 requires
  every strategy to survive these via fallback without losing a byte.
- When run inside a full checkout, the corpus additionally walks
  `../../internal/` — this repository's own sources are the "real repo"
  half of the corpus.

## Run

```bash
go test ./...                          # property tests P1–P5
go test -v -run TestMetricsReport      # decision-matrix tables
go test -bench=. -benchtime=20x -run='^$'   # throughput benchmarks
```

CGO is required for strategy A (tree-sitter compiles grammar C sources);
this machine needs a C toolchain, same as the main project (ADR-0003).

## Dependency footprint (the project decision this spike surfaces)

Strategy A adds four modules to whichever module adopts it:
`go-tree-sitter` (~11k lines of cgo), plus one cgo grammar module per
language (each compiles the grammar's `parser.c`, ~100–500k LOC
generated C). Strategy B adds nothing. Strategy C adds nothing. The
ADR (`docs/adr/0020-division-strategy.md`) records the tradeoff.

## Status

Throwaway spike for M5-T1. Never imported by `internal/`. Findings:
`docs/probes/m5-t1-division.md`. Decision: `docs/adr/0020-division-strategy.md`.
