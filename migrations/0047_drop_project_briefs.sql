-- 0047_drop_project_briefs: the project is its own intent record.
--
-- Migration 0045 copied the old brief's boundaries and sources onto the
-- project and stopped all writers. No code reads project_briefs now; the
-- status column was a second workflow state that intake derives from durable
-- artifacts instead.

DROP TRIGGER IF EXISTS project_briefs_touch;
DROP TABLE IF EXISTS project_briefs;
