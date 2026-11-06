-- 0037_project_priority: operator-set project priority with a single critical
-- project. The partial unique index is the invariant: at most one row may be
-- 'critical'. Setting a new critical demotes the previous one in the same
-- transaction (Repo.SetPriority).

ALTER TABLE projects ADD COLUMN priority TEXT NOT NULL DEFAULT 'medium'
    CHECK (priority IN ('critical', 'high', 'medium', 'low'));

CREATE UNIQUE INDEX projects_one_critical ON projects(priority) WHERE priority = 'critical';
CREATE INDEX projects_priority ON projects(priority, created_at);
