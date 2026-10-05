-- Migration 0016: projects.execution_json (ROADMAP F3-T4; ADR-0031).
--
-- ARCHITECTURE §6.2 gives each archetype concrete execution definitions
-- (for `code`: language, test framework, build/run commands, linters), but
-- none were modeled: the engine hard-coded `pytest -q` for every code
-- project. This column carries the per-project override as JSON.
--
-- NOT NULL DEFAULT '{}': an existing project, or one created without an
-- override, resolves to the built-in archetype defaults at use time
-- (project.Project.TestCommand), so no backfill is needed. The column is a
-- free-form JSON blob rather than four columns because the field set is
-- still §6.2-shaped and differs by archetype.

ALTER TABLE projects ADD COLUMN execution_json TEXT NOT NULL DEFAULT '{}';
