-- 0022_daydream.sql — §17.3 DaydreamLog (ROADMAP M7-T2).
--
-- One row per daydream action per pass. Records the action, the persona used,
-- the produced artifact/correction/insight ids, and the memory-compaction
-- counters. Output of a daydream action is always draft/proposed (§17.2);
-- this table is the audit trail, not a promotion mechanism.

CREATE TABLE IF NOT EXISTS daydream_logs (
    id                          TEXT PRIMARY KEY,
    action                      TEXT NOT NULL,
    persona_used                TEXT NOT NULL DEFAULT '',
    started_at                  TEXT NOT NULL,
    finished_at                 TEXT NOT NULL,
    artifacts_json              TEXT NOT NULL DEFAULT '[]',
    corrections_json            TEXT NOT NULL DEFAULT '[]',
    insights_json               TEXT NOT NULL DEFAULT '[]',
    chunks_processed            INTEGER NOT NULL DEFAULT 0,
    tokens_saved                INTEGER NOT NULL DEFAULT 0,
    dormant_index_entries_added INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_daydream_logs_started ON daydream_logs (started_at DESC);
