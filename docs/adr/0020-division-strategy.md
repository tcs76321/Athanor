# ADR 0020 — Division strategy for the MCE (M5-T1)

**Status:** Proposed · **Date:** 2026-09-16 · **Refs:** ARCHITECTURE §10.1; ROADMAP M5-T1, M5-T2, M5-T3; findings [docs/probes/m5-t1-division.md](../probes/m5-t1-division.md); spike `spikes/m5-t1-division/`

## Context

M5-T1 is a 4-hour timeboxed spike to choose how the division engine splits
files into dormant chunks (§10.1: algorithmic, zero-LLM, zero information
loss). Its output gates M5-T2 (division engine + chunk store) and M5-T3
(`context_swap`). Three candidates were implemented and raced against a
196-file, 1.14 MB corpus (this repository's own Go sources plus
hand-written Python/JavaScript/Markdown fixtures and adversarial edge and
broken cases):

- **A — tree-sitter**: `go-tree-sitter` v0.25.0 + grammar modules; chunks at
  named-root-child start bytes.
- **B — pure-Go**: `go/parser` for Go; hand-written structural scanners for
  Python (indentation) and JavaScript (brace/semicolon depth).
- **C — header-regex**: naive structural-header regexes; the only candidate
  that covers Markdown, which §10.1 explicitly prescribes for text.

Every candidate satisfies the byte-partition contract on the whole corpus:
reassembly is byte-identical (P1), chunks tile exactly (P2), line metadata
is correct (P3), division is deterministic (P4), and broken inputs degrade
to fallback without losing a byte (P5). The contract is therefore not the
differentiator — **semantic boundary quality and dependency cost are.**

Measured boundary agreement against per-language AST ground truth:
tree-sitter 1.00/0.89 (Go), pure-Go scanner 0.93/0.93 (JS) but 0.56/0.75
(Python), header-regex 0.99/0.67 (Go) and 0.59–0.87 elsewhere. Throughput
is a non-factor at Athanor's scale: the slowest candidate still divides
1.14 MB in 145 ms.

## Decision (pending human sign-off — see "The dependency call" below)

M5-T2 implements the division engine as a **hybrid**:

1. **Code languages → tree-sitter** (strategy A). One chunking model across
   languages, precision-1.00 boundaries where grammars exist, and
   error-recovery parsing that keeps meaningful boundaries even on broken
   files (best P5 posture of the three). Grammars are added per language on
   demand, Go first.
2. **Markdown and plain text → header-regex** (strategy C), per §10.1's own
   prescription for structural-header division. Zero deps; measured
   behavior on the corpus is exactly right for headings and exactly wrong
   inside code fences — acceptable for text, where fences containing
   non-heading `#` lines merely produce finer chunks.
3. **Universal fallback → fixed line blocks**, flagged `fallback` in the
   Dormant Index, guaranteeing P5 for any unknown language or parse
   failure.

`go/parser` was rejected for Go despite being 6× faster and zero-dep:
it would create a second code path to maintain, its boundary model differs
from tree-sitter's (import handling), and division throughput is not
bottleneck anywhere in the architecture (idle-path indexing, §17).

M5-T2 also inherits from the spike: the chunk schema (path, lang, kind,
byte range, line range, content), the boundary-at-declaration-start tiling
model (doc comments and imports fold into the following chunk via skip
sets), and a pooled-parser implementation note (the spike's throughput
includes per-call parser construction).

## The dependency call (project decision, per AGENTS.md)

Adopting strategy A adds the project's largest dependency surface to date:
`go-tree-sitter` (cgo runtime) plus one cgo grammar module per language
(each compiles 100k–500k generated C lines into every binary), plus
`mattn/go-pointer` transitively. CGO is already mandatory (ADR-0003), so
the build model does not change — but grammar version churn and compile
weight are real recurring costs.

**This ADR is a proposal until the human accepts the dependency.** If the
call is "no tree-sitter", the fallback position is: `go/parser` for Go
(precision 1.00 by construction) + header-regex elsewhere + universal
fallback — accepting 0.59–0.87 boundary agreement on non-Go code.

## Caveats

- **Fixture scale.** Python/JS agreement numbers come from 4 and 2 small
  hand-written fixtures, not a large third-party corpus. The Go numbers
  (187 real files) are the statistically meaningful ones. M5-T2's property
  tests should grow the corpus as real projects flow through the system.
- **Boundary "quality" is proxied by agreement with the AST strategies**;
  no direct measure of downstream context efficiency (swap frequency,
  chunk hit rate) exists yet. M5-T5's assembly-priority work is the first
  place that could be measured end-to-end.
- **Markdown header-regex false positives inside code fences are known and
  accepted** for this milestone; if they prove harmful in practice, a
  fence-aware pre-scan is a small, contained fix.
- **The spike is throwaway.** Nothing in `spikes/m5-t1-division/` is
  imported by `internal/`; M5-T2 re-implements against the ADR, with its
  own tests (byte-for-byte reassembly property per ROADMAP M5-T2).
