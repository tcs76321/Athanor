-- Migration 0017: canonical task lifecycle + task_type (ROADMAP M6-T2;
-- ARCHITECTURE §7.3; ADR-0033).
--
-- tasks was created (0001) with the M1 status enum
-- (pending,ready,in_progress,blocked,done,failed,cancelled). §7.3 names
-- the canonical set (pending,ready,running,paused,blocked,completed,
-- failed) and adds task_type. SQLite cannot alter a CHECK in place, so the
-- table is rebuilt with the ADR-0005 recipe: build new → copy → drop old →
-- rename new. This is the same shape migration 0004 used for jobs.
--
-- tasks has inbound foreign keys (jobs.task_id, artifacts.task_id) and a
-- self reference (parent_task_id), so the old table is dropped rather than
-- renamed — RENAME would rewrite the children's REFERENCES clauses to the
-- old name. The runner disables FK enforcement around the migration and
-- gates the commit on PRAGMA foreign_key_check (internal/store/migrate.go),
-- so a broken reference fails and rolls back.
--
-- Remap on copy: in_progress → running, done → completed. A value outside
-- the canonical set (which no production write path produced) fails the
-- CHECK and rolls the migration back loudly.

CREATE TABLE tasks_new (
	id               TEXT PRIMARY KEY,
	project_id       TEXT NOT NULL REFERENCES projects(id),
	goal_id          TEXT REFERENCES goals(id),
	parent_task_id   TEXT REFERENCES tasks(id),
	title            TEXT NOT NULL,
	description      TEXT NOT NULL DEFAULT '',
	status           TEXT NOT NULL DEFAULT 'pending'
	                 CHECK (status IN ('pending','ready','running','paused','blocked','completed','failed')),
	task_type        TEXT NOT NULL DEFAULT 'one_time'
	                 CHECK (task_type IN ('one_time','recurring','triggered')),
	priority         INTEGER NOT NULL DEFAULT 0,
	depends_on_json  TEXT NOT NULL DEFAULT '[]',
	acceptance_criteria_json TEXT NOT NULL DEFAULT '[]',
	budget_json      TEXT NOT NULL DEFAULT '{}',
	allowed_tools_json TEXT NOT NULL DEFAULT '[]',
	created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

INSERT INTO tasks_new (
	id, project_id, goal_id, parent_task_id, title, description,
	status, task_type, priority, depends_on_json, acceptance_criteria_json,
	budget_json, allowed_tools_json, created_at, updated_at
)
SELECT id, project_id, goal_id, parent_task_id, title, description,
       CASE status
           WHEN 'in_progress' THEN 'running'
           WHEN 'done'        THEN 'completed'
           ELSE status
       END,
       'one_time', priority, depends_on_json, acceptance_criteria_json,
       budget_json, allowed_tools_json, created_at, updated_at
FROM tasks;

DROP TABLE tasks;

ALTER TABLE tasks_new RENAME TO tasks;

CREATE INDEX idx_tasks_project_status ON tasks(project_id, status);
CREATE INDEX idx_tasks_goal           ON tasks(goal_id);
CREATE INDEX idx_tasks_parent         ON tasks(parent_task_id);

CREATE TRIGGER tasks_touch_updated_at AFTER UPDATE ON tasks
BEGIN
	UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = NEW.id;
END;
