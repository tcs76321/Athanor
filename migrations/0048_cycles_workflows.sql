-- 0048_cycles_workflows: projects contain cycles, cycles contain typed block
-- graphs (ADR-0134). A cycle is a run: it references a template version or an
-- inline graph, and its tasks keep the existing scheduler contract.
--
-- Legacy compatibility: every existing project gets one implicit cycle-0 row
-- and every existing task is attached to it. New one-shot projects can keep
-- cycle_id NULL until they create their first cycle.

CREATE TABLE IF NOT EXISTS project_cycles (
    id                TEXT PRIMARY KEY,
    project_id        TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    ordinal           INTEGER NOT NULL,
    kind              TEXT NOT NULL CHECK (kind IN ('legacy','discovery','design','build','operate','review')),
    goal              TEXT NOT NULL,
    state             TEXT NOT NULL CHECK (state IN ('legacy','draft','approved','running','paused','awaiting_review','completed','failed','cancelled')),
    template_id       TEXT NOT NULL DEFAULT '',
    template_version  INTEGER NOT NULL DEFAULT 0,
    trigger_id        TEXT NOT NULL DEFAULT '',
    budget_json       TEXT NOT NULL DEFAULT '{}',
    review_policy     TEXT NOT NULL DEFAULT '',
    frame_json        TEXT NOT NULL DEFAULT '{}',
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (project_id, ordinal)
);
CREATE INDEX IF NOT EXISTS idx_project_cycles_project ON project_cycles(project_id, ordinal);

CREATE TABLE IF NOT EXISTS workflow_templates (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL DEFAULT '',
    name            TEXT NOT NULL,
    current_version INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (project_id, name)
);

CREATE TABLE IF NOT EXISTS workflow_versions (
    id           TEXT PRIMARY KEY,
    template_id  TEXT NOT NULL REFERENCES workflow_templates(id) ON DELETE CASCADE,
    version      INTEGER NOT NULL,
    graph_json   TEXT NOT NULL,
    notes        TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT 'athanor',
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (template_id, version)
);

CREATE TABLE IF NOT EXISTS triggers (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL DEFAULT '',
    template_id     TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL CHECK (kind IN ('manual','schedule','event')),
    spec            TEXT NOT NULL DEFAULT '',
    enabled         INTEGER NOT NULL DEFAULT 1,
    next_run_at     TEXT,
    last_run_at     TEXT,
    idempotency_key TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_triggers_due ON triggers(enabled, next_run_at);

CREATE TABLE IF NOT EXISTS project_frames (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    version     INTEGER NOT NULL,
    frame_json  TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT 'conversation',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (project_id, version)
);

ALTER TABLE tasks ADD COLUMN cycle_id TEXT;

INSERT OR IGNORE INTO project_cycles
    (id, project_id, ordinal, kind, goal, state, created_at, updated_at)
SELECT 'cycle-' || p.id, p.id, 0, 'legacy', p.goal, 'legacy',
       p.created_at, p.updated_at
  FROM projects p;

UPDATE tasks
   SET cycle_id = 'cycle-' || project_id
 WHERE cycle_id IS NULL;

CREATE INDEX IF NOT EXISTS idx_tasks_cycle ON tasks(cycle_id, status);
