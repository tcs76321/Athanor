// M3-T2 commit 2.4: per-phase wall-time budget tests.
//
// The plumbing (config.Execution.PhaseBudget + context.WithTimeout
// in e.call) already exists; what this commit adds is the
// observability hook (the `context_deadline_exceeded` audit row)
// and the proof that:
//  1. a tight budget + slow Ollama surfaces a deadline error AND
//     writes the audit row (so post-mortem readers can see *why*).
//  2. the default 300s budget on `planning` does not fire for a
//     fast (250ms) call — i.e., the defaults are not silently zero.
package engine

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

// TestPerPhaseBudget_DeadlineExceeded_Audited proves the
// observability hook: a 200ms planning budget + a 500ms fake
// Ollama delay causes e.call to return a context-deadline
// error, AND a `context_deadline_exceeded` audit row is
// written to the EventLog. Without the audit row the failure
// would surface only as a job->failed transition; the row
// makes the *why* queryable.
func TestPerPhaseBudget_DeadlineExceeded_Audited(t *testing.T) {
	env := newEnvWithCfg(t, func(cfg *config.Config) {
		// Tight budget for the planning phase; the fake's
		// 500ms delay will exceed it.
		cfg.Execution.PhaseWallTimeBudgets = map[string]config.Duration{
			"planning": config.Duration(200 * time.Millisecond),
			"default":  config.Duration(200 * time.Millisecond),
		}
	})
	env.ollama.WithDelay(500 * time.Millisecond)

	jobID := env.submit(t)
	// Drive e.call directly. We use the test's already-created
	// project and a fresh job. The first call is the planning
	// phase; we exercise that.
	p, t0, err := env.contextsForTest(t, jobID)
	if err != nil {
		t.Fatalf("contextsForTest: %v", err)
	}
	if _, err := env.eng.call(context.Background(), mustJob(t, env, jobID),
		p, t0, llm.PhasePlanning, llm.RoleTall, "", nil); err == nil {
		t.Fatalf("call returned nil error; want a context-deadline error")
	}

	// Query the EventLog for the audit row. We expect
	// exactly one `context_deadline_exceeded` row.
	events, qerr := env.db.QueryEvents(context.Background(), store.EventFilter{JobID: jobID})
	if qerr != nil {
		t.Fatalf("QueryEvents: %v", qerr)
	}
	found := false
	for i := range events {
		var d struct {
			Event     string `json:"event"`
			Phase     string `json:"phase"`
			Persona   string `json:"persona"`
			BudgetSec int64  `json:"budget_sec"`
		}
		_ = json.Unmarshal([]byte(events[i].DataJSON), &d)
		if d.Event == "context_deadline_exceeded" {
			found = true
			if d.Phase != "planning" {
				t.Errorf("audit row phase = %q, want planning", d.Phase)
			}
			if d.Persona != "tall" {
				t.Errorf("audit row persona = %q, want tall", d.Persona)
			}
			// 200ms rounds to 0 seconds; the field is
			// documented as a second-granularity number
			// and is informational, not a budget
			// reproduction key. Future field: include
			// budget_milliseconds if precision is needed.
		}
	}
	if !found {
		t.Errorf("no context_deadline_exceeded audit row found; events = %d", len(events))
	}
}

// TestPerPhaseBudget_DefaultHonored proves the default
// 300s budget on `planning` does not fire for a 250ms fake
// Ollama delay. The 300s default is set in
// applyDefaults; this test exercises the round trip
// (cfg → PhaseBudget → e.call's WithTimeout) end-to-end.
func TestPerPhaseBudget_DefaultHonored(t *testing.T) {
	env := newEnv(t) // applyDefaults populates planning: 300s
	env.ollama.WithDelay(250 * time.Millisecond)

	jobID := env.submit(t)
	p, t0, err := env.contextsForTest(t, jobID)
	if err != nil {
		t.Fatalf("contextsForTest: %v", err)
	}
	if _, err := env.eng.call(context.Background(), mustJob(t, env, jobID),
		p, t0, llm.PhasePlanning, llm.RoleTall, "", nil); err != nil {
		t.Fatalf("call failed: %v (default 300s budget should be enough for a 250ms call)", err)
	}
}

// contextsForTest returns the Project and Task for the project
// a job belongs to. A small helper because the test's job
// creation path doesn't expose them.
func (e *testEnv) contextsForTest(t *testing.T, jobID string) (project.Project, project.Task, error) {
	t.Helper()
	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		return project.Project{}, project.Task{}, err
	}
	t1, err := e.projects.Task(context.Background(), j.TaskID)
	if err != nil {
		return project.Project{}, project.Task{}, err
	}
	p, err := e.projects.Get(context.Background(), t1.ProjectID)
	if err != nil {
		return project.Project{}, project.Task{}, err
	}
	return p, t1, nil
}

func mustJob(t *testing.T, e *testEnv, jobID string) job.Job {
	t.Helper()
	j, err := e.jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatalf("mustJob: %v", err)
	}
	return j
}
