# Local-first software — source note

Source ID: local-first.md
Published: 2026-11-10

Local-first software keeps the primary copy of a user's data on the user's device.
It works offline, syncs when a connection returns, and lets several devices edit
the same document without a central server as the source of truth.

The hard problems are conflict resolution, schema migration, and long-term data
ownership. A practical design uses an append-only change log, per-field
timestamps, and deterministic merge rules. Transport is replaceable; the data
model is not.

A local-first product should be able to export every record in an open format and
operate with no network for an indefinite period. Hosted services are optional
accelerators, never the only copy.

The trade-offs are storage growth, merge complexity, and the need for a
deterministic conflict policy. Teams that skip the conflict policy usually
discover it after their first multi-device edit.
