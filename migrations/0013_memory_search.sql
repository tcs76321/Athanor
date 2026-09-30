-- Migration 0013: MCE memory retrieval index (ROADMAP M5-T7; ADR-0026).
--
-- §25's query_memory is "hybrid vector + FTS retrieval from SQLite". This
-- migration adds both halves of that index.
--
-- 1. Two external-content FTS5 tables over the text projections the MCE
--    already stores:
--      * compacted_memory_fts  — compacted_memory.content (the §10.3 memos)
--      * dormant_index_fts     — dormant_index.summary (the Dormant Index)
--    Chunk bodies are BLOBs of source code and are deliberately NOT indexed
--    — BM25 over code is noise, and chunk content is reached through
--    context_swap. Search finds the *entry*; the hit names the chunk id so
--    the model can swap it in (ADR-0026 §4). The source path is matched by a
--    secondary LIKE in the retrieval engine: external-content FTS5 indexes
--    exactly one source table, so summary and path cannot share a table.
--
--    External content means the FTS table stores no copy of the text; the
--    triggers keep the index in sync with the source rows. For an
--    external-content table, DELETE is expressed as an INSERT of the special
--    'delete' command row carrying the *old* column values, and UPDATE is a
--    delete followed by an insert.
--
-- 2. memory_embeddings — the vector store the in-Go cosine VectorIndex reads
--    (ADR-0026 §2). Keyed by (owner_id, model) so re-embedding after a model
--    switch records a new row rather than overwriting history, and the `dim`
--    column pins the per-model embedding dimension (a mismatch is a typed
--    error, never silent truncation; ADR-0026 §3). project_id / job_id are
--    denormalized from the owning row so a scoped vector scan filters
--    without a join.
--
-- FTS5 availability is a build property (the `sqlite_fts5` build tag); the
-- store.CheckFTS5 boot preflight gates the daemon before this migration runs
-- (ADR-0026 §1), so a tag-less binary never reaches these statements.

CREATE VIRTUAL TABLE compacted_memory_fts USING fts5(
    content,
    content='compacted_memory',
    content_rowid='rowid'
);

CREATE TRIGGER compacted_memory_fts_ai AFTER INSERT ON compacted_memory BEGIN
    INSERT INTO compacted_memory_fts(rowid, content) VALUES (new.rowid, new.content);
END;

CREATE TRIGGER compacted_memory_fts_ad AFTER DELETE ON compacted_memory BEGIN
    INSERT INTO compacted_memory_fts(compacted_memory_fts, rowid, content)
    VALUES ('delete', old.rowid, old.content);
END;

CREATE TRIGGER compacted_memory_fts_au AFTER UPDATE ON compacted_memory BEGIN
    INSERT INTO compacted_memory_fts(compacted_memory_fts, rowid, content)
    VALUES ('delete', old.rowid, old.content);
    INSERT INTO compacted_memory_fts(rowid, content) VALUES (new.rowid, new.content);
END;

CREATE VIRTUAL TABLE dormant_index_fts USING fts5(
    summary,
    content='dormant_index',
    content_rowid='rowid'
);

CREATE TRIGGER dormant_index_fts_ai AFTER INSERT ON dormant_index BEGIN
    INSERT INTO dormant_index_fts(rowid, summary) VALUES (new.rowid, new.summary);
END;

CREATE TRIGGER dormant_index_fts_ad AFTER DELETE ON dormant_index BEGIN
    INSERT INTO dormant_index_fts(dormant_index_fts, rowid, summary)
    VALUES ('delete', old.rowid, old.summary);
END;

CREATE TRIGGER dormant_index_fts_au AFTER UPDATE ON dormant_index BEGIN
    INSERT INTO dormant_index_fts(dormant_index_fts, rowid, summary)
    VALUES ('delete', old.rowid, old.summary);
    INSERT INTO dormant_index_fts(rowid, summary) VALUES (new.rowid, new.summary);
END;

CREATE TABLE memory_embeddings (
    owner_id     TEXT NOT NULL,
    source_kind  TEXT NOT NULL CHECK (source_kind IN ('memo','chunk')),
    model        TEXT NOT NULL,
    dim          INTEGER NOT NULL CHECK (dim > 0),
    vector       BLOB NOT NULL,
    content_hash TEXT NOT NULL,
    project_id   TEXT,
    job_id       TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (owner_id, model)
);

CREATE INDEX idx_memory_embeddings_scope ON memory_embeddings(project_id, job_id);
CREATE INDEX idx_memory_embeddings_kind  ON memory_embeddings(source_kind);
