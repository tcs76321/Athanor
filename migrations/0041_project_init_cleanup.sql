-- Project initialization describes work, not a human business plan.
--
-- Discovery directions and AI plans used to carry a setup cost, a
-- time-to-first-deliverable/revenue estimate, required human skills, an
-- uncalibrated model confidence, and synthetic scores. Athanor runs locally
-- under its own budget, so those numbers had no meaning and the risk/evidence
-- components rewarded under-reporting or invented claims. The operator
-- chooses a direction; no score ranks it.
--
-- SQLite has supported DROP COLUMN since 3.35; the bundled driver is newer.
-- The remaining rows keep every content field (audience, need, approach,
-- format, first deliverable, first check, evidence, risks, assumptions).

ALTER TABLE discovery_options DROP COLUMN time_to_first_deliverable_days;
ALTER TABLE discovery_options DROP COLUMN setup_cost_usd;
ALTER TABLE discovery_options DROP COLUMN skills_json;
ALTER TABLE discovery_options DROP COLUMN alternatives;
ALTER TABLE discovery_options DROP COLUMN confidence;
ALTER TABLE discovery_options DROP COLUMN scores_json;

ALTER TABLE plan_candidates DROP COLUMN confidence;
ALTER TABLE plan_candidates DROP COLUMN score;
