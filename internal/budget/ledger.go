package budget

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNoWindow is returned when a grant has no matching window row.
var ErrNoWindow = errors.New("budget: no window")

// Ledger is the store-backed half of ADR-0091. It owns the reserve-then-settle
// protocol; callers never write the tables directly.
type Ledger struct {
	db *sql.DB
}

// New returns a ledger over db.
func New(db *sql.DB) *Ledger { return &Ledger{db: db} }

// Grant is one job's allocation.
type Grant struct {
	JobID       string
	Window      time.Time
	Priority    Priority
	Tokens      int64
	Wall        time.Duration
	TokensSpent int64
	WallSpent   time.Duration
	State       string
}

// Status is the window's committed spend and the two buckets' remaining
// allowances. Remaining is clamped at zero.
type Status struct {
	Window              time.Time     `json:"window_start"`
	End                 time.Time     `json:"window_end"`
	TokensCeiling       int64         `json:"tokens_ceiling"`
	WallCeiling         time.Duration `json:"wall_ns_ceiling"`
	TokensCommitted     int64         `json:"tokens_committed"`
	WallCommitted       time.Duration `json:"wall_ns_committed"`
	InteractiveLeft     int64         `json:"interactive_tokens_left"`
	BackgroundLeft      int64         `json:"background_tokens_left"`
	InteractiveWallLeft time.Duration `json:"interactive_wall_ns_left"`
	BackgroundWallLeft  time.Duration `json:"background_wall_ns_left"`
}

