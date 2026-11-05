-- AI-authored plan candidates (ADR-0097).
--
-- Discovery selects a direction; planning turns it into three distinct,
-- reviewable 30-day plans. A session is durable from the moment it starts, so
-- a Core restart shows generating/failed and can be retried instead of losing
-- the run. The selected candidate is an index into plan_candidates.

CREATE TABLE IF NOT EXISTS plan_sessions (
	id             TEXT PRIMARY KEY,
	project_id     TEXT NOT NULL REFERENCES projects(id),
	option_id      TEXT NOT NULL DEFAULT '',
	status         TEXT NOT NULL DEFAULT 'generating'
	               CHECK (status IN ('generating','ready','failed')),
	error          TEXT NOT NULL DEFAULT '',
	selected_index INTEGER NOT NULL DEFAULT -1,
	created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_plan_sessions_project ON plan_sessions(project_id, created_at);

CREATE TABLE IF NOT EXISTS plan_candidates (
	id               TEXT PRIMARY KEY,
	session_id       TEXT NOT NULL REFERENCES plan_sessions(id) ON DELETE CASCADE,
	sort_order       INTEGER NOT NULL DEFAULT 0,
	title            TEXT NOT NULL,
	summary          TEXT NOT NULL DEFAULT '',
	tasks_json       TEXT NOT NULL DEFAULT '[]',
	risks_json       TEXT NOT NULL DEFAULT '[]',
	assumptions_json TEXT NOT NULL DEFAULT '[]',
	confidence       REAL NOT NULL DEFAULT 0,
	score            INTEGER NOT NULL DEFAULT 0,
	created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_plan_candidates_session ON plan_candidates(session_id, sort_order);

CREATE TRIGGER IF NOT EXISTS plan_sessions_touch AFTER UPDATE ON plan_sessions
BEGIN
	UPDATE plan_sessions SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = NEW.id;
END;
