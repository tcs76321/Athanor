-- 0049_trigger_uniqueness: one trigger per (project, template, kind).
-- Re-creating a cycle from the same template updates that trigger instead of
-- accumulating a new one, so a daily brief does not fan out into several
-- timers. The kind is part of the key because a template may carry both a
-- schedule and a manual trigger.

CREATE UNIQUE INDEX IF NOT EXISTS idx_triggers_project_template_kind
    ON triggers(project_id, template_id, kind)
    WHERE template_id != '';
