-- Migration 0024: strategy trajectory (M8-T7; ARCHITECTURE §13.3,
-- docs/cognitive-operations.md, ADR-0064).
--
-- The immutable strategy_outcome gains the job's executed cognitive
-- trajectory: the ordered operations that ran, each with its attributed cost
-- (model calls / tokens) and its grounded verdict where a deterministic
-- verifier judged it. This is the dataset the M8 selection layer (M8-T8)
-- learns from and the novelty detector (M8-T9) measures a new task against.
-- Additive and forward-only; legacy rows default to an empty trajectory.
ALTER TABLE strategy_outcomes ADD COLUMN operations_json TEXT NOT NULL DEFAULT '[]';
