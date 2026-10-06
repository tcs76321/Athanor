// Package daydream implements the §17.3 DaydreamLog store (ROADMAP M7-T2).
//
// The idle-time action loop itself lives in cmd/athanor (it needs the MCE
// summarizer, the repository indexer, and an LLM adapter, all of which are
// cmd-side). This package owns the persisted record of what each action did:
// the action name, the persona, the artifact/correction/insight ids it
// produced, and the memory-compaction counters.
//
// Every daydream output is draft or proposed (§17.2); this package cannot
// promote anything.
package daydream

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/ids"
	"github.com/tcs76321/athanor/internal/store"
)

// The §17.1 action names. skill_refinement is deferred (ROADMAP §7 backlog).
const (
	ActionMemoryConsolidation    = "memory_consolidation"
	ActionRepoExploration        = "repository_exploration"
	ActionProactiveDocumentation = "proactive_documentation"
	ActionFeedbackReview         = "feedback_review"
	ActionStrategyMining         = "strategy_mining"
	ActionSkillRefinement        = "skill_refinement"
)

// actions is the closed set. A log row with an unknown action is rejected so a
// typo cannot silently create an unattributable record.
var actions = map[string]bool{
	ActionMemoryConsolidation:    true,
	ActionRepoExploration:        true,
	ActionProactiveDocumentation: true,
	ActionFeedbackReview:         true,
	ActionStrategyMining:         true,
	ActionSkillRefinement:        true,
}

// ValidAction reports whether name is a §17.1 action.
func ValidAction(name string) bool { return actions[name] }

// Log is one §17.3 daydream record.
type Log struct {
	ID                       string
	Action                   string
	PersonaUsed              string
	StartedAt                time.Time
	FinishedAt               time.Time
	ArtifactsProduced        []string
	CorrectionsProposed      []string
	InsightsProposed         []string
	ChunksProcessed          int
	TokensSaved              int
	DormantIndexEntriesAdded int
}

// Repo persists daydream logs.
type Repo struct{ store *store.Store }

// NewRepo returns a repo backed by s.
func NewRepo(s *store.Store) *Repo { return &Repo{store: s} }

// Insert validates and persists one log row. The ID is generated when empty.
func (r *Repo) Insert(ctx context.Context, l Log) (Log, error) {
	if !ValidAction(l.Action) {
		return Log{}, fmt.Errorf("daydream: unknown action %q", l.Action)
	}
	if l.ID == "" {
		l.ID = ids.New()
	}
	if l.StartedAt.IsZero() {
		l.StartedAt = time.Now().UTC()
	}
	if l.FinishedAt.IsZero() {
		l.FinishedAt = time.Now().UTC()
	}
	artifacts, err := marshalIDs(l.ArtifactsProduced)
	if err != nil {
		return Log{}, err
	}
	corrections, err := marshalIDs(l.CorrectionsProposed)
	if err != nil {
		return Log{}, err
	}
	insights, err := marshalIDs(l.InsightsProposed)
	if err != nil {
		return Log{}, err
	}
	if _, err := r.store.DB().ExecContext(ctx,
		`INSERT INTO daydream_logs
		   (id, action, persona_used, started_at, finished_at,
		    artifacts_json, corrections_json, insights_json,
		    chunks_processed, tokens_saved, dormant_index_entries_added)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.ID, l.Action, l.PersonaUsed,
		l.StartedAt.UTC().Format(time.RFC3339), l.FinishedAt.UTC().Format(time.RFC3339),
		artifacts, corrections, insights,
		l.ChunksProcessed, l.TokensSaved, l.DormantIndexEntriesAdded,
	); err != nil {
		return Log{}, fmt.Errorf("daydream: inserting log: %w", err)
	}
	return l, nil
}

// Recent returns up to limit logs, newest first (limit <= 0 means all).
func (r *Repo) Recent(ctx context.Context, limit int) ([]Log, error) {
	q := `SELECT id, action, persona_used, started_at, finished_at,
	             artifacts_json, corrections_json, insights_json,
	             chunks_processed, tokens_saved, dormant_index_entries_added
	      FROM daydream_logs ORDER BY started_at DESC, id DESC`
	var args []any
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := r.store.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("daydream: listing logs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Log
	for rows.Next() {
		var l Log
		var started, finished, artifacts, corrections, insights string
		if err := rows.Scan(&l.ID, &l.Action, &l.PersonaUsed, &started, &finished,
			&artifacts, &corrections, &insights,
			&l.ChunksProcessed, &l.TokensSaved, &l.DormantIndexEntriesAdded); err != nil {
			return nil, err
		}
		if l.StartedAt, err = time.Parse(time.RFC3339, started); err != nil {
			return nil, fmt.Errorf("daydream: parsing started_at %q: %w", started, err)
		}
		if l.FinishedAt, err = time.Parse(time.RFC3339, finished); err != nil {
			return nil, fmt.Errorf("daydream: parsing finished_at %q: %w", finished, err)
		}
		if err := json.Unmarshal([]byte(artifacts), &l.ArtifactsProduced); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(corrections), &l.CorrectionsProposed); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(insights), &l.InsightsProposed); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func marshalIDs(idsIn []string) (string, error) {
	if len(idsIn) == 0 {
		return "[]", nil
	}
	raw, err := json.Marshal(idsIn)
	if err != nil {
		return "", fmt.Errorf("daydream: marshalling ids: %w", err)
	}
	return string(raw), nil
}
