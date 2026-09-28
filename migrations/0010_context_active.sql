-- Migration 0010: MCE active-chunk set (ROADMAP M5-T3; ADR-0021 §6, §10).
--
-- §10.1 keeps one chunk "active" — loaded into the model's context window —
-- per working scope; the rest are dormant. Persisting the mapping means a
-- crash mid-swap resumes with the same active chunk instead of losing the
-- working set (§23.6). `scope_id` is a caller-supplied working-set key (the
-- engine passes a job ID today; a later milestone may widen it). A missing
-- row means "no chunk active", which is the correct initial state.
--
-- The FK to context_chunks makes an active pointer to a pruned chunk
-- impossible; the migration runner's foreign_key_check gate (ADR-0006)
-- covers the migration itself.

CREATE TABLE context_active (
    scope_id   TEXT PRIMARY KEY,
    chunk_id   TEXT NOT NULL REFERENCES context_chunks(id),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX idx_context_active_chunk ON context_active(chunk_id);

CREATE TRIGGER context_active_touch_updated_at AFTER UPDATE ON context_active
BEGIN
    UPDATE context_active SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE scope_id = NEW.scope_id;
END;