# ADR 0128 — Division grammars for the full generation language set

**Status:** Accepted · **Date:** 2026-11-07 · **Refs:** ADR-0020, ADR-0028,
ADR-0101; ARCHITECTURE §10.1; `internal/mce/division`, `internal/deps`

## Context

Generation and test-command detection already cover Python, Go, JavaScript,
TypeScript, Rust, Java, Ruby, PHP, and Shell (ADR-0101). Lossless division
covered only Go, Python, and JavaScript, so a Rust/Java/TS repository fell back
to fixed-line blocks exactly where repo-scale context mattered most. ADR-0020
explicitly deferred additional grammars until demand existed; the multi-slot
work makes that demand concrete.

## Decision

Add tree-sitter grammars for TypeScript (TS and TSX), Rust, Java, Ruby, PHP
(PHP-only grammar), and Shell, at the same major line as the existing runtime:

- `tree-sitter-typescript` (TS/TSX variants)
- `tree-sitter-rust`
- `tree-sitter-java`
- `tree-sitter-ruby`
- `tree-sitter-php` (PHP-only; HTML wrappers are not code chunks)
- `tree-sitter-bash`

`LangFor` maps the extensions; `grammarFor` selects TSX by `.tsx`. Each grammar
has an explicit skip set for package/import/comment nodes, verified against a
real parse fixture rather than guessed. Unknown or parse-failing sources keep
the fixed-line fallback (P5), and the byte-partition contract is unchanged.

The dependency allowlist grows from ten to sixteen direct modules; the six
grammars are ratified as one project decision.

## Consequences

- Repositories in the full generation language set get AST-boundary chunks and
  repository indexing without a language-specific fallback.
- CGO build time and binary size grow; CI must stay green on macOS and Linux.
- The division property suite now spans at least ten languages; a new grammar
  must add a fixture and the same P1–P5 coverage.
- `internal/mce/index_walk` automatically indexes the new languages because it
  filters through `division.LangFor`.
