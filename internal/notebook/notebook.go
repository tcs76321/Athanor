// Package notebook persists a job's explicit working memory (ADR-0109): the
// plan, hypotheses, observations, results, and decisions the model should
// carry across phases. It is deliberately smaller and more readable than the
// event log, and deliberately separate from the artifact store: the notebook
// is what the model thinks it is doing, not the bytes it produced.
package notebook

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/store"
)

// Kind is the closed notebook entry vocabulary.
type Kind string

const (
	KindPlan        Kind = "plan"
	KindHypothesis  Kind = "hypothesis"
	KindObservation Kind = "observation"
	KindExperiment  Kind = "experiment"
	KindResult      Kind = "result"
	KindDecision    Kind = "decision"
	KindQuestion    Kind = "question"
	KindAnswer      Kind = "answer"
)

// MaxEntriesPerJob bounds a job's notebook. The engine renders only the most
// recent lines, and the store prunes older entries on append so a long job
// cannot grow the table without limit.
const MaxEntriesPerJob = 200

// MaxEntryBytes bounds one entry. A model response can be large; the notebook
// is a summary, not a transcript.
const MaxEntryBytes = 8192

// Entry is one persisted notebook row.
type Entry struct {
	ID        string    `json:"id"`
	JobID     string    `json:"job_id"`
	ProjectID string    `json:"project_id"`
	Kind      Kind      `json:"kind"`
	Phase     string    `json:"phase"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Store persists notebook entries.
type Store struct{ store *store.Store }

// New returns a notebook backed by s.
func New(s *store.Store) *Store { return &Store{store: s} }

// Append writes one entry and prunes the job's oldest entries beyond
// MaxEntriesPerJob. An empty job or content is rejected; content longer than
// MaxEntryBytes is trimmed rather than rejected so a runaway model response
// cannot fail a job.
func (s *Store) Append(ctx context.Context, e Entry) (Entry, error) {
	if s == nil || s.store == nil {
		return Entry{}, fmt.Errorf("notebook: nil store")
	}
	if strings.TrimSpace(e.JobID) == "" {
		return Entry{}, fmt.Errorf("notebook: job id is required")
	}
	if strings.TrimSpace(e.ProjectID) == "" {
		return Entry{}, fmt.Errorf("notebook: project id is required")
	}
	if e.Kind == "" {
		return Entry{}, fmt.Errorf("notebook: kind is required")
	}
	e.Content = trimContent(e.Content)
	if e.Content == "" {
		return Entry{}, fmt.Errorf("notebook: content is required")
	}
	e.ID = ids.New()
	e.CreatedAt = time.Now().UTC()

	tx, err := s.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, fmt.Errorf("notebook: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO job_notebook (id, job_id, project_id, kind, phase, content, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.JobID, e.ProjectID, string(e.Kind), e.Phase, e.Content,
		e.CreatedAt.Format(time.RFC3339Nano)); err != nil {
		return Entry{}, fmt.Errorf("notebook: insert: %w", err)
	}
	// Keep the newest MaxEntriesPerJob rows. The subquery is deterministic
	// on (created_at, id); ties are impossible enough in practice but the id
	// fallback makes the retention rule stable.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM job_notebook
		  WHERE job_id = ?
		    AND id NOT IN (
		        SELECT id FROM job_notebook
		         WHERE job_id = ?
		         ORDER BY created_at DESC, id DESC
		         LIMIT ?)`,
		e.JobID, e.JobID, MaxEntriesPerJob); err != nil {
		return Entry{}, fmt.Errorf("notebook: prune: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Entry{}, fmt.Errorf("notebook: commit: %w", err)
	}
	return e, nil
}

// Recent returns the job's newest entries in chronological order (oldest
// first), so the prompt reads as a narrative. limit <= 0 defaults to 20 and
// is capped at MaxEntriesPerJob.
func (s *Store) Recent(ctx context.Context, jobID string, limit int) ([]Entry, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > MaxEntriesPerJob {
		limit = MaxEntriesPerJob
	}
	rows, err := s.store.DB().QueryContext(ctx,
		`SELECT id, job_id, project_id, kind, phase, content, created_at
		   FROM job_notebook
		  WHERE job_id = ?
		  ORDER BY created_at DESC, id DESC
		  LIMIT ?`, jobID, limit)
	if err != nil {
		return nil, fmt.Errorf("notebook: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Entry
	for rows.Next() {
		var e Entry
		var kind, created string
		if err := rows.Scan(&e.ID, &e.JobID, &e.ProjectID, &kind, &e.Phase, &e.Content, &created); err != nil {
			return nil, err
		}
		e.Kind = Kind(kind)
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			e.CreatedAt = t
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse to chronological order without a second allocation.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// trimContent bounds one entry and keeps the result valid UTF-8. The marker
// makes truncation visible to both the operator and the model.
func trimContent(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= MaxEntryBytes {
		return s
	}
	cut := MaxEntryBytes - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
