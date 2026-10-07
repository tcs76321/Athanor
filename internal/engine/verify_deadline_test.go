package engine

import (
	"context"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// blockingRunner blocks RunTests until the context is done, simulating a
// hanging test command (an infinite loop, or a test blocked on stdin).
type blockingRunner struct{ *fakeRunner }

func (b *blockingRunner) RunTests(ctx context.Context, _ string, _ toolenvelope.ExecuteRequest) (toolenvelope.ExecuteResult, error) {
	<-ctx.Done()
	return toolenvelope.ExecuteResult{}, ctx.Err()
}

// TestToolDeadlineDoesNotHangJob is the regression guard for the soak stall: a
// hanging pod tool call must be bounded by the evaluating phase budget and
// soft-fail the candidate, never stall the job. Before the fix the per-phase
// budget was applied only to LLM calls, so the job sat in `evaluating` forever.
func TestToolDeadlineDoesNotHangJob(t *testing.T) {
	e := newEnvWithCfg(t, func(c *config.Config) {
		c.Execution.DivergenceCandidates = 1
		zero := 0
		c.Execution.MaxReflectionLoops = &zero
		c.Execution.PhaseWallTimeBudgets = map[string]config.Duration{
			"evaluating": config.Duration(300 * time.Millisecond),
		}
	})
	e.eng.runner = &blockingRunner{fakeRunner: e.runner}

	jobID := e.submitCode(t)
	done := make(chan struct{})
	go func() {
		e.eng.Run(context.Background(), jobID)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("job hung: the tool deadline did not bound the hanging test")
	}
	if _, ok := eventField(t, e, jobID, "tool_deadline_exceeded", "tool"); !ok {
		t.Error("no tool_deadline_exceeded audit row")
	}
}
