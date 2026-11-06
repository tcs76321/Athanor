-- Discovery options describe a direction of work, not a business pitch.
-- Rename the legacy business-shaped columns to the language the prompt and UI
-- already use: domain, focus, audience, need, approach, format, availability,
-- first deliverable, first check, time to first deliverable, setup cost, and
-- alternatives.
--
-- SQLite has supported RENAME COLUMN since 3.25; the application's bundled
-- driver is newer, and a fresh database applies this after 0034.

ALTER TABLE discovery_options RENAME COLUMN industry TO domain;
ALTER TABLE discovery_options RENAME COLUMN niche TO focus;
ALTER TABLE discovery_options RENAME COLUMN customer TO audience;
ALTER TABLE discovery_options RENAME COLUMN pain TO need;
ALTER TABLE discovery_options RENAME COLUMN wedge TO approach;
ALTER TABLE discovery_options RENAME COLUMN business_model TO format;
ALTER TABLE discovery_options RENAME COLUMN pricing TO availability;
ALTER TABLE discovery_options RENAME COLUMN first_offer TO first_deliverable;
ALTER TABLE discovery_options RENAME COLUMN validation_experiment TO first_check;
ALTER TABLE discovery_options RENAME COLUMN time_to_first_revenue_days TO time_to_first_deliverable_days;
ALTER TABLE discovery_options RENAME COLUMN startup_cost_usd TO setup_cost_usd;
ALTER TABLE discovery_options RENAME COLUMN competition TO alternatives;
