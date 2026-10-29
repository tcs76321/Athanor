package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// regulatorPrefix scopes the ADR-0090 checkpoint state: the last failure
// signature and the per-unit refine counts. It is job scratch, cleared on a
// terminal transition.
const (
	regulatorSigPrefix     = "regulator:sig:"
	regulatorRefinedPrefix = "regulator:refined:"
)

func (e *Engine) getRegulatorSignature(ctx context.Context, jobID string) string {
	var v string
	err := e.db.DB().QueryRowContext(ctx,
		`SELECT value FROM system_state WHERE key = ?`, regulatorSigPrefix+jobID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	return v
}

func (e *Engine) setRegulatorSignature(ctx context.Context, jobID, sig string) error {
	_, err := e.db.DB().ExecContext(ctx,
		`INSERT INTO system_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		regulatorSigPrefix+jobID, sig)
	return err
}

func (e *Engine) getRefinedCounts(ctx context.Context, jobID string) map[string]int {
	out := map[string]int{}
	var raw string
	err := e.db.DB().QueryRowContext(ctx,
		`SELECT value FROM system_state WHERE key = ?`, regulatorRefinedPrefix+jobID).Scan(&raw)
	if err != nil {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func (e *Engine) setRefinedCounts(ctx context.Context, jobID string, counts map[string]int) error {
	raw, err := json.Marshal(counts)
	if err != nil {
		return err
	}
	_, err = e.db.DB().ExecContext(ctx,
		`INSERT INTO system_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		regulatorRefinedPrefix+jobID, string(raw))
	return err
}
