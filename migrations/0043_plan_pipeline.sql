-- 0043_plan_pipeline: four-call planning (seed + three expansions) needs a
-- durable per-run stage/heartbeat and a per-plan status. Existing sessions are
-- marked ready with their current candidates; a generating legacy session will
-- be interrupted on the next boot by the heartbeat check.

ALTER TABLE plan_sessions ADD COLUMN stage TEXT NOT NULL DEFAULT 'seed';
ALTER TABLE plan_sessions ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE plan_sessions ADD COLUMN prompt_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plan_sessions ADD COLUMN response_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plan_sessions ADD COLUMN tail TEXT NOT NULL DEFAULT '';
ALTER TABLE plan_sessions ADD COLUMN heartbeat_at TEXT;
ALTER TABLE plan_sessions ADD COLUMN expanded_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plan_sessions ADD COLUMN stop_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE plan_sessions ADD COLUMN total_tokens INTEGER NOT NULL DEFAULT 0;
UPDATE plan_sessions SET heartbeat_at = updated_at;

ALTER TABLE plan_candidates ADD COLUMN stance TEXT NOT NULL DEFAULT '';
ALTER TABLE plan_candidates ADD COLUMN status TEXT NOT NULL DEFAULT 'ready';
ALTER TABLE plan_candidates ADD COLUMN error TEXT NOT NULL DEFAULT '';
ALTER TABLE plan_candidates ADD COLUMN expanded_at TEXT;
