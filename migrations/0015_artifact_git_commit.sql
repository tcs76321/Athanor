-- Migration 0015: artifacts.git_commit (ROADMAP F3-T5; ADR-0030).
--
-- ARCHITECTURE §9.2 lists `git_commit` as an artifact field, but the
-- artifacts table (migration 0001) never carried it, so Git-as-undo
-- (§14; Invariant 6 -- "all code changes are atomic commits on
-- agent-created branches") had nowhere to record the commit an accepted
-- artifact landed in. This column is that record.
--
-- Nullable on purpose: an artifact accepted before this migration, or
-- accepted for a project with no repository_path, legitimately has none.
-- It is set once, by artifact.Store.SetGitCommit, after the acceptance
-- transaction commits (the commit SHA cannot exist before acceptance).

ALTER TABLE artifacts ADD COLUMN git_commit TEXT;
