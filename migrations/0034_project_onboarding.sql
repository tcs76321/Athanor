-- Project onboarding, discovery, and a shorter goal floor.
--
-- The product path for an exploratory goal is: brief → discover → choose →
-- plan → approve. The brief and the option portfolio are durable so a restart
-- resumes the wizard; the chosen option becomes the project's direction.
--
-- Goals were capped at 20 characters by migration 0001's CHECK. A short,
-- honest goal ("make money online") is valid; the onboarding brief supplies
-- the depth. Rebuild the table with a 3-character floor. defer_foreign_keys
-- keeps tasks.goal_id valid across the swap inside this migration's
-- transaction.

PRAGMA defer_foreign_keys=ON;

CREATE TABLE goals_new (
	id          TEXT PRIMARY KEY,
	project_id  TEXT NOT NULL REFERENCES projects(id),
	text        TEXT NOT NULL CHECK (length(text) BETWEEN 3 AND 500),
	status      TEXT NOT NULL DEFAULT 'open'
	            CHECK (status IN ('open','decomposed','completed','failed')),
	created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
INSERT INTO goals_new (id, project_id, text, status, created_at, updated_at)
	SELECT id, project_id, text, status, created_at, updated_at FROM goals;
DROP TABLE goals;
ALTER TABLE goals_new RENAME TO goals;
CREATE TRIGGER goals_touch_updated_at AFTER UPDATE ON goals
BEGIN
	UPDATE goals SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = NEW.id;
END;

CREATE TABLE IF NOT EXISTS project_briefs (
	project_id       TEXT PRIMARY KEY REFERENCES projects(id),
	status           TEXT NOT NULL DEFAULT 'pending'
	                 CHECK (status IN ('pending','briefed','discovered','planned','approved','skipped')),
	goal             TEXT NOT NULL DEFAULT '',
	constraints_json TEXT NOT NULL DEFAULT '[]',
	limitations      TEXT NOT NULL DEFAULT '',
	sources_json     TEXT NOT NULL DEFAULT '[]',
	success          TEXT NOT NULL DEFAULT '',
	updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS discovery_sessions (
	id                 TEXT PRIMARY KEY,
	project_id         TEXT NOT NULL REFERENCES projects(id),
	status             TEXT NOT NULL DEFAULT 'generating'
	                   CHECK (status IN ('generating','ready','failed')),
	brief              TEXT NOT NULL DEFAULT '',
	error              TEXT NOT NULL DEFAULT '',
	selected_option_id TEXT NOT NULL DEFAULT '',
	created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	updated_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_discovery_sessions_project ON discovery_sessions(project_id, created_at);

CREATE TABLE IF NOT EXISTS discovery_options (
	id                        TEXT PRIMARY KEY,
	session_id                TEXT NOT NULL REFERENCES discovery_sessions(id) ON DELETE CASCADE,
	sort_order                INTEGER NOT NULL DEFAULT 0,
	title                     TEXT NOT NULL,
	one_liner                 TEXT NOT NULL DEFAULT '',
	industry                  TEXT NOT NULL DEFAULT '',
	niche                     TEXT NOT NULL DEFAULT '',
	customer                  TEXT NOT NULL DEFAULT '',
	pain                      TEXT NOT NULL DEFAULT '',
	wedge                     TEXT NOT NULL DEFAULT '',
	business_model            TEXT NOT NULL DEFAULT '',
	pricing                   TEXT NOT NULL DEFAULT '',
	first_offer               TEXT NOT NULL DEFAULT '',
	validation_experiment     TEXT NOT NULL DEFAULT '',
	time_to_first_revenue_days INTEGER NOT NULL DEFAULT 0,
	startup_cost_usd          INTEGER NOT NULL DEFAULT 0,
	skills_json               TEXT NOT NULL DEFAULT '[]',
	competition               TEXT NOT NULL DEFAULT '',
	evidence_json             TEXT NOT NULL DEFAULT '[]',
	risks_json                TEXT NOT NULL DEFAULT '[]',
	assumptions_json          TEXT NOT NULL DEFAULT '[]',
	confidence                REAL NOT NULL DEFAULT 0,
	scores_json               TEXT NOT NULL DEFAULT '{}',
	created_at                TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_discovery_options_session ON discovery_options(session_id, sort_order);

-- Keep project_briefs.updated_at honest.
CREATE TRIGGER IF NOT EXISTS project_briefs_touch AFTER UPDATE ON project_briefs
BEGIN
	UPDATE project_briefs SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE project_id = NEW.project_id;
END;

CREATE TRIGGER IF NOT EXISTS discovery_sessions_touch AFTER UPDATE ON discovery_sessions
BEGIN
	UPDATE discovery_sessions SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = NEW.id;
END;
