-- Migration 0012: compacted memory store (ROADMAP M5-T6; ADR-0025).
--
-- §10.3 compaction is the MCE's only lossy operation. Every compacted input is
-- identified by a content-address key, `input_hash` =
-- sha256(kind || template_version || persona || temperature || profile ||
-- sha256(content)), so the SAME input always resolves to the SAME stored output
-- without a model call. That is what makes "same input -> same output across
-- runs" true even though Ollama at temperature 0.0 is not bit-reproducible
-- (M3-T7-c).
--
-- Source data is never deleted: the event log is append-only (migration 0001
-- triggers reject UPDATE/DELETE) and artifact bytes are untouched. A row here
-- is a DERIVED memo, never a replacement.
--
-- `temperature` is CHECK-pinned to 0.0: the §10.3 invariant is enforced in
-- config validation, in the Compactor adapter, and again here, so no code path
-- can persist a compaction produced at a non-zero temperature.
--
-- `persona` is the §10.3 security persona. `source_hash` / `source_relpath` /
-- `project_id` / `job_id` are optional attribution; `HasSource`/`HasJob` let
-- the daydream memory-consolidation sources skip already-consolidated inputs.

CREATE TABLE compacted_memory (
    id               TEXT PRIMARY KEY,
    kind             TEXT NOT NULL CHECK (kind IN ('deterministic','semantic')),
    profile_type     TEXT NOT NULL,
    profile_state    TEXT NOT NULL,
    input_hash       TEXT NOT NULL,
    template_version TEXT NOT NULL,
    persona          TEXT NOT NULL,
    temperature      REAL NOT NULL CHECK (temperature = 0.0),
    source_hash      TEXT,
    source_relpath   TEXT,
    project_id       TEXT,
    job_id           TEXT,
    source_bytes     INTEGER NOT NULL,
    compacted_bytes  INTEGER NOT NULL,
    content          TEXT NOT NULL,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (kind, input_hash)
);

CREATE INDEX idx_compacted_memory_project ON compacted_memory(project_id);
CREATE INDEX idx_compacted_memory_source  ON compacted_memory(source_hash);
CREATE INDEX idx_compacted_memory_kind    ON compacted_memory(kind);

CREATE TRIGGER compacted_memory_touch_updated_at AFTER UPDATE ON compacted_memory
BEGIN
    UPDATE compacted_memory SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = NEW.id;
END;
