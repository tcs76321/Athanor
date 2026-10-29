-- Horizon budget ledger (ADR-0091).
--
-- A window is a fixed accounting period (default 24h anchored at
-- budget.window_start). A grant is one job's allocation from that window;
-- reserve-then-settle prevents two jobs from spending the same remaining
-- tokens. Spent is settled from the job's llm_call token accounting.
--
-- `state`:
--   reserved  the job holds the allocation and is still running
--   settled   the job finished; tokens_spent/wall_ns_spent hold actuals
--   released  the allocation was returned (deferred/cancelled); actuals kept
--
-- One row per (job, window): a deferred job re-reserves the same row and its
-- prior partial spend stays counted.
CREATE TABLE IF NOT EXISTS budget_windows (
    window_start     TEXT PRIMARY KEY,
    window_end       TEXT NOT NULL,
    tokens_ceiling   INTEGER NOT NULL,
    wall_ns_ceiling  INTEGER NOT NULL,
    reserve_percent  INTEGER NOT NULL DEFAULT 40,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS budget_grants (
    id             TEXT PRIMARY KEY,
    job_id         TEXT NOT NULL REFERENCES jobs(id),
    window_start   TEXT NOT NULL REFERENCES budget_windows(window_start),
    priority       TEXT NOT NULL CHECK (priority IN ('interactive','background')),
    tokens         INTEGER NOT NULL,
    wall_ns        INTEGER NOT NULL,
    tokens_spent   INTEGER NOT NULL DEFAULT 0,
    wall_ns_spent  INTEGER NOT NULL DEFAULT 0,
    state          TEXT NOT NULL DEFAULT 'reserved'
                   CHECK (state IN ('reserved','settled','released')),
    granted_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    settled_at     TEXT,
    UNIQUE (job_id, window_start)
);

CREATE INDEX IF NOT EXISTS idx_budget_grants_window
    ON budget_grants(window_start, state);
CREATE INDEX IF NOT EXISTS idx_budget_grants_job
    ON budget_grants(job_id, state);
