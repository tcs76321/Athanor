-- ADR-0090 S4: arm-tagged outcomes.
--
-- A later selection layer needs the reasoning arm and its cost alongside the
-- result. The values are recorded from the job's llm_call and
-- regulator_decision events at terminal state; defaults keep old rows valid.
ALTER TABLE strategy_outcomes ADD COLUMN think_arm TEXT NOT NULL DEFAULT '';
ALTER TABLE strategy_outcomes ADD COLUMN reasoning_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE strategy_outcomes ADD COLUMN deliberate_units INTEGER NOT NULL DEFAULT 0;
ALTER TABLE strategy_outcomes ADD COLUMN refines INTEGER NOT NULL DEFAULT 0;
ALTER TABLE strategy_outcomes ADD COLUMN reframes INTEGER NOT NULL DEFAULT 0;
