// Package coverage is the recurring-cycle ledger: what a project's news/brief
// cycle has already covered. The retain block writes a structured item list;
// the next compile reads the recent keys and tells the synthesizer not to
// present them as new. It is the production form of the benchmark's
// "old ids" check (ADR-0139 §6, ADR-0134).
package coverage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/store"
)

// Item is one covered item.
type Item struct {
	Key       string
	Title     string
	Sources   []string
	FirstSeen time.Time
	LastSeen  time.Time
}

// Repo persists coverage.
type Repo struct{ store *store.Store }

// NewRepo returns a repository over s.
func NewRepo(s *store.Store) *Repo { return &Repo{store: s} }

// Record upserts items for one project/namespace. An item already present
// keeps its first_seen_at and advances last_seen_at. Empty keys are skipped.
func (r *Repo) Record(ctx context.Context, projectID, namespace string, items []Item) error {
	projectID = strings.TrimSpace(projectID)
	namespace = strings.TrimSpace(namespace)
	if projectID == "" || namespace == "" {
		return fmt.Errorf("coverage: project and namespace are required")
	}
	if len(items) == 0 {
		return nil
	}
	tx, err := r.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("coverage: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, it := range items {
		key := strings.TrimSpace(it.Key)
		if key == "" {
			continue
		}
		sources, _ := json.Marshal(it.Sources)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO coverage_items (project_id, namespace, item_key, title, sources_json)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(project_id, namespace, item_key) DO UPDATE SET
			   last_seen_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'),
			   title = excluded.title,
			   sources_json = excluded.sources_json`,
			projectID, namespace, key, strings.TrimSpace(it.Title), string(sources),
		); err != nil {
			return fmt.Errorf("coverage: upsert %q: %w", key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("coverage: commit: %w", err)
	}
	return nil
}

// Recent returns the most recently seen items, newest first.
func (r *Repo) Recent(ctx context.Context, projectID, namespace string, limit int) ([]Item, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := r.store.DB().QueryContext(ctx,
		`SELECT item_key, title, sources_json, first_seen_at, last_seen_at
		   FROM coverage_items
		  WHERE project_id = ? AND namespace = ?
		  ORDER BY last_seen_at DESC, item_key ASC
		  LIMIT ?`, projectID, namespace, limit)
	if err != nil {
		return nil, fmt.Errorf("coverage: recent: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Item
	for rows.Next() {
		var it Item
		var sources, first, last string
		if err := rows.Scan(&it.Key, &it.Title, &sources, &first, &last); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(sources), &it.Sources)
		it.FirstSeen, _ = time.Parse(time.RFC3339Nano, first)
		it.LastSeen, _ = time.Parse(time.RFC3339Nano, last)
		out = append(out, it)
	}
	return out, rows.Err()
}

// Keys is the id-only projection the compiler injects into the synthesize
// check.
func (r *Repo) Keys(ctx context.Context, projectID, namespace string, limit int) ([]string, error) {
	items, err := r.Recent(ctx, projectID, namespace, limit)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Key)
	}
	return out, nil
}
