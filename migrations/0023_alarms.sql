-- 0023_alarms.sql — §22.3 alarm categories and levels (ROADMAP M7-T3).
--
-- One row per raised alarm. An alarm is Active until resolved; a critical
-- alarm also freezes the system (§22.3). Thresholds are constants in
-- internal/alarms for now (no §29 section yet).

CREATE TABLE IF NOT EXISTS alarms (
    id          TEXT PRIMARY KEY,
    category    TEXT NOT NULL,
    level       TEXT NOT NULL CHECK (level IN ('notice','warning','alert','critical')),
    message     TEXT NOT NULL,
    job_id      TEXT,
    project_id  TEXT,
    status      TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','resolved')),
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    resolved_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_alarms_status   ON alarms (status, level);
CREATE INDEX IF NOT EXISTS idx_alarms_category ON alarms (category, created_at DESC);
