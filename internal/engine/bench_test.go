package engine

import (
	"context"
	"fmt"
	"testing"
)

// benchSeq is a package-level counter so each benchmark iteration gets
// a unique project name (the projects.name column is UNIQUE).
var benchSeq int

// submitForBench creates a queued job for a benchmark iteration. It is
// the bench analog of testEnv.submit; it does not call t.Fatal so it
// works under b.Fatalf and b.Errorf.
func (e *testEnv) submitForBench(b *testing.B) string {
	b.Helper()
	benchSeq++
	_, task, err := e.projects.Create(context.Background(),
		fmt.Sprintf("bench-%d", benchSeq), "text",
		"Write a short essay about local-first software.", "", nil)
	if err != nil {
		b.Fatalf("creating project: %v", err)
	}
	j, err := e.jobs.Create(context.Background(), task.ID, task.ProjectID)
	if err != nil {
		b.Fatalf("creating job: %v", err)
	}
	return j.ID
}

// BenchmarkRunFullChain measures the wall time of a single job running
// the M1 phase chain end-to-end through a fake Ollama. The fake
// responds immediately, so this measures the engine's per-phase
// overhead (DB writes, prompt assembly, state transitions) plus the
// Enqueue poll — not LLM latency.
//
// Run with:
//
//	make bench
//
// or directly:
//
//	go test -bench=BenchmarkRunFullChain -benchtime=10x ./internal/engine/
//
// The baseline numbers (recorded in docs/benchmarks/engine-m1.txt when
// the benchmark is first run) are the comparison point for M3's
// multi-candidate evaluation. M3 should be slower per job (N candidates
// per phase) but the per-phase cost should remain in the same order
// of magnitude; if it isn't, the per-phase overhead has regressed
// somewhere.
func BenchmarkRunFullChain(b *testing.B) {
	// newEnv takes a *testing.T. We construct a fresh env once and
	// reuse it across iterations so b.N measures the steady-state
	// per-job cost, not per-job setup cost. The harness's t.Cleanup
	// registers a close on the DB, which is fine in a benchmark —
	// the OS reclaims it when the process exits.
	noop := &testing.T{}
	e := newEnv(noop)

	for b.Loop() {
		jobID := e.submitForBench(b)
		e.eng.Run(context.Background(), jobID)
	}
}
