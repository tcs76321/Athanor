package engine

import (
	"context"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/budget"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/store"
)

// TestHorizonAdmissionDefersThenSettles proves the ADR-0091 admission path:
// a full window defers the next job instead of failing it, and settlement
// returns the unspent allocation so the deferred job can run.
func TestHorizonAdmissionDefersThenSettles(t *testing.T) {
	env := newEnvWithCfg(t, func(c *config.Config) {
		c.Budget.TokensPerWindow = 100
		c.Budget.MaxGrantTokens = 100
	})
	ledger := budget.New(env.db.DB())
	env.eng.SetBudgetLedger(ledger)
	ctx := context.Background()

	_, _, job1 := env.createProjectTask(t, "text", "first horizon job with enough goal text")
	_, _, job2 := env.createProjectTask(t, "text", "second horizon job with enough goal text")

	if ok, _ := env.eng.admitHorizon(ctx, job1); !ok {
		t.Fatal("first job was deferred with a fresh window")
	}
	ok, wait := env.eng.admitHorizon(ctx, job2)
	if ok {
		t.Fatal("second job was admitted with an exhausted window")
	}
	if wait <= 0 {
		t.Errorf("defer wait = %s, want a positive retry", wait)
	}

	// A real call spends 50 tokens; settlement records it and frees the rest.
	if _, err := env.db.AppendEvent(ctx, store.Event{
		Category: "jobs", JobID: job1,
		Data: map[string]any{"event": "llm_call", "prompt_tokens": 30, "completion_tokens": 20},
	}); err != nil {
		t.Fatal(err)
	}
	env.eng.settleHorizon(ctx, job1)

	s := env.cfg.BudgetSettings()
	w, err := budget.WindowFor(time.Now(), s)
	if err != nil {
		t.Fatal(err)
	}
	st, err := ledger.Status(ctx, w, s)
	if err != nil {
		t.Fatal(err)
	}
	if st.TokensCommitted != 50 {
		t.Errorf("committed = %d, want 50 (settled actual spend)", st.TokensCommitted)
	}
	if ok, _ := env.eng.admitHorizon(ctx, job2); !ok {
		t.Fatal("second job still deferred after settlement freed budget")
	}
}

// TestHorizonHourlyCapDefers proves limits.max_tasks_per_hour is enforced at
// the same admission gate as the budget (ADR-0091 item 9).
func TestHorizonHourlyCapDefers(t *testing.T) {
	env := newEnvWithCfg(t, func(c *config.Config) {
		c.Budget.TokensPerWindow = 1000
		c.Budget.MaxGrantTokens = 100
		c.Limits.MaxTasksPerHour = 1
	})
	env.eng.SetBudgetLedger(budget.New(env.db.DB()))
	ctx := context.Background()

	_, _, job1 := env.createProjectTask(t, "text", "first horizon job with enough goal text")
	_, _, job2 := env.createProjectTask(t, "text", "second horizon job with enough goal text")
	if _, err := env.jobs.Transition(ctx, job1, job.StateContextBuilding); err != nil {
		t.Fatal(err)
	}

	ok, wait := env.eng.admitHorizon(ctx, job2)
	if ok {
		t.Fatal("second job was admitted above max_tasks_per_hour")
	}
	if wait != time.Minute {
		t.Errorf("wait = %s, want 1m", wait)
	}
}

// TestHorizonDisabledIsInert pins the safe default: no configured ceiling
// means no admission change.
func TestHorizonDisabledIsInert(t *testing.T) {
	env := newEnv(t)
	env.eng.SetBudgetLedger(budget.New(env.db.DB()))
	if s := env.cfg.BudgetSettings(); s.Enabled() {
		t.Fatal("default budget is enabled; it must stay inert")
	}
	if ok, retry := env.eng.admitHorizon(context.Background(), "missing"); !ok || retry != 0 {
		t.Errorf("disabled admission = ok=%v retry=%s", ok, retry)
	}
}
