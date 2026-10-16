// Package evaluation (repo.go): SQLite-backed repository for
// EvaluationRecords (migration 0007). The repo is the only writer and
// the only reader of evaluation_records; engine and quality-probe code
// go through Repo.
//
// Atomicity: Create writes the row and the audit `events` entry in one
// transaction so a half-written record can never be observed. This
// mirrors the artifact-versioning pattern (artifact.NewVersion) and
// the job-transition pattern (job.Repository.Transition). Append-only
// events is the §28.1 invariant; this repo respects it by never
// updating or deleting an evaluation record after creation.

package evaluation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/store"
)

// ErrNotFound reports an evaluation record ID that does not exist.
var ErrNotFound = errors.New("evaluation: record not found")

// Repo persists evaluation records.
type Repo struct {
	db *store.Store
}

// NewRepo returns a repo writing through the given store.
func NewRepo(db *store.Store) *Repo {
	return &Repo{db: db}
}

// recordColumns is the column list shared between INSERT and SELECT so
// the schema/Go-field mapping cannot drift.
const recordColumns = `id, job_id, artifact_id, compared_against,
	score, passed_tests,
	failed_tests_json, missing_criteria_json,
	security_issues_json, style_issues_json,
	better_than_previous, confidence, summary, created_at`

// scanRecord reads one row produced by recordColumns (or any superset
// ending in the same column order) into a Record.
func scanRecord(row interface{ Scan(...any) error }) (Record, error) {
	var (
		rec                Record
		comparedAgainst    sql.NullString
		passedTests        int
		betterThanPrevious int
		failedJSON         string
		missingJSON        string
		securityJSON       string
		styleJSON          string
		createdAt          string
	)
	if err := row.Scan(
		&rec.ID, &rec.JobID, &rec.ArtifactID, &comparedAgainst,
		&rec.Score, &passedTests,
		&failedJSON, &missingJSON, &securityJSON, &styleJSON,
		&betterThanPrevious, &rec.Confidence, &rec.Summary, &createdAt,
	); err != nil {
		return Record{}, err
	}
	if comparedAgainst.Valid {
		rec.ComparedAgainst = comparedAgainst.String
	}
	rec.PassedTests = passedTests != 0
	rec.BetterThanPrevious = betterThanPrevious != 0
	var err error
	if rec.FailedTests, err = decodeList(failedJSON); err != nil {
		return Record{}, fmt.Errorf("evaluation: %w", err)
	}
	if rec.MissingCriteria, err = decodeList(missingJSON); err != nil {
		return Record{}, fmt.Errorf("evaluation: %w", err)
	}
	if rec.SecurityIssues, err = decodeList(securityJSON); err != nil {
		return Record{}, fmt.Errorf("evaluation: %w", err)
	}
	if rec.StyleIssues, err = decodeList(styleJSON); err != nil {
		return Record{}, fmt.Errorf("evaluation: %w", err)
	}
	if rec.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return Record{}, fmt.Errorf("evaluation: parsing created_at: %w", err)
	}
	return rec, nil
}

