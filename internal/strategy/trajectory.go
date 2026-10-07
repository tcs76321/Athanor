package strategy

import "github.com/tcs76321/athanor/internal/cognitive"

// nonNilOperations normalizes a nil trajectory to an empty slice so the JSON
// column is always a valid array (migration 0024, M8-T7). The canonical
// vocabulary and the Operation type live in internal/cognitive (ADR-0064) —
// the single source of truth shared by strategy capture and the policy
// selection seam.
func nonNilOperations(ops []cognitive.Operation) []cognitive.Operation {
	return cognitive.NonNil(ops)
}
