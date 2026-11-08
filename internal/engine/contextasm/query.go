// Package contextasm owns the pre-call context policy: the §10.5 tier
// ladder, the §10.4 pressure triggers, the MCE working set, correction and
// memory retrieval, per-job tier suppression, and the adaptive window planner.
//
// It is a leaf: the root engine asks it to prepare a prompt and then invokes
// the model itself. Context assembly never calls the model, never transitions a
// job, and never touches the database except through jobstate. That boundary
// keeps assembly policy testable and puts every lifecycle transition in one
// place.
package contextasm

import (
	"context"

	"github.com/tcs76321/athanor/internal/prompt"
)

// Query identifies the working set to load. JobID is the context_swap scope
// (the active chunk). ProjectID, Query, and Limit scope the project-repository
// rows the Dormant Index publishes (F5, ADR-0059): Query ranks them by summary
// relevance and Limit caps them (≤0 asks the provider for its default).
type Query struct {
	JobID     string
	ProjectID string
	Query     string
	Limit     int
}

// Provider is the engine's window onto the MCE working set (ADR-0023 §7). The
// production implementation lives in cmd/ over mce.ChunkStore (ADR-0019
// inversion: the MCE must not import the engine).
//
// A nil provider is valid configuration: tiers 3 and 6 are empty, so prompts
// stay byte-identical to the pre-T5 assembler.
type Provider interface {
	// ActiveChunk returns the §10.1 chunk currently active for the job's
	// scope. ok is false when no chunk is active.
	ActiveChunk(ctx context.Context, q Query) (chunk prompt.ChunkText, ok bool, err error)
	// DormantIndex returns the §10.1 Dormant Index rows for the job: its own
	// chunks plus ranked, bounded project-repository rows.
	DormantIndex(ctx context.Context, q Query) ([]prompt.IndexLine, error)
}

// WorkingSetProvider is the optional P0 extension: the ranked, byte-capped
// project chunk bodies that form the repository working set. It is separate so
// every pre-P0 fake keeps satisfying Provider without a stub method.
type WorkingSetProvider interface {
	WorkingSet(ctx context.Context, q Query, maxChunks, maxBytes int) ([]prompt.ChunkText, error)
}

// ActiveSeeder is the optional engine-driven §10.1 swap: choose the top-ranked
// project chunk and make it the job's active chunk (byte-exact, persisted).
type ActiveSeeder interface {
	SeedActive(ctx context.Context, q Query, maxBytes int) (prompt.ChunkText, bool, error)
}
