// Package jobstate owns the engine's per-job scratch rows in `system_state`.
//
// The engine writes short-lived state while a job runs — the planning
// difficulty and language hints, the structured analysis, the reflection and
// self-refine counters, the regulator signature and per-unit refine counts,
// and the §10.5 evicted-tier suppression set. None of it is durable state: it
// is cleared when the job reaches a terminal state.
//
// Before this package the keys were spread across six engine files and the
// terminal clear list was hand-maintained, so a new key could leak a row per
// job forever. Store.Clear owns the canonical key list; TestClearCoversEveryKey
// creates every accessor's row and proves Clear removes it.
//
// Store is the only engine package allowed to touch the database. It issues
// single statements only — never a transaction — so it can be called from an
// orchestration path without holding the writer connection across I/O
// (ADR-0027, amended by ADR-0125).
package jobstate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/tcs76321/athanor/internal/analysis"
	"github.com/tcs76321/athanor/internal/prompt"
	"github.com/tcs76321/athanor/internal/store"
)

const (
	difficultyPrefix       = "plan:difficulty:"
	languagePrefix         = "plan:language:"
	analysisPrefix         = "analysis:"
	reflectCounterPrefix   = "reflect:counter:"
	reflectProposalPrefix  = "reflect:proposal:"
	refineCounterPrefix    = "refine:counter:"
	evictionStatePrefix    = "context:evicted:"
	regulatorSigPrefix     = "regulator:sig:"
	regulatorRefinedPrefix = "regulator:refined:"
)

// Store reads and writes per-job scratch state.
type Store struct {
	db *store.Store
}

// New returns a scratch store over the daemon database.
func New(db *store.Store) *Store { return &Store{db: db} }

// keys is the canonical list of every per-job scratch key Clear removes.
func keys(jobID string) []string {
	return []string{
		difficultyPrefix + jobID,
		languagePrefix + jobID,
		analysisPrefix + jobID,
		reflectCounterPrefix + jobID,
		reflectProposalPrefix + jobID,
		refineCounterPrefix + jobID,
		evictionStatePrefix + jobID,
		regulatorSigPrefix + jobID,
		regulatorRefinedPrefix + jobID,
	}
}

// Clear deletes every scratch row for a terminal job. A delete failure is
// logged per key and does not stop the rest: the rows are best-effort cleanup,
// not a correctness precondition.
func (s *Store) Clear(ctx context.Context, jobID string) {
	if s == nil || s.db == nil {
		return
	}
	for _, k := range keys(jobID) {
		if _, err := s.db.DB().ExecContext(ctx, `DELETE FROM system_state WHERE key = ?`, k); err != nil {
			slog.Error("engine: clearing job state", "job", jobID, "key", k, "err", err)
		}
	}
}

func (s *Store) get(ctx context.Context, key string) (string, error) {
	if s == nil || s.db == nil {
		return "", sql.ErrNoRows
	}
	var v string
	err := s.db.DB().QueryRowContext(ctx, `SELECT value FROM system_state WHERE key = ?`, key).Scan(&v)
	return v, err
}

