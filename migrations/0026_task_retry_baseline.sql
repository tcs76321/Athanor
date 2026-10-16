-- known-issues H8: the operator reset seam.
--
-- handleFailure counts a task's attempts as len(jobs) - retry_baseline, so an
-- operator reset that returns a blocked task to `pending` while pinning the
-- baseline to the jobs already recorded grants it a fresh §7.2 retry window
-- instead of re-blocking on the very next failure. A task that has never been
-- reset has baseline 0, preserving the original counting exactly.
ALTER TABLE tasks ADD COLUMN retry_baseline INTEGER NOT NULL DEFAULT 0;
