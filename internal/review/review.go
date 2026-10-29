// Package review is the F4 first-use review queue: a human rating of an
// accepted artifact, stored with provenance, plus the query that lists
// accepted artifacts with no review yet. It deliberately stores the rating and
// its rubric rather than a model label; the queue payload is blinded by
// construction (an artifact id, kind, and time, no model name).
package review

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/store"
)

// Review is one stored human review.
type Review struct {
	ID         string            `json:"id"`
	ArtifactID string            `json:"artifact_id"`
	JobID      string            `json:"job_id"`
	ProjectID  string            `json:"project_id"`
	Reviewer   string            `json:"reviewer"`
	Score      float64           `json:"score"`
	Accepted   bool              `json:"accepted"`
	Blinded    bool              `json:"blinded"`
	Rubric     map[string]int    `json:"rubric,omitempty"`
	Note       string            `json:"note,omitempty"`
	Provenance map[string]string `json:"provenance,omitempty"`
	CreatedAt  string            `json:"created_at"`
}

// CreateInput is one review submission.
type CreateInput struct {
	ArtifactID string
	JobID      string
	ProjectID  string
	Reviewer   string
	Score      float64
	Accepted   bool
	Blinded    bool
	Rubric     map[string]int
	Note       string
	Provenance map[string]string
}

// Repo persists reviews.
type Repo struct{ db *store.Store }

func NewRepo(s *store.Store) *Repo { return &Repo{db: s} }

// Create validates and stores one review.
func (r *Repo) Create(ctx context.Context, in CreateInput) (Review, error) {
	if in.ArtifactID == "" || in.JobID == "" || in.ProjectID == "" {
		return Review{}, fmt.Errorf("review: artifact, job, and project are required")
	}
	if in.Score < 0 || in.Score > 1 {
		return Review{}, fmt.Errorf("review: score must be within [0,1], got %v", in.Score)
	}
	if in.Reviewer == "" {
		in.Reviewer = "operator"
	}
	rubric, err := json.Marshal(in.Rubric)
	if err != nil {
		return Review{}, err
	}
	prov, err := json.Marshal(in.Provenance)
	if err != nil {
		return Review{}, err
	}
	id := ids.New()
	if _, err := r.db.DB().ExecContext(ctx, `
		INSERT INTO artifact_reviews
			(id, artifact_id, job_id, project_id, reviewer, score, accepted, blinded,
			 rubric_json, note, provenance_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, in.ArtifactID, in.JobID, in.ProjectID, in.Reviewer,
		in.Score, in.Accepted, in.Blinded, string(rubric), in.Note, string(prov)); err != nil {
		return Review{}, fmt.Errorf("storing review: %w", err)
	}
	return r.Get(ctx, id)
}

// Get returns one review by id.
func (r *Repo) Get(ctx context.Context, id string) (Review, error) {
	var (
		out               Review
		accepted, blinded int
		rubric, prov      string
	)
	err := r.db.DB().QueryRowContext(ctx, `
		SELECT id, artifact_id, job_id, project_id, reviewer, score, accepted, blinded,
		       rubric_json, note, provenance_json, created_at
		  FROM artifact_reviews WHERE id = ?`, id).
		Scan(&out.ID, &out.ArtifactID, &out.JobID, &out.ProjectID, &out.Reviewer,
			&out.Score, &accepted, &blinded, &rubric, &out.Note, &prov, &out.CreatedAt)
	if err != nil {
		return Review{}, err
	}
	out.Accepted, out.Blinded = accepted != 0, blinded != 0
	_ = json.Unmarshal([]byte(rubric), &out.Rubric)
	_ = json.Unmarshal([]byte(prov), &out.Provenance)
	return out, nil
}

// ListByArtifact returns the reviews for one artifact, newest first.
func (r *Repo) ListByArtifact(ctx context.Context, artifactID string) ([]Review, error) {
	return r.list(ctx, `WHERE artifact_id = ? ORDER BY created_at DESC, id`, artifactID)
}

// ListRecent returns recent reviews across projects.
func (r *Repo) ListRecent(ctx context.Context, limit int) ([]Review, error) {
	if limit <= 0 {
		limit = 50
	}
	return r.list(ctx, `ORDER BY created_at DESC, id LIMIT ?`, limit)
}

func (r *Repo) list(ctx context.Context, tail string, args ...any) ([]Review, error) {
	rows, err := r.db.DB().QueryContext(ctx, `
		SELECT id, artifact_id, job_id, project_id, reviewer, score, accepted, blinded,
		       rubric_json, note, provenance_json, created_at
		  FROM artifact_reviews `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Review
	for rows.Next() {
		var (
			rv                Review
			accepted, blinded int
			rubric, prov      string
		)
		if err := rows.Scan(&rv.ID, &rv.ArtifactID, &rv.JobID, &rv.ProjectID, &rv.Reviewer,
			&rv.Score, &accepted, &blinded, &rubric, &rv.Note, &prov, &rv.CreatedAt); err != nil {
			return nil, err
		}
		rv.Accepted, rv.Blinded = accepted != 0, blinded != 0
		_ = json.Unmarshal([]byte(rubric), &rv.Rubric)
		_ = json.Unmarshal([]byte(prov), &rv.Provenance)
		out = append(out, rv)
	}
	return out, rows.Err()
}

// Queue returns accepted artifact ids that have no review yet, newest first.
// The API maps them to blinded artifact summaries.
func (r *Repo) Queue(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.DB().QueryContext(ctx, `
		SELECT a.id
		  FROM artifacts a
		  LEFT JOIN artifact_reviews rv ON rv.artifact_id = a.id
		 WHERE a.status = 'accepted' AND rv.id IS NULL
		 ORDER BY a.created_at DESC, a.id
		 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
