-- F3 first-use project interview + plan approval.
--
-- One row per project. The interview is resumable: answers and the generated
-- plan are stored before approval, and the first job is started only after the
-- operator approves (or explicitly skips) the plan.
CREATE TABLE IF NOT EXISTS project_interviews (
    project_id     TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    status         TEXT NOT NULL DEFAULT 'questions'
                   CHECK (status IN ('questions','plan_ready','approved','skipped')),
    questions_json TEXT NOT NULL DEFAULT '[]',
    answers_json   TEXT NOT NULL DEFAULT '{}',
    plan_md        TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TRIGGER IF NOT EXISTS project_interviews_touch_updated_at
AFTER UPDATE ON project_interviews
BEGIN
    UPDATE project_interviews
       SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
     WHERE project_id = NEW.project_id;
END;
