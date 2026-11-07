-- Migration 0039: record the actual first-cycle candidate count on a strategy
-- outcome. The profile's signature records the planned count at job start; the
-- divergence phase may lower it (one-shot-first), cap it (non-code), or raise
-- it (measured panel). Learned candidate selection needs the arm that actually
-- ran, not the plan.
ALTER TABLE strategy_outcomes ADD COLUMN candidates INTEGER NOT NULL DEFAULT 0;
