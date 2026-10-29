package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/prompt"
	"github.com/tcs76321/athanor/internal/store"
)

// panicProvider panics on the first context read. It simulates a phase bug
// (or a third-party parser panic) after the job has started.
type panicProvider struct{}

func (panicProvider) ActiveChunk(context.Context, ContextQuery) (prompt.ChunkText, bool, error) {
	panic("context provider exploded")
}

func (panicProvider) DormantIndex(context.Context, ContextQuery) ([]prompt.IndexLine, error) {
	panic("context provider exploded")
}

// TestRunContainsJobPanic proves a panic in a phase fails one job instead of
// killing the process, and that the post-mortem rows survive.
func TestRunContainsJobPanic(t *testing.T) {
	env := newEnv(t)
	jobID := env.submit(t)
	env.eng.ctxProvider = panicProvider{}

	// Run is synchronous; without the recover this call would panic the test
	// process.
	env.eng.Run(context.Background(), jobID)

	j, err := env.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatalf("loading job: %v", err)
	}
	if j.State != job.StateFailed {
		t.Fatalf("job state = %s, want failed", j.State)
	}

	events, err := env.db.QueryEvents(context.Background(), store.EventFilter{JobID: jobID})
	if err != nil {
		t.Fatalf("querying events: %v", err)
	}
	found := false
	for _, e := range events {
		if e.Category == "jobs" && strings.Contains(e.DataJSON, "job_panicked") {
			found = true
			break
		}
	}
	if !found {
		t.Error("no job_panicked audit event recorded")
	}
}
