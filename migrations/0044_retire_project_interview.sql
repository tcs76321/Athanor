-- 0044_retire_project_interview: the classic interview is retired. The brief
-- (outcome, limits, sources, acceptance, priority) and the discovery/plan
-- pipeline replace it; the separate question set and plan table are removed.

DROP TRIGGER IF EXISTS project_interviews_touch_updated_at;
DROP TABLE IF EXISTS project_interviews;
