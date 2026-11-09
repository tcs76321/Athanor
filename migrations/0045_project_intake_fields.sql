-- 0045_project_intake_fields: a project's intent lives on the project.
--
-- The brief duplicated projects.goal and carried two overlapping boundary
-- fields (constraints and limitations) plus a success field that the primary
-- planning path ignored. Project intake now keeps one optional boundaries
-- text and one sources list on the project itself, infers a direct/explore
-- on-ramp, and records the approved plan for audit. project_briefs is retained
-- for one migration window so a running Core can finish an in-flight review;
-- 0047 drops it once no code reads it.

ALTER TABLE projects ADD COLUMN boundaries TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN sources_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE projects ADD COLUMN onramp TEXT NOT NULL DEFAULT ''
    CHECK (onramp IN ('', 'direct', 'explore'));
ALTER TABLE projects ADD COLUMN approved_plan_id TEXT NOT NULL DEFAULT '';

-- Backfill the optional context fields from the old brief when one exists.
-- constraints_json is a JSON array; limitations was free text. Boundaries is
-- the single free-text list the operator now edits.
UPDATE projects SET
	boundaries = COALESCE((
		SELECT trim(
			COALESCE((
				SELECT group_concat(value, char(10))
				FROM json_each(b.constraints_json)
			), '') ||
			CASE WHEN trim(b.limitations) <> ''
			     THEN char(10) || trim(b.limitations)
			     ELSE '' END
		)
		FROM project_briefs b WHERE b.project_id = projects.id
	), ''),
	sources_json = COALESCE((
		SELECT CASE
			WHEN b.sources_json IS NULL OR b.sources_json = '' THEN '[]'
			ELSE b.sources_json END
		FROM project_briefs b WHERE b.project_id = projects.id
	), '[]')
WHERE EXISTS (SELECT 1 FROM project_briefs b WHERE b.project_id = projects.id);
