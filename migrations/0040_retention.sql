-- ADR-0127: retention archives and the guarded append-only exception.
--
-- Events older than the retention window are copied to gzip JSONL archives
-- before deletion. The existing events_no_delete trigger becomes conditional on
-- a single-row guard so only the retention transaction can remove archived
-- rows. A normal DELETE FROM events still aborts.

CREATE TABLE IF NOT EXISTS retention_guard (
    id     INTEGER PRIMARY KEY CHECK (id = 1),
    active INTEGER NOT NULL DEFAULT 0 CHECK (active IN (0, 1))
);
INSERT OR IGNORE INTO retention_guard (id, active) VALUES (1, 0);

DROP TRIGGER IF EXISTS events_no_delete;
CREATE TRIGGER events_no_delete
BEFORE DELETE ON events
WHEN NOT EXISTS (SELECT 1 FROM retention_guard WHERE id = 1 AND active = 1)
BEGIN
    SELECT RAISE(ABORT, 'events table is append-only');
END;

CREATE TABLE IF NOT EXISTS retention_archives (
    id             TEXT PRIMARY KEY,
    first_event_id INTEGER NOT NULL,
    last_event_id  INTEGER NOT NULL,
    event_count    INTEGER NOT NULL,
    cutoff_ts      TEXT NOT NULL,
    path           TEXT NOT NULL,
    sha256         TEXT NOT NULL,
    bytes          INTEGER NOT NULL,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_retention_archives_cutoff ON retention_archives(cutoff_ts);
