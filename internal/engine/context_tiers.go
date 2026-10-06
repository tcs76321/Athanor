package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/corrections"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// M5-T5 (§10.5, ADR-0023): the engine's side of the context priority
// queue. internal/prompt owns the tier algebra (pricing, ladder,
// rendering); this file owns tier *content* — the MCE working set, the §25
// tool envelope, the persisted suppression state — and the §10.4 eviction
// seam that walks the ladder.

// ContextQuery identifies the working set to load. JobID is the context_swap
// scope (the active chunk). ProjectID, Query, and Limit scope the
// project-repository rows the Dormant Index publishes (F5, ADR-0059): Query
// ranks them by summary relevance and Limit caps them (≤0 asks the provider
// for its default).
type ContextQuery struct {
	JobID     string
	ProjectID string
	Query     string
	Limit     int
}

// ContextProvider is the engine's window onto the MCE working set
// (ADR-0023 §7). The production implementation lives in cmd/ over
// mce.ChunkStore (ADR-0019 inversion: the MCE must not import the engine).
//
// A nil provider is valid configuration: tiers 3 and 6 are empty, so
// prompts stay byte-identical to the pre-T5 assembler. That is what every
// unit test that predates M5-T5 relies on.
type ContextProvider interface {
	// ActiveChunk returns the §10.1 chunk currently active for the job's
	// scope. ok is false when no chunk is active. When the provider is
	// configured to seed (context_engine.seed_active_chunk), it may activate
	// the top-ranked project chunk before returning.
	ActiveChunk(ctx context.Context, q ContextQuery) (chunk prompt.ChunkText, ok bool, err error)
	// DormantIndex returns the §10.1 Dormant Index rows for the job: its own
	// chunks plus ranked, bounded project-repository rows.
	DormantIndex(ctx context.Context, q ContextQuery) ([]prompt.IndexLine, error)
}

// Eviction is the outcome of one §10.4 force-eviction (ADR-0022 §5).
type Eviction struct {
	// Tiers is what the seam moved to Dormant, lowest priority first.
	// Empty means nothing was evictable, and the §10.4 gate must pause.
	Tiers []prompt.Tier
	// FreedTokens is the summed estimated weight of Tiers.
	FreedTokens int
}

// Evictor is §10.4's force-eviction seam. The signature evolved in M5-T5
// (ADR-0022 §5 anticipated it): the seam now receives the tier weights
// from the assembly that triggered it, so the ladder can pick tiers
// without re-deriving prices, and it is stateless — the engine persists
// whatever it returns.
//
// A nil Evictor is still valid configuration: `critical` pressure then
// treats the call as a floor breach and pauses. Production wires
// NewLadderEvictor() at boot (M5-T5.6).
type Evictor interface {
	Evict(ctx context.Context, jobID string, weights map[prompt.Tier]int, suppressed []prompt.Tier) (Eviction, error)
}

// ladderEvictor is §10.5's ladder as the §10.4 seam (ADR-0023 §6): exactly
// one step down, lowest priority first. It is deliberately stateless, so
// production wiring (`NewLadderEvictor`) needs no engine reference and no
// second construction phase.
type ladderEvictor struct{}

// NewLadderEvictor returns the production §10.4 eviction seam.
func NewLadderEvictor() Evictor { return ladderEvictor{} }

func (ladderEvictor) Evict(_ context.Context, _ string,
	weights map[prompt.Tier]int, suppressed []prompt.Tier) (Eviction, error) {

	tier, tokens, ok := prompt.EvictNext(weights, suppressed)
	if !ok {
		// Only tiers 1–3 remain: there is no remedy, and the caller's
		// floor-breach pause is the correct outcome.
		return Eviction{}, nil
	}
	return Eviction{Tiers: []prompt.Tier{tier}, FreedTokens: tokens}, nil
}

// evictionStatePrefix is the `system_state` key prefix for a job's
// suppressed tiers (ADR-0023 §5) — the `reflect:counter:` pattern from
// M3-T4, applied to context pressure.
const evictionStatePrefix = "context:evicted:"

