-- Task-level deterministic acceptance checks.
--
-- Project execution checks are project-wide. A cycle needs different
-- acceptance for each block now: the synthesize block uses the corpus's
-- required sections, while verify/retain use their own structural contract.
-- NULL means "fall back to the project execution checks".
ALTER TABLE tasks ADD COLUMN checks_json TEXT;
