# Design Notes — Synthetic Context Engine

This fixture exercises the markdown header splitter. It deliberately
contains a fenced code block with flush-left `#` lines that are *not*
headings, to document the known header-regex limitation (ADR-0020).

## Goals

- Propose the smallest possible design.
- Keep the parser dependency-free.

## Shape

```text
# this looks like a heading but is inside a fence
unsafe { do_nothing() }
```

## Open questions

1. Do we need incremental parsing?
2. How large may a single chunk be?