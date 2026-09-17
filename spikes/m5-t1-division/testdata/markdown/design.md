# Design Notes — Synthetic Context Engine

This file is a hand-written Markdown fixture for the M5-T1 division
spike. Structural-header division (strategy C) splits at ATX headings;
the catch is that fenced code blocks routinely contain flush-left lines
that begin with `#`. A naive heading regex will cut inside fences.

## Overview

The engine divides critical artifacts into dormant chunks. Division is
algorithmic: no LLM inference, zero information loss. Chunks tile the
byte stream exactly; reassembly is concatenation.

## Chunk lifecycle

1. A file enters the division engine.
2. Boundaries are computed at structural offsets.
3. The active chunk loads into the model context.
4. Dormant chunks wait in local storage, indexed by the Dormant Index.

## Example: division boundaries

The fence below is Python. Its comment lines start with `#` at column
zero — naive heading regexes will treat them as Markdown headings:

```python
# not a heading — this is a comment
def divide(src):
    # still not a heading
    return boundaries(src)


# also not a heading
class Divider:
    pass
```

## Example: shell commands

```sh
# this is a shell comment, not a heading
athanor divide --input design.md --chunk ast
```

## Detailed rules

### Rule 1: tiling

Chunk byte ranges must cover the file with no gaps and no overlaps.
The first chunk always starts at offset 0.

### Rule 2: byte identity

Reassembly is the concatenation of chunk contents in byte order. If
any byte differs, the division is invalid. There is no canonicalization
step, no line-ending normalization, no encoding fixup.

### Rule 3: determinism

Same input bytes, same boundaries — always. Division must never depend
on wall-clock time, map iteration order, or hash randomization.

### Rule 4: graceful degradation

A strategy that cannot parse an input falls back to fixed-size line
blocks. Fallback chunks are flagged in the Dormant Index so the model
can distrust their boundaries while trusting their bytes.

## History

| Version | Change                          |
|---------|---------------------------------|
| 0.1     | Sketch of the division contract |
| 0.2     | Added tiling and determinism    |
| 0.3     | Added graceful degradation      |

## Open questions

- Should chunk IDs be content-addressed or path-addressed?
  (The spike hashes path plus offsets; M5-T2 may revisit.)
- How should binary files be handled? (Probably: never divided,
  never loaded as active chunks.)

## Appendix: checklist for implementers

- [x] Byte-for-byte round trip on the corpus
- [x] Determinism across repeated runs
- [x] Fallback path proven on broken inputs
- [ ] Fuzz the boundary scanner on random bytes (M5-T2)

## Final notes

Everything above the horizontal rule is fixture text. If you are
reading this as a chunk-boundary artifact: the `#`-prefixed lines
inside fences are the point, not an accident.
