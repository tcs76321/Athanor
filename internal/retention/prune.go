package retention

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tcs76321/athanor/internal/store"
)

// PruneResult counts auxiliary rows removed by one pass.
type PruneResult struct {
	Daydream      int64
	BudgetGrants  int64
	BudgetWindows int64
	Alarms        int64
	Archives      int
}

// PruneAux removes old daydream logs, closed budget windows (and their grants),
// and resolved alarms. It never touches jobs, artifacts, corrections, or
// quarantine rows.
func PruneAux(ctx context.Context, st *store.Store, daydreamBefore, budgetBefore, alarmsBefore time.Time) (PruneResult, error) {
	var out PruneResult
	exec := func(q string, arg any) (int64, error) {
		res, err := st.DB().ExecContext(ctx, q, arg)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}
	var err error
	if !daydreamBefore.IsZero() {
		if out.Daydream, err = exec(`DELETE FROM daydream_logs WHERE started_at < ?`, daydreamBefore.UTC().Format(time.RFC3339Nano)); err != nil {
			return out, fmt.Errorf("retention: pruning daydream logs: %w", err)
		}
	}
	if !budgetBefore.IsZero() {
		cutoff := budgetBefore.UTC().Format("2006-01-02T15:04:05.000Z")
		if out.BudgetGrants, err = exec(
			`DELETE FROM budget_grants WHERE window_start IN
			   (SELECT window_start FROM budget_windows WHERE window_end < ?)`, cutoff); err != nil {
			return out, fmt.Errorf("retention: pruning budget grants: %w", err)
		}
		if out.BudgetWindows, err = exec(`DELETE FROM budget_windows WHERE window_end < ?`, cutoff); err != nil {
			return out, fmt.Errorf("retention: pruning budget windows: %w", err)
		}
	}
	if !alarmsBefore.IsZero() {
		if out.Alarms, err = exec(
			`DELETE FROM alarms WHERE status = 'resolved' AND resolved_at IS NOT NULL AND resolved_at < ?`,
			alarmsBefore.UTC().Format(time.RFC3339Nano)); err != nil {
			return out, fmt.Errorf("retention: pruning alarms: %w", err)
		}
	}
	return out, nil
}

// PruneArchives removes archive files older than keepDays or beyond maxBytes,
// oldest first. keepDays 0 means age is unlimited; maxBytes 0 means size is
// unlimited. Manifest rows are removed with the files.
func PruneArchives(ctx context.Context, st *store.Store, dir string, keepDays int, maxBytes int64) (int, error) {
	if keepDays <= 0 && maxBytes <= 0 {
		return 0, nil
	}
	archives, err := ListArchives(ctx, st)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	var total int64
	for _, a := range archives {
		total += a.Bytes
	}
	removed := 0
	for _, a := range archives {
		remove := false
		if keepDays > 0 {
			if created, perr := time.Parse(time.RFC3339Nano, a.CreatedAt); perr == nil {
				if created.Before(now.AddDate(0, 0, -keepDays)) {
					remove = true
				}
			}
		}
		if !remove && maxBytes > 0 && total > maxBytes {
			remove = true
		}
		if !remove {
			continue
		}
		path := filepath.Join(dir, filepath.Base(a.Path))
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("retention: removing archive %s: %w", path, err)
		}
		if _, err := st.DB().ExecContext(ctx, `DELETE FROM retention_archives WHERE id = ?`, a.ID); err != nil {
			return removed, fmt.Errorf("retention: deleting manifest %s: %w", a.ID, err)
		}
		total -= a.Bytes
		removed++
	}
	return removed, nil
}
