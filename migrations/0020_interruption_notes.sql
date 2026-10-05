-- Migration 0020: interruption queue (ROADMAP M6-T8c; ARCHITECTURE §20.4).
--
-- A user must not interrupt generation mid-token (it corrupts the model
-- context). Instead a note is queued here and injected into the prompt at the
-- next safe point (a phase transition / context assembly), then marked
-- injected. The engine drains pending rows per job.
CREATE TABLE interruption_notes (
	id          TEXT PRIMARY KEY,
	job_id      TEXT NOT NULL REFERENCES jobs(id),
	note        TEXT NOT NULL,
	status      TEXT NOT NULL DEFAULT 'pending'
	            CHECK (status IN ('pending','injected')),
	created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	injected_at TEXT
);

CREATE INDEX idx_interruption_notes_job_status ON interruption_notes(job_id, status);
