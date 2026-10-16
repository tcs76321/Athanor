-- Migration 0025: missing indexes on hot read paths.
--
-- events.project_id: QueryEvents filters by project, and the append-only
-- events table grows without bound, so a project-filtered audit query
-- degenerates to a full scan. artifacts.job_id: LatestForJob / ListByJob
-- filter by job_id on the per-job comparison/artifact paths; the existing
-- idx_artifacts_project leads with project_id, which cannot serve them.
-- Additive and forward-only.
CREATE INDEX idx_events_project ON events(project_id);
CREATE INDEX idx_artifacts_job    ON artifacts(job_id, kind);
