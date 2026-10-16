-- known-issues M16: one insight row per (scope, pattern_json).
--
-- `insightExists` was a SELECT-then-INSERT, so the daydream loop and the API
-- handler could each insert the same pattern. A UNIQUE index makes the insert
-- atomic; CreateInsight upserts (re-emerging a retired row, no-op otherwise).
-- Dedup any pre-existing duplicates first (keep the lowest id).
DELETE FROM strategy_insights
WHERE id NOT IN (SELECT MIN(id) FROM strategy_insights GROUP BY scope, pattern_json);

CREATE UNIQUE INDEX IF NOT EXISTS idx_strategy_insights_pattern
    ON strategy_insights(scope, pattern_json);
