# M5-T1 Division-Strategy Spike — Findings

**Date:** 2026-09-16 · **Spike:** `spikes/m5-t1-division/` · **Timebox:** 4h (size M)
**Refs:** ROADMAP M5-T1; ARCHITECTURE §10.1; [ADR-0020](../adr/0020-division-strategy.md)

## Question

Which algorithm divides files into dormant chunks such that (a) reassembly is
byte-identical, (b) chunk boundaries are semantically meaningful, and (c) the
dependency cost is acceptable to this deliberately lean project?

## Candidates

| # | Name | Approach | New deps |
|---|---|---|---|
| A | `tree-sitter` | `go-tree-sitter` v0.25.0 + grammar modules (Go, Python, JavaScript); chunks at named-root-child start bytes | 5 modules, 4 of them cgo |
| B | `pure-go` | `go/parser` for Go; indentation-aware scanner (triple-quotes, brackets, continuations) for Python; brace/semicolon-depth scanner for JavaScript | 0 |
| C | `header-regex` | naive structural-header regexes, including Markdown headings | 0 |

## Corpus

196 files, 1,136,840 bytes:

- **187 Go files** — this repository's own sources (`internal/**`), the
  "real repo" half of the corpus.
- **9 hand-written fixtures** — Python (`pipeline.py`, realistic code with
  declaration-shaped text inside docstrings), JavaScript (`scheduler.js`,
  braces inside template literals/comments), Markdown (`design.md`,
  `#`-prefixed lines inside code fences), plus edge cases (empty, no
  trailing newline, unicode, very long lines, CRLF) and three deliberately
  broken files.

Hand-written rather than vendored from third-party repos: keeps the
throwaway spike free of licensing surface. The Go half is real code.

## Property results (the acceptance gate)

All three strategies pass **P1–P5** on all 196 files:

| Property | Result |
|---|---|
| P1 byte-identical reassembly | PASS — all strategies × 196 files |
| P2 exact tiling (no gaps/overlaps) | PASS |
| P3 line metadata correct | PASS |
| P4 determinism (same input → same chunks) | PASS |
| P5 broken inputs degrade, never lose bytes | PASS |

P5 behavior differs by design and is worth recording:

| Input | tree-sitter | pure-go | header-regex |
|---|---|---|---|
| `broken.go` | 2 chunks, `ast` (error recovery) | 1 chunk, `fallback` (parser rejects) | 4 chunks, `header` |
| `broken.py` | 3 chunks, `ast` | 2 chunks, `structural` (scanner doesn't parse) | 3 chunks, `header` |
| `broken.js` | 2 chunks, `ast` | 1 chunk, `fallback` (unbalanced EOF detected) | 1 chunk, `fallback` |

Tree-sitter's error recovery is the best P5 posture: broken files still get
semantically meaningful AST boundaries. The pure-Go scanners detect
untrusted states (unbalanced braces, parser errors) and fall back honestly.
A regex cannot detect anything, so C neither knows nor cares — its
boundaries on broken files are as arbitrary as on clean ones.

## Boundary agreement (semantic quality)

Ground truth per language: `go/parser` for Go, tree-sitter for Python/JS.
Precision = candidate boundaries that coincide with ground truth; recall =
ground-truth boundaries the candidate found. Non-zero boundaries only.

| Language | Candidate | Precision | Recall | Chunks (vs ground truth) |
|---|---|---|---|---|
| go (187 files) | tree-sitter | **1.00** | 0.89 | 1555 (vs 1729) |
| go (187 files) | header-regex | 0.99 | 0.67 | 1225 |
| python (4 files) | pure-go scanner | 0.56 | 0.75 | 31 (vs 24) |
| python (4 files) | header-regex | 0.59 | 0.65 | 26 |
| javascript (2 files) | pure-go scanner | **0.93** | **0.93** | 17 (= 17) |
| javascript (2 files) | header-regex | 0.87 | 0.87 | 17 |

Explanations, checked by hand:

- **Go recall 0.89 (tree-sitter vs go/parser) is by design, not error:**
  go/parser emits a boundary at the `import` declaration; the spike's
  tree-sitter skip set folds package clause + imports into the first
  declaration's chunk. Precision 1.00 means every tree-sitter boundary is a
  real declaration start.
- **header-regex on Go only sees `^func `** — `type`/`var`/`const` groups
  merge into the preceding chunk (recall 0.67).
- **Python is where heuristics fall apart:** on only four small fixtures,
  the hand-rolled scanner lands at 0.56/0.75 and the naive regex at
  0.59/0.65. Python's grammar-free structure (indentation + decorators +
  string-heavy docstrings) is exactly the case where a real grammar earns
  its keep.
- **JavaScript turned out well for the scanner** (0.93/0.93): brace-depth
  plus top-level `;` tracking happens to align with declaration starts —
  but this is one fixture file, and the scanner silently degrades the
  moment a language uses different block syntax.

## Throughput (Apple M2 Max, largest corpus file per language)

| Strategy | Go | Python | JavaScript | Markdown |
|---|---|---|---|---|
| tree-sitter | 13.6 MB/s | 7.8 MB/s | 8.2 MB/s | n/a (fallback) |
| pure-go | 86.6 MB/s | 210.7 MB/s | 138.8 MB/s | n/a (fallback) |
| header-regex | 85.5 MB/s | 70.5 MB/s | 63.3 MB/s | 77.4 MB/s |

Notes:

- tree-sitter numbers **include per-call parser construction** (the spike's
  `Divide` API is stateless). A pooled parser is trivial and is an M5-T2
  implementation detail; the division itself is not the bottleneck.
- Full-corpus wall time: 196 files / 1.14 MB in 145 ms (tree-sitter),
  19 ms (pure-go), 12 ms (header-regex). Even the slowest candidate divides
  a mid-size repo in well under a second — throughput is not a deciding
  factor at Athanor's scale (division also runs on the idle path for
  indexing, §17).

## Dependency weight (the project decision)

Strategy A in the main module would add:

- `github.com/tree-sitter/go-tree-sitter` (cgo runtime, ~11k lines of Go/cgo)
- one cgo grammar module per language (`tree-sitter-go`,
  `tree-sitter-python`, `tree-sitter-javascript`, …), each compiling the
  grammar's generated `parser.c` (100k–500k lines of C) into every binary
- `github.com/mattn/go-pointer` (transitive)

Against today's five direct dependencies, this is the largest single
dependency decision in the project so far. It does not change the build
model (CGO is already mandatory, ADR-0003), but it does add grammar version
churn and compile weight. Strategies B and C add nothing.

## Iteration log (honest)

1. First JS scanner placed boundaries immediately after `}`; agreement with
   tree-sitter was 0.00 — a systematic off-by-newline, not a tiling
   failure. Fixed by sliding the boundary to the next content line start.
2. First Python scanner split only at `def`/`class`; it missed module-level
   statements (`if __name__ == "__main__":`) — recall 0.60. Fixed by
   boundarying any column-0 statement after a blank line, with import and
   comment lines excluded to mirror the AST skip sets.
3. The naive-regex fixtures needed flush-left declaration-shaped lines
   inside triple-quoted strings; indented fake-decls punish nothing (`(?m)^`
   is column-anchored). Fixed in the fixture, not the code.

## Conclusion

See [ADR-0020](../adr/0020-division-strategy.md). In one line: **the
byte-partition property is cheap and universal; semantic boundary quality
is what costs** — and only a real grammar buys it everywhere. The hybrid
(tree-sitter for code, header-regex for markdown/text, universal fallback)
is the recommendation; the dependency call is the human's.
