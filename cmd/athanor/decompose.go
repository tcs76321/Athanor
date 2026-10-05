package main

import (
	"time"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/dag"
	"github.com/tcs76321/athanor/internal/decompose"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

// newDecomposer builds the M6-T1 DAG decomposer (ADR-0032) over the
// daemon's shared LLM client, persona registry, and project repository.
// The graph bounds come from the execution config; the model-call timeout
// reuses the planning per-phase budget.
func newDecomposer(cfg *config.Config, client *llm.Client, registry *llm.Registry,
	projects *project.Repo, events *store.Store) *decompose.Decomposer {
	limits := dag.Limits{
		MaxTasks:     cfg.Execution.DAGMaxTasks,
		MaxDepth:     cfg.Execution.DAGMaxDepth,
		MaxTotalJobs: cfg.Execution.DAGMaxTotalJobs,
	}
	timeout, ok := cfg.Execution.PhaseBudget(llm.PhasePlanning)
	if !ok || timeout <= 0 {
		timeout = 10 * time.Minute
	}
	return decompose.New(client, registry, projects, events, cfg.ContextEngine, limits, timeout)
}

// compile-time proof that *llm.Client satisfies the decomposer's seam.
var _ decompose.LLMClient = (*llm.Client)(nil)
