package engine

import (
	"context"
	"testing"
)

// TestTerminalClearsJobScratch proves a finished job leaves no per-job
// `system_state` rows behind (M9: the difficulty hint, the reflection and
// self-refine counters, and the §10.5 suppression row are per-job scratch).
func TestTerminalClearsJobScratch(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	jobID := env.submit(t)

	for _, k := range jobScratchKeys(jobID) {
		if _, err := env.db.DB().ExecContext(ctx,
			`INSERT INTO system_state (key, value) VALUES (?, 'x')
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k); err != nil {
			t.Fatal(err)
		}
	}

	env.eng.Run(ctx, jobID) // drives the job to a terminal state

	var n int
	if err := env.db.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM system_state WHERE key LIKE ?`, "%"+jobID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("system_state rows for the job = %d, want 0 after a terminal transition", n)
	}
}
