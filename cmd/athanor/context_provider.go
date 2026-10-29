// M5-T5.6 / F5: the ContextProvider adapter. internal/engine defines the seam
// (the ADR-0019 inversion: the MCE must not import the engine); this file is
// where the two meet, exactly like context_swap_adapter.go. The adapter maps
// storage records into the prompt package's tier payloads, so the engine never
// sees an mce type and internal/mce never sees the engine.
//
// F5 (ADR-0059) widens the adapter to the *automatic working set*: the Dormant
// Index now unions the job's own chunks with ranked, bounded project-repository
// chunks, and (opt-in) the top-ranked project chunk can be seeded as the active
// tier-3 chunk when a job has none.
package main

import (
	"context"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/engine"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/prompt"
)

// contextProvider adapts the MCE chunk store to engine.ContextProvider.
//
// `enabled` mirrors context_engine.enable_lossless_swapping (ADR-0021 §9):
// when false, ingestion refuses and the working set is unavailable, so the
// provider reports empty tiers rather than a working set the operator
// switched off. That is the same gate the context_swap route uses (501).
type contextProvider struct {
	store     *mce.ChunkStore
	retriever *mce.Retriever
	enabled   bool
	// F5 (ADR-0059): the repository Dormant Index bound and the opt-in
	// tier-3 seeding bounds, resolved from context_engine at boot.
	repositoryLimit int
	seedActive      bool
	seedMaxBytes    int
}

// Compile-time proof that the adapter satisfies the engine seam.
var _ engine.ContextProvider = (*contextProvider)(nil)

func newContextProvider(store *mce.ChunkStore, enabled bool, ce config.ContextEngine) *contextProvider {
	limit := ce.RepositoryIndexLimit
	if limit <= 0 {
		limit = mce.DefaultRepositoryIndexLimit
	}
	max := ce.SeedActiveMaxBytes
	if max <= 0 {
		max = 16384
	}
	return &contextProvider{
		store:           store,
		retriever:       store.Retriever(),
		enabled:         enabled,
		repositoryLimit: limit,
		seedActive:      ce.SeedActiveChunkEnabled(),
		seedMaxBytes:    max,
	}
}

// chunkText maps a stored chunk into the prompt tier payload.
func chunkText(rec mce.ChunkRecord) prompt.ChunkText {
	return prompt.ChunkText{
		ID:        rec.ID,
		RelPath:   rec.SourceRelPath,
		LineStart: rec.LineStart,
		LineEnd:   rec.LineEnd,
		Kind:      string(rec.Kind),
		Text:      string(rec.Content),
	}
}

// ActiveChunk returns the §10.1 chunk currently active for the job's scope
// (the job ID, matching toolenvelope.ContextSwapRequest.Scope). A missing row
// is "no chunk active", not an error: that is the normal state before any
// context_swap — or after an opt-in seed (below).
//
// When `seedActive` is on and no chunk is active, the adapter activates the
// single top-ranked project chunk, provided it fits seed_active_max_bytes.
// That is a deliberate side effect on a normally-read call (ADR-0059): tier 3
// is full-fidelity and never evicted, so seeding is byte-capped and off by
// default.
func (p *contextProvider) ActiveChunk(ctx context.Context, q engine.ContextQuery) (prompt.ChunkText, bool, error) {
	if !p.enabled || p.store == nil || q.JobID == "" {
		return prompt.ChunkText{}, false, nil
	}
	rec, ok, err := p.store.Active(ctx, q.JobID)
	if err != nil {
		return prompt.ChunkText{}, false, err
	}
	if ok {
		return chunkText(rec), true, nil
	}
	if !p.seedActive {
		return prompt.ChunkText{}, false, nil
	}
	entries, err := p.store.IndexForProject(ctx, q.ProjectID, q.Query, 1)
	if err != nil || len(entries) == 0 {
		return prompt.ChunkText{}, false, nil
	}
	target, err := p.store.Get(ctx, entries[0].ChunkID)
	if err != nil || len(target.Content) > p.seedMaxBytes {
		return prompt.ChunkText{}, false, nil
	}
	res, err := p.store.Swap(ctx, q.JobID, target.ID)
	if err != nil {
		return prompt.ChunkText{}, false, nil
	}
	return chunkText(res.Loaded), true, nil
}

