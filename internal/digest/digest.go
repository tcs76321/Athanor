// Package digest builds the §27.2 Morning Digest: an asynchronous summary of
// a time window's activity — completed work, generated artifacts, failures
// with reasons, pending approvals, daydreaming output, and token usage.
//
// Build is a read-only aggregation over SQLite. It is deterministic given the
// window. The daemon exposes it at GET /digest and the CLI at `athanor digest`.
package digest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/store"
)

// Failure is one failed job with its recorded reason.
type Failure struct {
	JobID     string `json:"job_id"`
	ProjectID string `json:"project_id"`
	Reason    string `json:"reason"`
}

// ArtifactCounts summarizes artifacts created in the window.
type ArtifactCounts struct {
	Draft     int `json:"draft"`
	Accepted  int `json:"accepted"`
	Rejected  int `json:"rejected"`
	Candidate int `json:"candidate"`
}

// DaydreamSummary summarizes §17.3 daydream activity in the window.
type DaydreamSummary struct {
	Actions             int `json:"actions"`
	ArtifactsProduced   int `json:"artifacts_produced"`
	CorrectionsProposed int `json:"corrections_proposed"`
	InsightsProposed    int `json:"insights_proposed"`
	ChunksProcessed     int `json:"chunks_processed"`
	TokensSaved         int `json:"tokens_saved"`
}

// Digest is the §27.2 window summary.
type Digest struct {
	Since          time.Time       `json:"since"`
	Until          time.Time       `json:"until"`
	JobsCompleted  int             `json:"jobs_completed"`
	JobsFailed     int             `json:"jobs_failed"`
	JobsCancelled  int             `json:"jobs_cancelled"`
	Failures       []Failure       `json:"failures"`
	GoalsCompleted int             `json:"goals_completed"`
	TasksDone      int             `json:"tasks_done"`
	Artifacts      ArtifactCounts  `json:"artifacts"`
	PendingHITL    int             `json:"pending_hitl"`
	Daydream       DaydreamSummary `json:"daydream"`
	Tokens         int             `json:"tokens"`
	ActiveAlarms   int             `json:"active_alarms"`
}

// Build aggregates activity in [since, until).
func Build(ctx context.Context, st *store.Store, since, until time.Time) (Digest, error) {
	if until.IsZero() {
		until = time.Now().UTC()
	}
	d := Digest{Since: since.UTC(), Until: until.UTC(), Failures: []Failure{}}
	db := st.DB()
	s := since.UTC().Format(time.RFC3339)
	u := until.UTC().Format(time.RFC3339)

	// Job counts by terminal state.
	for state, dst := range map[string]*int{
		"completed": &d.JobsCompleted, "failed": &d.JobsFailed, "cancelled": &d.JobsCancelled,
	} {
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM jobs WHERE state = ? AND updated_at >= ? AND updated_at < ?`,
			state, s, u).Scan(&n); err != nil {
			return Digest{}, fmt.Errorf("digest: jobs %s: %w", state, err)
		}
		*dst = n
	}

	// Failed jobs with reasons.
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id FROM jobs WHERE state='failed' AND updated_at >= ? AND updated_at < ?
		 ORDER BY updated_at DESC LIMIT 20`, s, u)
	if err != nil {
		return Digest{}, fmt.Errorf("digest: failed jobs: %w", err)
	}
	type failedJob struct{ id, project string }
	var failed []failedJob
	for rows.Next() {
		var j failedJob
		if err := rows.Scan(&j.id, &j.project); err != nil {
			_ = rows.Close()
			return Digest{}, err
		}
		failed = append(failed, j)
	}
	_ = rows.Close()
	for _, j := range failed {
		d.Failures = append(d.Failures, Failure{JobID: j.id, ProjectID: j.project, Reason: failureReason(ctx, db, j.id)})
	}

	// Goals completed / tasks done.
	if err := scanCount(ctx, db, &d.GoalsCompleted,
		`SELECT COUNT(*) FROM goals WHERE status='completed' AND updated_at >= ? AND updated_at < ?`, s, u); err != nil {
		return Digest{}, err
	}
	if err := scanCount(ctx, db, &d.TasksDone,
		`SELECT COUNT(*) FROM tasks WHERE status='completed' AND updated_at >= ? AND updated_at < ?`, s, u); err != nil {
		return Digest{}, err
	}

	// Artifacts by status.
	rows, err = db.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM artifacts WHERE created_at >= ? AND created_at < ? GROUP BY status`, s, u)
	if err != nil {
		return Digest{}, fmt.Errorf("digest: artifacts: %w", err)
	}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			_ = rows.Close()
			return Digest{}, err
		}
		switch status {
		case "draft":
			d.Artifacts.Draft = n
		case "accepted":
			d.Artifacts.Accepted = n
		case "rejected":
			d.Artifacts.Rejected = n
		case "candidate":
			d.Artifacts.Candidate = n
		}
	}
	_ = rows.Close()

	// Pending HITL requests.
	if err := scanCount(ctx, db, &d.PendingHITL,
		`SELECT COUNT(*) FROM hitl_requests WHERE status='pending'`); err != nil {
		return Digest{}, err
	}

	// Active alarms.
	if err := scanCount(ctx, db, &d.ActiveAlarms,
		`SELECT COUNT(*) FROM alarms WHERE status='active'`); err != nil {
		return Digest{}, err
	}

	// Daydream activity.
	rows, err = db.QueryContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(chunks_processed),0), COALESCE(SUM(tokens_saved),0),
		        artifacts_json, corrections_json, insights_json
		 FROM daydream_logs WHERE started_at >= ? AND started_at < ?
		 GROUP BY artifacts_json, corrections_json, insights_json`, s, u)
	if err != nil {
		return Digest{}, fmt.Errorf("digest: daydream: %w", err)
	}
	for rows.Next() {
		var artifacts, corrections, insights string
		var chunks, saved int
		if err := rows.Scan(&d.Daydream.Actions, &chunks, &saved, &artifacts, &corrections, &insights); err != nil {
			_ = rows.Close()
			return Digest{}, err
		}
		d.Daydream.ChunksProcessed += chunks
		d.Daydream.TokensSaved += saved
		d.Daydream.ArtifactsProduced += jsonLen(artifacts)
		d.Daydream.CorrectionsProposed += jsonLen(corrections)
		d.Daydream.InsightsProposed += jsonLen(insights)
	}
	_ = rows.Close()

	// Token usage over the window's outcomes.
	if err := scanCount(ctx, db, &d.Tokens,
		`SELECT COALESCE(SUM(token_cost),0) FROM strategy_outcomes WHERE created_at >= ? AND created_at < ?`, s, u); err != nil {
		return Digest{}, err
	}

	return d, nil
}

func scanCount(ctx context.Context, db *sql.DB, dst *int, query string, args ...any) error {
	if err := db.QueryRowContext(ctx, query, args...).Scan(dst); err != nil {
		return fmt.Errorf("digest: count: %w", err)
	}
	return nil
}

// failureReason finds the most recent job_failed event's error for a job.
func failureReason(ctx context.Context, db *sql.DB, jobID string) string {
	rows, err := db.QueryContext(ctx,
		`SELECT data_json FROM events WHERE job_id = ? AND data_json LIKE '%job_failed%'
		 ORDER BY ts DESC LIMIT 1`, jobID)
	if err != nil {
		return ""
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return ""
	}
	var raw string
	if err := rows.Scan(&raw); err != nil {
		return ""
	}
	var data struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return ""
	}
	return data.Error
}

func jsonLen(raw string) int {
	var v []string
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return 0
	}
	return len(v)
}