// evictionStateKey names the suppression row for one job.
func evictionStateKey(jobID string) string { return evictionStatePrefix + jobID }

// loadSuppressedTiers reads the persisted suppression set for a job.
//
// A missing row is the normal initial state. A corrupt row (bad JSON) is
// logged and treated as empty rather than failing the job: the ladder
// re-derives evictions on the next overflow, so the worst case is one
// re-eviction, not a stuck job. Pinned tiers are dropped here as well as
// in the assembler — defense in depth against a hand-edited row.
func (e *Engine) loadSuppressedTiers(ctx context.Context, jobID string) []prompt.Tier {
	var raw string
	err := e.db.DB().QueryRowContext(ctx,
		`SELECT value FROM system_state WHERE key = ?`, evictionStateKey(jobID)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		slog.Error("engine: reading eviction state", "job", jobID, "err", err)
		return nil
	}
	var stored []prompt.Tier
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		slog.Error("engine: decoding eviction state", "job", jobID, "err", err, "raw", raw)
		return nil
	}
	return normalizeSuppressed(stored)
}

// saveSuppressedTiers persists a job's suppression set (evictable tiers
// only, ascending). An empty set deletes the row so `system_state` stays
// bounded.
func (e *Engine) saveSuppressedTiers(ctx context.Context, jobID string, tiers []prompt.Tier) {
	out := normalizeSuppressed(tiers)
	if len(out) == 0 {
		e.clearSuppressedTiers(ctx, jobID)
		return
	}
	raw, err := json.Marshal(out)
	if err != nil {
		slog.Error("engine: encoding eviction state", "job", jobID, "err", err)
		return
	}
	if _, err := e.db.DB().ExecContext(ctx,
		`INSERT INTO system_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		evictionStateKey(jobID), string(raw)); err != nil {
		slog.Error("engine: writing eviction state", "job", jobID, "err", err)
	}
}

// clearSuppressedTiers removes a job's suppression row. Called when the
// job reaches a terminal state: a finished job's evictions are history,
// not state, and keeping the row would leave one row per evicting job.
func (e *Engine) clearSuppressedTiers(ctx context.Context, jobID string) {
	if _, err := e.db.DB().ExecContext(ctx,
		`DELETE FROM system_state WHERE key = ?`, evictionStateKey(jobID)); err != nil {
		slog.Error("engine: clearing eviction state", "job", jobID, "err", err)
	}
}

// normalizeSuppressed drops pinned and duplicate tiers and returns the
// remainder in ascending order (deterministic for the stored JSON).
func normalizeSuppressed(tiers []prompt.Tier) []prompt.Tier {
	if len(tiers) == 0 {
		return nil
	}
	seen := make(map[prompt.Tier]bool, len(tiers))
	out := make([]prompt.Tier, 0, len(tiers))
	for _, t := range tiers {
		if !t.Evictable() || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) == 0 {
		// "Nothing suppressed" is nil, never an empty non-nil slice: the
		// zero value is what every caller compares against.
		return nil
	}
	return out
}

// ceilingFor is §10.5's assembly budget (ADR-0023 §3): the §10.4 critical
// threshold applied to the persona's window, leaving the residual as
// generation headroom. A non-positive threshold or window means unbounded
// — the pre-T5 behavior — rather than "evict everything".
func ceilingFor(ce config.ContextEngine, window int) int {
	if window <= 0 || ce.KVCacheCriticalThresh <= 0 {
		return 0
	}
	return int(ce.KVCacheCriticalThresh * float64(window))
}

// toolManifest resolves the §25 envelope the server would enforce for this
// task, so the prompt can never promise a tool the route would refuse
// (ADR-0023 §8). A task override wins over the daemon default; an
// unparseable envelope yields no manifest at all, because claiming fewer
// tools is the safe direction.
func (e *Engine) toolManifest(t project.Task) []string {
	names := e.cfg.JobPod.DefaultTools
	if len(t.AllowedTools) > 0 {
		names = t.AllowedTools
	}
	env, err := toolenvelope.Parse(names)
	if err != nil {
		slog.Warn("engine: unparseable tool envelope; rendering no manifest",
			"task", t.ID, "err", err)
		return nil
	}
	tools := env.Tools()
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, string(tool))
	}
	return out
}

// promptTiers is the §10.5 tier payload set for one call.
type promptTiers struct {
	Tools         []string
	ActiveChunk   *prompt.ChunkText
	Candidates    []prompt.CandidateArtifact
	DormantIndex  []prompt.IndexLine
	Corrections   []string
	CorrectionIDs []string
}

// maxInjectedCorrections caps how many corrections retrieval feeds the
// assembler. Token pressure is the §10.5 ladder's job; this cap only stops a
// flood of tiny corrections from crowding the section.
const maxInjectedCorrections = 8

// renderCorrection renders one §18.2 record as a single §11.2 §8 line: the
// actionable derived rule, tagged with the category/severity/scope that
// ranked it.
func renderCorrection(r corrections.Record) string {
	return fmt.Sprintf("[%s/%s %s] %s", r.Category, r.Severity, r.Scope, r.DerivedRule)
}

// promptTiersFor gathers the tier payloads for one call. Provider failures
// degrade to empty tiers with a warning — the M5-T2 posture (a missing
// chunk must not fail a job that can otherwise proceed); they are never
// silently presented as "no working set" without the log line.
func (e *Engine) promptTiersFor(ctx context.Context, j job.Job, t project.Task,
	candidates []prompt.CandidateArtifact) promptTiers {

	pt := promptTiers{Tools: e.toolManifest(t), Candidates: candidates}
	// M6-T7 (§18.3, ADR-0038): retrieval is deterministic — severity, then
	// scope, then recency — so a high-severity project correction outranks
	// everything lower.
	if e.correctionSource != nil {
		recs, err := e.correctionSource.Relevant(ctx, t.ProjectID, maxInjectedCorrections)
		if err != nil {
			slog.Warn("engine: reading corrections", "task", t.ID, "err", err)
		} else {
			for _, rec := range recs {
				pt.Corrections = append(pt.Corrections, renderCorrection(rec))
				pt.CorrectionIDs = append(pt.CorrectionIDs, rec.ID)
			}
		}
	}
	if e.ctxProvider == nil {
		return pt
	}
	q := e.contextQuery(j, t)
	chunk, ok, err := e.ctxProvider.ActiveChunk(ctx, q)
	switch {
	case err != nil:
		slog.Warn("engine: reading active chunk", "job", j.ID, "err", err)
	case ok:
		pt.ActiveChunk = &chunk
	}
	idx, err := e.ctxProvider.DormantIndex(ctx, q)
	if err != nil {
		slog.Warn("engine: reading dormant index", "job", j.ID, "err", err)
	} else {
		pt.DormantIndex = idx
		if len(idx) > 0 {
			// F5 (ADR-0059): the Dormant Index now carries project
			// repository rows, so record what was published.
			e.auditCat(ctx, j.ID, "context", map[string]any{
				"event": "dormant_index_sourced", "project": j.ProjectID,
				"rows": len(idx), "limit": q.Limit,
			})
		}
	}
	return pt
}

// contextQuery builds the MCE working-set query for one call: the job scope
// (the active chunk), the project scope and configured limit (the ranked
// repository rows), and a relevance hint from the task title, description,
// and acceptance criteria.
func (e *Engine) contextQuery(j job.Job, t project.Task) ContextQuery {
	q := ContextQuery{JobID: j.ID, ProjectID: j.ProjectID}
	if e.cfg != nil {
		q.Limit = e.cfg.ContextEngine.RepositoryIndexLimit
	}
	var b strings.Builder
	b.WriteString(t.Title)
	for _, part := range append([]string{t.Description}, t.Criteria...) {
		if part == "" {
			continue
		}
		b.WriteByte('\n')
		b.WriteString(part)
	}
	q.Query = b.String()
	return q
}
