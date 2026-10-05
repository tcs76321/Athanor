# ADR 0029 — File-IO containment scope (F3-T3)

**Status:** Accepted · **Date:** 2026-10-04 · **Refs:** ARCHITECTURE §21.3,
§4; ROADMAP F3-T3; ADR-0015 (airlock pipeline), M4-T1
(`internal/airlock/paths`), ADR-0028 (repository indexing).

## Context

README and ARCHITECTURE described `internal/airlock/paths` as the layer
*every* file operation routes through. An F3 audit found raw `os.*` IO in
several packages — the artifact store (`persist.go`, `queries.go`), the
engine's comparison read (`compare.go`), config loading (`config/load.go`),
log rotation (`logging/rotate.go`), pre-migrate backups (`store/backup.go`),
per-job token files (`jobpod/token.go`), and the airlock pipelines
themselves. The blanket claim was false.

The relevant question is not "does this line call `os.Open`" but "can an
untrusted party influence the path." Athanor has two classes of path:

1. **Externally-influenced.** Filenames arriving through the ingress
   `inbox/` (delivered by `fsnotify`, so attacker-named), repository-relative
   paths supplied to the indexing pipeline, and the export tree walked by
   egress. These cross a trust boundary and must be resolved, validated, and
   opened with `O_NOFOLLOW`.
2. **Internal / operator-controlled.** The configured config path, state
   directory, artifact store directory, log directory, backup directory, and
   token directory. These derive from operator configuration or from
   server-generated UUIDs (`internal/ids`), never from untrusted input. The
   artifact store is the clearest case: its `StoragePath` is
   `<state>/artifacts/<uuid>`, written by the daemon and read back by ID.

## Decision

- **Externally-influenced paths route through `internal/airlock/paths`**
  (`Resolve`/`Validate`/`OpenNoFollow`). This was already true for egress,
  indexing, and the ingress *validation* step; ingress is now fixed to
  **open through `OpenNoFollow`** as well. The previous ingress code called
  `Validate` (an `Lstat`), then re-opened the file by path for hashing and
  reading — leaving the `Lstat`→`open` TOCTOU window open on the one path
  class that is genuinely attacker-named. Opening once with `O_NOFOLLOW`
  makes the check and the read the same file descriptor.
- **Internal / operator-controlled paths are exempt.** They may use plain
  `os.*`. Applying containment to a path the operator already controls adds
  no security and obscures intent.
- **The docs say so.** The README claim is narrowed to "every
  externally-influenced file operation"; this ADR is the durable record of
  the classification.

## Consequences

- The containment guarantee is now precise and defensible: it names the
  trust boundary instead of over-claiming.
- A future contributor adding a *new* externally-influenced path must route
  it through `airlock/paths`; a new internal path need not. Reviewers have a
  rule to apply.
- F3-T3 does **not** add a structural AST gate forbidding raw `os.*` in
  `internal/`. Such a gate would need a large allowlist for the legitimate
  internal paths and would mostly encode "we listed these files." The
  existing Gate G1 already constrains the dangerous surfaces (`os/exec`,
  `syscall`, container clients, outbound HTTP); containment discipline for
  file paths remains a documented review rule, as it was before.
- If a future feature makes an internal path externally influenceable
  (e.g. a user-supplied artifact path), the path moves to class 1 by this
  ADR and must be routed through `airlock/paths`.