// WorkingSet returns the P0 repository pack: the top-ranked project chunk
// bodies that fit the byte cap, excluding the job's active chunk (the prompt
// renderer dedupes as a second defence) and any chunk that would overflow the
// cap. Retrieval is FTS5 bm25 over the summary index plus the optional vector
// half; no model call is made. A nil/disabled provider returns no pack, which
// keeps the pre-P0 prompt byte-identical.
func (p *contextProvider) WorkingSet(ctx context.Context, q engine.ContextQuery,
	maxChunks, maxBytes int) ([]prompt.ChunkText, error) {
	if !p.enabled || p.store == nil || p.retriever == nil || q.ProjectID == "" ||
		maxChunks <= 0 || maxBytes <= 0 {
		return nil, nil
	}
	activeID := ""
	if rec, ok, err := p.store.Active(ctx, q.JobID); err == nil && ok {
		activeID = rec.ID
	}
	hits, err := p.retriever.Query(ctx, mce.QueryOptions{
		Query: q.Query,
		Scope: mce.Scope{ProjectID: q.ProjectID},
		TopK:  maxChunks * 2,
	})
	if err != nil {
		return nil, err
	}
	out := make([]prompt.ChunkText, 0, maxChunks)
	seen := map[string]bool{}
	used := 0
	add := func(id string) {
		if id == "" || id == activeID || seen[id] || len(out) >= maxChunks {
			return
		}
		rec, err := p.store.Get(ctx, id)
		if err != nil || len(rec.Content) == 0 {
			return
		}
		if used+len(rec.Content) > maxBytes {
			return
		}
		seen[id] = true
		out = append(out, chunkText(rec))
		used += len(rec.Content)
	}
	for _, hit := range hits {
		if hit.Kind == mce.SourceChunk {
			add(hit.ID)
		}
	}
	// Before summaries are ready the FTS index has no text to match, so a
	// fresh project would get no pack. Fall back to the most recently updated
	// project chunks; the caps still bound it and the prompt still marks the
	// content as data.
	if len(out) == 0 {
		if entries, ierr := p.store.IndexForProject(ctx, q.ProjectID, "", maxChunks*2); ierr == nil {
			for _, e := range entries {
				add(e.ChunkID)
			}
		}
	}
	return out, nil
}

// SeedActive makes the top-ranked project chunk the job's active chunk
// (byte-exact, persisted). It is the engine-driven §10.1 swap; the same
// ranking as the working-set pack is used, but the active pointer is what is
// persisted so a crash resumes the same working set.
func (p *contextProvider) SeedActive(ctx context.Context, q engine.ContextQuery,
	maxBytes int) (prompt.ChunkText, bool, error) {
	if !p.enabled || p.store == nil || p.retriever == nil || q.JobID == "" || q.ProjectID == "" {
		return prompt.ChunkText{}, false, nil
	}
	if maxBytes <= 0 {
		maxBytes = 16384
	}
	hits, err := p.retriever.Query(ctx, mce.QueryOptions{
		Query: q.Query,
		Scope: mce.Scope{ProjectID: q.ProjectID},
		TopK:  8,
	})
	if err != nil {
		return prompt.ChunkText{}, false, err
	}
	try := func(id string) (prompt.ChunkText, bool, error) {
		rec, gerr := p.store.Get(ctx, id)
		if gerr != nil || len(rec.Content) == 0 || len(rec.Content) > maxBytes {
			return prompt.ChunkText{}, false, nil
		}
		res, serr := p.store.Swap(ctx, q.JobID, rec.ID)
		if serr != nil {
			return prompt.ChunkText{}, false, serr
		}
		return chunkText(res.Loaded), true, nil
	}
	for _, hit := range hits {
		if hit.Kind != mce.SourceChunk {
			continue
		}
		if chunk, ok, err := try(hit.ID); ok || err != nil {
			return chunk, ok, err
		}
	}
	// Before summaries are ready, fall back to the most recent project chunks.
	entries, err := p.store.IndexForProject(ctx, q.ProjectID, "", 8)
	if err != nil {
		return prompt.ChunkText{}, false, err
	}
	for _, e := range entries {
		if chunk, ok, err := try(e.ChunkID); ok || err != nil {
			return chunk, ok, err
		}
	}
	return prompt.ChunkText{}, false, nil
}

// Compile-time proof that the adapter satisfies the optional P0 seams.
var _ engine.WorkingSetProvider = (*contextProvider)(nil)
var _ engine.ActiveSeeder = (*contextProvider)(nil)

// DormantIndex returns the §10.1 Dormant Index for the job (§11.2 §10): the
// job's own chunks first, then the ranked, bounded project-repository rows,
// de-duplicated by chunk ID. Summary may be empty while summary_status is not
// yet 'ready'; the prompt's renderer discloses that rather than inventing a
// summary.
func (p *contextProvider) DormantIndex(ctx context.Context, q engine.ContextQuery) ([]prompt.IndexLine, error) {
	if !p.enabled || p.store == nil || q.JobID == "" {
		return nil, nil
	}
	jobEntries, err := p.store.IndexForJob(ctx, q.JobID)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = p.repositoryLimit
	}
	projectEntries, err := p.store.IndexForProject(ctx, q.ProjectID, q.Query, limit)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(jobEntries)+len(projectEntries))
	out := make([]prompt.IndexLine, 0, len(jobEntries)+len(projectEntries))
	add := func(e mce.IndexEntry) {
		if seen[e.ChunkID] {
			return
		}
		seen[e.ChunkID] = true
		out = append(out, prompt.IndexLine{
			ChunkID:   e.ChunkID,
			Summary:   e.Summary,
			RelPath:   e.SourceRelPath,
			LineStart: e.LineStart,
			LineEnd:   e.LineEnd,
			Kind:      string(e.Kind),
		})
	}
	for _, e := range jobEntries {
		add(e)
	}
	for _, e := range projectEntries {
		add(e)
	}
	return out, nil
}
