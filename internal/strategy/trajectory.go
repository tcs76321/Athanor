package strategy

import (
	"context"
	"encoding/json"

	"github.com/tcs76321/athanor/internal/cognitive"
)

// OperationSamples counts, per cognitive operation, how many recent outcomes
// for an archetype recorded that operation (M8-T10). It is the exploration
// floor's input: an operation with fewer than the floor's sample count is
// "unproven" and must stay eligible so learned selection cannot starve it.
// limit <= 0 defaults to 20. An operation counts once per outcome.
func (r *Repo) OperationSamples(ctx context.Context, archetype string, limit int) (map[string]int, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.store.DB().QueryContext(ctx,
		`SELECT o.operations_json FROM strategy_outcomes o
		 JOIN strategy_profiles p ON p.id = o.strategy_profile_id
		 WHERE p.archetype = ?
		 ORDER BY o.created_at DESC, o.id DESC LIMIT ?`,
		archetype, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	counts := map[string]int{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var ops []cognitive.Operation
		if json.Unmarshal([]byte(raw), &ops) != nil {
			continue
		}
		seen := map[string]bool{}
		for _, op := range ops {
			if !seen[op.Name] {
				counts[op.Name]++
				seen[op.Name] = true
			}
		}
	}
	return counts, rows.Err()
}

// nonNilOperations normalizes a nil trajectory to an empty slice so the JSON
// column is always a valid array (migration 0024, M8-T7). The canonical
// vocabulary and the Operation type live in internal/cognitive (ADR-0064) —
// the single source of truth shared by strategy capture and the policy
// selection seam.
func nonNilOperations(ops []cognitive.Operation) []cognitive.Operation {
	return cognitive.NonNil(ops)
}
