-- Migration 0014: repository indexing (ROADMAP M5-T8; ADR-0028).
--
-- Two changes.
--
-- 1. projects.repository_path — the §6.1 repository location the M5-T8
--    indexer walks. Nullable: a project created before T8, or created
--    without a repository, has none, and indexing is then a no-op until an
--    operator sets one.
--
-- 2. indexed_sources — the incremental manifest. One row per (project,
--    workspace-relative path) recording the file's size, mtime, and content
--    hash plus the chunk count produced. A pass skips a file whose size and
--    mtime match; a file whose hash changed has its superseded source pruned
--    (ADR-0028 §2, §5). `status`/`error` record a per-file failure without
--    losing the row, so a later pass retries it.
--
-- The manifest is deliberately separate from context_chunks: the chunk
-- store is content-addressed and shared by job-scoped ingestion, while the
-- manifest is the project-scoped memory of "what this indexer has seen".
--
-- FK note (ADR-0006): the migration runner disables PRAGMA foreign_keys
-- around each migration and gates the commit on PRAGMA foreign_key_check,
-- so the indexed_sources → projects reference is safe to introduce here.

ALTER TABLE projects ADD COLUMN repository_path TEXT;

CREATE TABLE indexed_sources (
    project_id  TEXT NOT NULL REFERENCES projects(id),
    relpath     TEXT NOT NULL,
    lang        TEXT NOT NULL,
    size        INTEGER NOT NULL,
    mtime_unix  INTEGER NOT NULL,
    source_hash TEXT NOT NULL,
    chunks      INTEGER NOT NULL DEFAULT 0,
    status      TEXT NOT NULL DEFAULT 'ok' CHECK (status IN ('ok','failed')),
    error       TEXT,
    indexed_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (project_id, relpath)
);

CREATE INDEX idx_indexed_sources_hash ON indexed_sources(source_hash);

CREATE TRIGGER indexed_sources_touch_updated_at AFTER UPDATE ON indexed_sources
BEGIN
    UPDATE indexed_sources SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
    WHERE project_id = NEW.project_id AND relpath = NEW.relpath;
END;