func (s *Store) set(ctx context.Context, key, value string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.DB().ExecContext(ctx,
		`INSERT INTO system_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) del(ctx context.Context, key string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.DB().ExecContext(ctx, `DELETE FROM system_state WHERE key = ?`, key)
	return err
}

func (s *Store) text(ctx context.Context, key string) string {
	v, err := s.get(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		slog.Warn("jobstate: reading value", "key", key, "err", err)
		return ""
	}
	return v
}

// Difficulty reads the planner's easy/hard hint ("" when absent).
func (s *Store) Difficulty(ctx context.Context, jobID string) string {
	return s.text(ctx, difficultyPrefix+jobID)
}

// SetDifficulty persists the planner's difficulty hint.
func (s *Store) SetDifficulty(ctx context.Context, jobID, hint string) error {
	if hint == "" {
		return nil
	}
	return s.set(ctx, difficultyPrefix+jobID, hint)
}

// Language reads the planner's source-language hint ("" when absent).
func (s *Store) Language(ctx context.Context, jobID string) string {
	return s.text(ctx, languagePrefix+jobID)
}

// SetLanguage persists the planner's language hint.
func (s *Store) SetLanguage(ctx context.Context, jobID, language string) error {
	if language == "" || language == "unknown" {
		return nil
	}
	return s.set(ctx, languagePrefix+jobID, language)
}

// Analysis reads the planning phase's structured decomposition. Missing or
// corrupt state degrades to the one-unit direct fallback; analysis must never
// block a job.
func (s *Store) Analysis(ctx context.Context, jobID string) (analysis.Analysis, bool) {
	raw, err := s.get(ctx, analysisPrefix+jobID)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return analysis.Default(), false
	}
	var a analysis.Analysis
	if json.Unmarshal([]byte(raw), &a) != nil || len(a.Units) == 0 {
		return analysis.Default(), false
	}
	return a, true
}

// SetAnalysis persists the planning phase's structured decomposition.
func (s *Store) SetAnalysis(ctx context.Context, jobID string, a analysis.Analysis) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return s.set(ctx, analysisPrefix+jobID, string(raw))
}

// ReflectCount reads the reflection counter (0 when absent or corrupt).
func (s *Store) ReflectCount(ctx context.Context, jobID string) int {
	return parseInt(s.text(ctx, reflectCounterPrefix+jobID))
}

// SetReflectCount persists the reflection counter.
func (s *Store) SetReflectCount(ctx context.Context, jobID string, n int) error {
	return s.set(ctx, reflectCounterPrefix+jobID, itoa(n))
}

// ReflectionProposal reads the last reflection proposal ("" when absent).
func (s *Store) ReflectionProposal(ctx context.Context, jobID string) string {
	return s.text(ctx, reflectProposalPrefix+jobID)
}

// SetReflectionProposal persists the last reflection proposal.
func (s *Store) SetReflectionProposal(ctx context.Context, jobID, proposal string) error {
	return s.set(ctx, reflectProposalPrefix+jobID, proposal)
}

// RefineCount reads the self-refine counter (0 when absent or corrupt).
func (s *Store) RefineCount(ctx context.Context, jobID string) int {
	return parseInt(s.text(ctx, refineCounterPrefix+jobID))
}

// SetRefineCount persists the self-refine counter.
func (s *Store) SetRefineCount(ctx context.Context, jobID string, n int) error {
	return s.set(ctx, refineCounterPrefix+jobID, itoa(n))
}

// RegulatorSignature reads the last failure signature ("" when absent).
func (s *Store) RegulatorSignature(ctx context.Context, jobID string) string {
	return s.text(ctx, regulatorSigPrefix+jobID)
}

// SetRegulatorSignature persists the last failure signature.
func (s *Store) SetRegulatorSignature(ctx context.Context, jobID, sig string) error {
	return s.set(ctx, regulatorSigPrefix+jobID, sig)
}

// RefinedCounts reads the per-unit refine counts. Missing or corrupt state is
// an empty map: the regulator must never block a job.
func (s *Store) RefinedCounts(ctx context.Context, jobID string) map[string]int {
	out := map[string]int{}
	raw, err := s.get(ctx, regulatorRefinedPrefix+jobID)
	if err != nil {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// SetRefinedCounts persists the per-unit refine counts.
func (s *Store) SetRefinedCounts(ctx context.Context, jobID string, counts map[string]int) error {
	raw, err := json.Marshal(counts)
	if err != nil {
		return err
	}
	return s.set(ctx, regulatorRefinedPrefix+jobID, string(raw))
}

// EvictedTiers reads the persisted §10.5 suppression set. A missing or corrupt
// row is empty: the ladder re-derives evictions on the next overflow, so the
// worst case is one re-eviction rather than a stuck job.
func (s *Store) EvictedTiers(ctx context.Context, jobID string) []prompt.Tier {
	raw, err := s.get(ctx, evictionStatePrefix+jobID)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return nil
	}
	var tiers []prompt.Tier
	if json.Unmarshal([]byte(raw), &tiers) != nil {
		slog.Error("jobstate: decoding eviction state", "job", jobID, "raw", raw)
		return nil
	}
	return tiers
}

// SetEvictedTiers persists the suppression set. An empty set deletes the row so
// `system_state` stays bounded.
func (s *Store) SetEvictedTiers(ctx context.Context, jobID string, tiers []prompt.Tier) error {
	if len(tiers) == 0 {
		return s.del(ctx, evictionStatePrefix+jobID)
	}
	raw, err := json.Marshal(tiers)
	if err != nil {
		return err
	}
	return s.set(ctx, evictionStatePrefix+jobID, string(raw))
}
