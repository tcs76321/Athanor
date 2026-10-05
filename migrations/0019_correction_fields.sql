-- Migration 0019: CorrectionRecord fields (ROADMAP M6-T6; ARCHITECTURE §18.2).
--
-- The corrections table (0002; severities canonicalized by 0003) lacks three
-- §18.2 fields: the artifact a correction is about, its scope
-- (project|global), and the verbatim user feedback (the derived rule was
-- already modeled). ADD COLUMN cannot add a table-level CHECK (migration
-- 0006 note), so scope's value set is enforced in Go
-- (internal/corrections).
--
-- artifact_id is a nullable FK; SQLite requires an added FK column's default
-- to be NULL when foreign keys are enabled, which it is here.
ALTER TABLE corrections ADD COLUMN artifact_id TEXT REFERENCES artifacts(id);
ALTER TABLE corrections ADD COLUMN scope TEXT NOT NULL DEFAULT 'project';
ALTER TABLE corrections ADD COLUMN user_feedback TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_corrections_artifact ON corrections(artifact_id);
