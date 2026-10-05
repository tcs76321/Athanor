-- Migration 0021: strategy capture (ROADMAP M6-T10/T11; ARCHITECTURE §13.3).
--
-- Every job exercises a strategy (personas, temperatures, prompt templates,
-- candidate counts). strategy_profiles records it at job start from the
-- persona plan (zero inference); strategy_outcomes records the immutable
-- result at job end; strategy_insights holds mined patterns (T11). The
-- UNIQUE(job_id) constraints make capture idempotent and enforce one profile
-- and one outcome per job.
CREATE TABLE strategy_profiles (
	id                 TEXT PRIMARY KEY,
	job_id             TEXT NOT NULL UNIQUE REFERENCES jobs(id),
	project_id         TEXT NOT NULL REFERENCES projects(id),
	archetype          TEXT NOT NULL DEFAULT '',
	exploration_path_id TEXT,
	signature_json     TEXT NOT NULL DEFAULT '[]',
	task_features_json TEXT NOT NULL DEFAULT '{}',
	created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE strategy_outcomes (
	id                   TEXT PRIMARY KEY,
	job_id               TEXT NOT NULL UNIQUE REFERENCES jobs(id),
	strategy_profile_id  TEXT NOT NULL REFERENCES strategy_profiles(id),
	result               TEXT NOT NULL
	                     CHECK (result IN ('accepted_new','accepted_previous','rejected','failed','cancelled')),
	score                REAL NOT NULL DEFAULT 0,
	evaluator_confidence REAL NOT NULL DEFAULT 0,
	retries              INTEGER NOT NULL DEFAULT 0,
	reflection_loops     INTEGER NOT NULL DEFAULT 0,
	token_cost           INTEGER NOT NULL DEFAULT 0,
	wall_time_ms         INTEGER NOT NULL DEFAULT 0,
	created_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE strategy_insights (
	id            TEXT PRIMARY KEY,
	scope         TEXT NOT NULL CHECK (scope IN ('project','global')),
	polarity      TEXT NOT NULL CHECK (polarity IN ('winning','losing')),
	pattern_json  TEXT NOT NULL DEFAULT '{}',
	evidence_json TEXT NOT NULL DEFAULT '{}',
	statement     TEXT NOT NULL DEFAULT '',
	status        TEXT NOT NULL DEFAULT 'proposed'
	              CHECK (status IN ('proposed','active','muted','retired')),
	created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX idx_strategy_profiles_project ON strategy_profiles(project_id);
CREATE INDEX idx_strategy_outcomes_profile ON strategy_outcomes(strategy_profile_id);
CREATE INDEX idx_strategy_insights_status  ON strategy_insights(scope, status);
