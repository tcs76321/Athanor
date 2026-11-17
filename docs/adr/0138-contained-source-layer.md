# ADR-0138 — A contained source layer for local files and the news wire

**Status:** accepted · **Date:** 2026-11-17 · **Refs:** ARCHITECTURE §21.3;
[ADR-0029](0029-file-io-containment-scope.md), [ADR-0134](0134-project-cycles-and-blocks.md),
[ADR-0137](0137-untrusted-content-and-instruction-hierarchy.md)

## Context

Two call sites read a project's local source material:

- `internal/firstuse` read `sources/<name>` and walked `wire/` at cycle
  compile time to give the task prompt authoritative text;
- `internal/engine/contextasm` read the same two locations again for one-shot
  tasks that did not go through the compiler.

Both used `os.ReadFile` directly. That had three problems:

1. **Path escape.** A symlink in a cloned repository (`wire/x.md` →
   `/etc/passwd`) was followed and its target became prompt text.
2. **Duplicated layout knowledge.** `sources/` and `wire/` were hardcoded in
   two packages, so a project could not use a different directory without a
   code change.
3. **Silent failure.** A missing or rejected source disappeared without a
   manifest, so a report could quietly omit a source the operator expected.

## Decision

1. **One loader.** `internal/source` owns local source reads. Its
   `LoadLocal(root, specs, limits)` takes explicit `Spec`s (`file`, `dir`,
   `url`, `query`), returns a `Manifest` of `Loaded` and `Skipped` entries,
   and never reads a URL or query from disk.
2. **Containment first.** Every open goes through `airlock/paths`
   (`Resolve` → `Lstat` → `OpenNoFollow`). Symlinks, traversal, absolute
   paths, device nodes, setuid files, and executables are refused and
   recorded as skips. Per-file and total byte caps bound the prompt and the
   process.
3. **Layout is data.** `ProjectSpecs` maps the 0.0.0 layout (`sources/`,
   `wire/`) into specs; the loader does not know those names. A future
   per-project or per-block `source_dirs` setting is a new spec source, not
   a code change.
4. **Visible skips.** The manifest is available to the caller. Today the
   compiler and the assembler use `SourceTexts()` / `PromptBlock()`; a
   later slice surfaces the skip list in the UI.
5. **Untrusted rendering.** `Manifest.PromptBlock` wraps each source with
   `prompt.Untrusted` (ADR-0137), so local files and the wire are data, not
   instructions.
6. **Structural gate.** `internal/gate/gate_source_test.go` fails if
   `internal/firstuse`, `internal/engine/contextasm`, or
   `internal/engine/phases` calls `os.ReadFile` directly. The fixture reader
   is the single named exception because it has its own containment.

## Consequences

- A symlinked source is now a visible skip instead of a prompt-time file
  read.
- The two callers share one implementation, so a cap or extension change
  lands once.
- Source text is sorted deterministically and carries a SHA-256 hash, which
  is the input to the source-snapshot work planned for research.
- A missing source still does not fail a cycle (best-effort), but it is
  recorded; a later UI slice can show it.
- No new dependency.