// Create persists a new evaluation record. The row and the audit
// `events` entry commit in a single transaction: the audit trail
// cannot diverge from the data, mirroring the §8.2 state-transition
// invariant.
//
// JobID, ArtifactID, ID, and CreatedAt must already be set on the
// input. The remaining fields are the evaluator's output. The repo
// does not validate score/confidence ranges — the evaluator owns the
// rubric (M3-T2).
func (r *Repo) Create(ctx context.Context, rec Record) (Record, error) {
	if rec.ID == "" || rec.JobID == "" || rec.ArtifactID == "" {
		return Record{}, fmt.Errorf("evaluation: ID, JobID, and ArtifactID are required")
	}
	if rec.CreatedAt.IsZero() {
		return Record{}, fmt.Errorf("evaluation: CreatedAt is required (call NewRecord)")
	}

	tx, err := r.db.DB().BeginTx(ctx, nil)
	if err != nil {
		return Record{}, fmt.Errorf("evaluation: beginning tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	compared := sql.NullString{String: rec.ComparedAgainst, Valid: rec.ComparedAgainst != ""}
	passed := 0
	if rec.PassedTests {
		passed = 1
	}
	better := 0
	if rec.BetterThanPrevious {
		better = 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO evaluation_records (`+recordColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.JobID, rec.ArtifactID, compared,
		rec.Score, passed,
		encodeList(rec.FailedTests), encodeList(rec.MissingCriteria),
		encodeList(rec.SecurityIssues), encodeList(rec.StyleIssues),
		better, rec.Confidence, rec.Summary,
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
	); err != nil {
		return Record{}, fmt.Errorf("evaluation: inserting record: %w", err)
	}

	// Audit row: every record is paired with an event in the
	// append-only log. Ships in the same transaction so a crash
	// between INSERT and audit append cannot leave a record without
	// its footprint.
	auditData, err := json.Marshal(map[string]any{
		"event":                "evaluation_record_created",
		"record_id":            rec.ID,
		"artifact_id":          rec.ArtifactID,
		"score":                rec.Score,
		"passed_tests":         rec.PassedTests,
		"better_than_previous": rec.BetterThanPrevious,
		"confidence":           rec.Confidence,
	})
	if err != nil {
		return Record{}, fmt.Errorf("evaluation: marshalling audit event: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO events (ts, category, job_id, level, data_json)
		VALUES (strftime('%Y-%m-%dT%H:%M:%fZ','now'), 'jobs', ?, 'info', ?)`,
		rec.JobID, string(auditData),
	); err != nil {
		return Record{}, fmt.Errorf("evaluation: appending audit event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Record{}, fmt.Errorf("evaluation: committing: %w", err)
	}
	return rec, nil
}

// ListByJob returns the records for a job in creation order (oldest
// first). The index `idx_evaluation_records_job` covers this query.
func (r *Repo) ListByJob(ctx context.Context, jobID string) ([]Record, error) {
	rows, err := r.db.DB().QueryContext(ctx,
		`SELECT `+recordColumns+` FROM evaluation_records
		 WHERE job_id = ? ORDER BY created_at ASC, id ASC`, jobID)
	if err != nil {
		return nil, fmt.Errorf("evaluation: listing records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Record
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("evaluation: scanning record: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("evaluation: iterating records: %w", err)
	}
	return out, nil
}

// Get loads one record by ID. Mainly useful for tests and the quality
// probe; the comparison phase prefers ListByJob.
func (r *Repo) Get(ctx context.Context, id string) (Record, error) {
	row := r.db.DB().QueryRowContext(ctx,
		`SELECT `+recordColumns+` FROM evaluation_records WHERE id = ?`, id)
	rec, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return rec, err
}

// ListByArtifact returns the records whose `artifact_id` matches
// the given artifact, in creation order (oldest first). M3-T3
// commit 3.1 uses this to load the previous accepted artifact's
// evaluation history for the comparison prompt's "Previous-record
// summary" section.
//
// Records here describe the artifact *as a candidate* — what the
// security persona said about it at the time it was the
// diverging phase's output. The records that *compared against*
// the same artifact (where the artifact is the `compared_against`
// field) live in a different query (not exposed yet; M3-T7 may
// add it).
func (r *Repo) ListByArtifact(ctx context.Context, artifactID string) ([]Record, error) {
	rows, err := r.db.DB().QueryContext(ctx,
		`SELECT `+recordColumns+` FROM evaluation_records
		 WHERE artifact_id = ? ORDER BY created_at ASC, id ASC`, artifactID)
	if err != nil {
		return nil, fmt.Errorf("evaluation: listing records by artifact: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Record
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("evaluation: scanning record: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("evaluation: iterating records: %w", err)
	}
	return out, nil
}
