-- 0053_trigger_timezone: a schedule is local time, stored as UTC.
--
-- The cron fields are interpreted in the trigger's timezone; next_run_at stays
-- RFC3339 UTC so comparisons and ordering are unchanged. The default keeps
-- every existing trigger exactly as it behaved before this migration.

ALTER TABLE triggers ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC';
