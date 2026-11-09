-- 0042_project_direction_contract: discovery options become a compact work
-- contract instead of an audience-first business pitch.
--
-- Six core fields: title, problem, scope, approach, deliverable, verification
-- (the API/UI name is "check"). Risks and assumptions remain optional lists.
-- Removed: one_liner, domain, focus, audience, need, format, availability,
-- first_deliverable, first_check, and evidence_json. Existing content is
-- backfilled; the audience is folded into the problem, format into the
-- deliverable, and domain/focus into scope. Evidence was ungrounded (the model
-- never read the sources), so unsourced claims live under assumptions.

ALTER TABLE discovery_options ADD COLUMN problem TEXT NOT NULL DEFAULT '';
ALTER TABLE discovery_options ADD COLUMN scope TEXT NOT NULL DEFAULT '';
ALTER TABLE discovery_options ADD COLUMN deliverable TEXT NOT NULL DEFAULT '';
ALTER TABLE discovery_options ADD COLUMN verification TEXT NOT NULL DEFAULT '';

UPDATE discovery_options SET
	problem = TRIM(need || CASE WHEN audience <> '' THEN ' (for ' || audience || ')' ELSE '' END),
	scope = TRIM(domain || CASE WHEN domain <> '' AND focus <> '' THEN ' · ' ELSE '' END || focus),
	deliverable = TRIM(first_deliverable || CASE WHEN format <> '' THEN ' (' || format || ')' ELSE '' END),
	verification = first_check;

ALTER TABLE discovery_options DROP COLUMN one_liner;
ALTER TABLE discovery_options DROP COLUMN domain;
ALTER TABLE discovery_options DROP COLUMN focus;
ALTER TABLE discovery_options DROP COLUMN audience;
ALTER TABLE discovery_options DROP COLUMN need;
ALTER TABLE discovery_options DROP COLUMN format;
ALTER TABLE discovery_options DROP COLUMN availability;
ALTER TABLE discovery_options DROP COLUMN first_deliverable;
ALTER TABLE discovery_options DROP COLUMN first_check;
ALTER TABLE discovery_options DROP COLUMN evidence_json;