// Reserve requests an allocation for jobID. It is idempotent while the job
// holds a live reservation in the window. A zero grant with granted=false means
// the bucket is exhausted; the caller defers rather than failing.
func (l *Ledger) Reserve(ctx context.Context, w Window, s Settings, jobID string, p Priority,
	requestTokens int64, requestWall time.Duration) (Grant, bool, error) {
	if !s.Enabled() {
		return Grant{}, true, nil
	}
	if !p.Valid() {
		return Grant{}, false, fmt.Errorf("budget: invalid priority %q", p)
	}
	key := windowKey(w)
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return Grant{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureWindow(ctx, tx, key, w, s); err != nil {
		return Grant{}, false, err
	}

	var id string
	var tokens, wallNS, spent, wallSpent int64
	var state string
	err = tx.QueryRowContext(ctx,
		`SELECT id, tokens, wall_ns, tokens_spent, wall_ns_spent, state
		   FROM budget_grants WHERE job_id = ? AND window_start = ?`, jobID, key).
		Scan(&id, &tokens, &wallNS, &spent, &wallSpent, &state)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Grant{}, false, err
	}
	if exists && state == "reserved" {
		if err := tx.Commit(); err != nil {
			return Grant{}, false, err
		}
		return Grant{JobID: jobID, Window: w.Start, Priority: p, Tokens: tokens,
			Wall: time.Duration(wallNS), TokensSpent: spent,
			WallSpent: time.Duration(wallSpent), State: state}, true, nil
	}

	committed, wallCommitted, err := committedIn(ctx, tx, key)
	if err != nil {
		return Grant{}, false, err
	}
	grantTokens := requestTokens
	if s.Tokens > 0 {
		remaining := s.CeilingFor(p, s.Tokens) - committed
		grantTokens = GrantSize(requestTokens, s.MaxGrant, remaining)
		if grantTokens <= 0 {
			return Grant{}, false, nil
		}
	}
	grantWall := requestWall
	if s.Wall > 0 {
		wallRemaining := WallCeilingFor(p, s.Wall, s.ReservePct, s.BackgroundPct) - wallCommitted
		grantWall = WallGrantSize(requestWall, wallRemaining)
		if grantWall <= 0 && requestWall > 0 {
			return Grant{}, false, nil
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if exists {
		// Keep the prior partial spend; add the new grant on top.
		_, err = tx.ExecContext(ctx,
			`UPDATE budget_grants
			    SET priority = ?, tokens = tokens_spent + ?, wall_ns = wall_ns_spent + ?,
			        state = 'reserved', granted_at = ?, settled_at = NULL
			  WHERE id = ?`,
			string(p), grantTokens, int64(grantWall), now, id)
	} else {
		id = jobID + "@" + key
		_, err = tx.ExecContext(ctx,
			`INSERT INTO budget_grants
			   (id, job_id, window_start, priority, tokens, wall_ns, tokens_spent, wall_ns_spent, state, granted_at)
			 VALUES (?, ?, ?, ?, ?, ?, 0, 0, 'reserved', ?)`,
			id, jobID, key, string(p), grantTokens, int64(grantWall), now)
	}
	if err != nil {
		return Grant{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Grant{}, false, err
	}
	return Grant{JobID: jobID, Window: w.Start, Priority: p, Tokens: grantTokens,
		Wall: grantWall, State: "reserved"}, true, nil
}

// Spend records incremental usage against the job's live grant and returns the
// remaining tokens (negative means the grant is overdrawn). Accounting must
// never fail a phase, so callers log and continue on error.
func (l *Ledger) Spend(ctx context.Context, jobID string, tokens int64, wall time.Duration) (int64, error) {
	if l == nil || l.db == nil {
		return 0, nil
	}
	if tokens < 0 {
		tokens = 0
	}
	_, err := l.db.ExecContext(ctx,
		`UPDATE budget_grants
		    SET tokens_spent = tokens_spent + ?, wall_ns_spent = wall_ns_spent + ?
		  WHERE id = (SELECT id FROM budget_grants
		               WHERE job_id = ? AND state = 'reserved'
		               ORDER BY granted_at DESC LIMIT 1)`,
		tokens, int64(wall), jobID)
	if err != nil {
		return 0, err
	}
	var remaining int64
	err = l.db.QueryRowContext(ctx,
		`SELECT tokens - tokens_spent FROM budget_grants
		  WHERE job_id = ? AND state = 'reserved'
		  ORDER BY granted_at DESC LIMIT 1`, jobID).Scan(&remaining)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return remaining, err
}

// Settle closes the live grant with its actual spend. A missing or already
// closed grant is a no-op.
func (l *Ledger) Settle(ctx context.Context, jobID string, tokens int64, wall time.Duration) error {
	if l == nil || l.db == nil {
		return nil
	}
	_, err := l.db.ExecContext(ctx,
		`UPDATE budget_grants
		    SET state = 'settled', tokens_spent = ?, wall_ns_spent = ?, settled_at = ?
		  WHERE id = (SELECT id FROM budget_grants
		               WHERE job_id = ? AND state = 'reserved'
		               ORDER BY granted_at DESC LIMIT 1)`,
		tokens, int64(wall), time.Now().UTC().Format(time.RFC3339Nano), jobID)
	return err
}

// Release returns an allocation without settling a completed run. Actual spend
// recorded by Spend is kept, so a deferred job's partial cost still counts.
func (l *Ledger) Release(ctx context.Context, jobID string) error {
	if l == nil || l.db == nil {
		return nil
	}
	_, err := l.db.ExecContext(ctx,
		`UPDATE budget_grants SET state = 'released', settled_at = ?
		  WHERE id = (SELECT id FROM budget_grants
		               WHERE job_id = ? AND state = 'reserved'
		               ORDER BY granted_at DESC LIMIT 1)`,
		time.Now().UTC().Format(time.RFC3339Nano), jobID)
	return err
}

// Status reports the window's committed spend and remaining buckets.
func (l *Ledger) Status(ctx context.Context, w Window, s Settings) (Status, error) {
	key := windowKey(w)
	committed, wallCommitted, err := committedIn(ctx, l.db, key)
	if err != nil {
		return Status{}, err
	}
	st := Status{
		Window:          w.Start,
		End:             w.End,
		TokensCeiling:   s.Tokens,
		WallCeiling:     s.Wall,
		TokensCommitted: committed,
		WallCommitted:   wallCommitted,
	}
	st.InteractiveLeft = clamp0(s.CeilingFor(Interactive, s.Tokens) - committed)
	st.BackgroundLeft = clamp0(s.CeilingFor(Background, s.Tokens) - committed)
	st.InteractiveWallLeft = clampDur(WallCeilingFor(Interactive, s.Wall, s.ReservePct, s.BackgroundPct) - wallCommitted)
	st.BackgroundWallLeft = clampDur(WallCeilingFor(Background, s.Wall, s.ReservePct, s.BackgroundPct) - wallCommitted)
	return st, nil
}

func ensureWindow(ctx context.Context, tx *sql.Tx, key string, w Window, s Settings) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO budget_windows
		   (window_start, window_end, tokens_ceiling, wall_ns_ceiling, reserve_percent)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(window_start) DO UPDATE SET
		   window_end = excluded.window_end,
		   tokens_ceiling = excluded.tokens_ceiling,
		   wall_ns_ceiling = excluded.wall_ns_ceiling,
		   reserve_percent = excluded.reserve_percent,
		   updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		key, w.End.UTC().Format(time.RFC3339Nano), s.Tokens, int64(s.Wall), s.ReservePct)
	return err
}

// querier is the shared surface of *sql.DB and *sql.Tx.
type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func committedIn(ctx context.Context, q querier, key string) (int64, time.Duration, error) {
	var tokens, wall int64
	err := q.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(CASE WHEN state = 'reserved' THEN tokens ELSE tokens_spent END), 0),
		        COALESCE(SUM(CASE WHEN state = 'reserved' THEN wall_ns ELSE wall_ns_spent END), 0)
		   FROM budget_grants WHERE window_start = ?`, key).Scan(&tokens, &wall)
	return tokens, time.Duration(wall), err
}

func windowKey(w Window) string {
	return w.Start.UTC().Format(time.RFC3339Nano)
}

func clamp0(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

func clampDur(v time.Duration) time.Duration {
	if v < 0 {
		return 0
	}
	return v
}
