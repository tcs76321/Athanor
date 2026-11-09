-- 0046_discovery_direction_contract: a direction is a comparison unit.
--
-- Six prose fields per option made comparison harder than it needed to be.
-- The contract is now title/outcome/method/check (required) plus optional
-- scope/risks/assumptions. outcome folds the old problem + deliverable;
-- method replaces approach. The verification column keeps its name so the
-- engine-facing check is unchanged.

ALTER TABLE discovery_options ADD COLUMN outcome TEXT NOT NULL DEFAULT '';
ALTER TABLE discovery_options ADD COLUMN method TEXT NOT NULL DEFAULT '';

UPDATE discovery_options SET
	outcome = trim(problem || CASE WHEN problem <> '' AND deliverable <> '' THEN ' ' ELSE '' END || deliverable),
	method = approach;

ALTER TABLE discovery_options DROP COLUMN problem;
ALTER TABLE discovery_options DROP COLUMN approach;
ALTER TABLE discovery_options DROP COLUMN deliverable;
