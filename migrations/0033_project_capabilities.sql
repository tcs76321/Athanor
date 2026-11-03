-- ADR-0093: projects have capabilities, not an operator-chosen archetype.
--
-- capabilities_json is the inferred set of kinds the project can produce.
-- tasks.capability is the task's primary output kind (the final artifact
-- kind). The legacy projects.archetype column stays for one deprecation
-- window and is written with the primary capability for old clients; no new
-- product surface reads or writes it.

ALTER TABLE projects ADD COLUMN capabilities_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE tasks ADD COLUMN capability TEXT NOT NULL DEFAULT '';

-- Backfill the legacy rows: one capability from the old archetype, and each
-- task inherits its project's primary capability.
UPDATE projects
   SET capabilities_json = '["' || archetype || '"]';

UPDATE tasks
   SET capability = (SELECT p.archetype FROM projects p WHERE p.id = tasks.project_id);
