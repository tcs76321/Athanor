package contextasm

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/corrections"
	"github.com/tcs76321/athanor/internal/engine/jobstate"
	"github.com/tcs76321/athanor/internal/interruptions"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/mce"
	"github.com/tcs76321/athanor/internal/notebook"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// Consumer ports. The assembler reads only what it needs; the root's adapters
// satisfy these structurally, including their larger exported interfaces.

// Corrections is the §18.3 retrieval surface: active corrections relevant to a
// project, severity-ordered.
type Corrections interface {
	Relevant(ctx context.Context, projectID string, limit int) ([]corrections.Record, error)
}

// Memory is the §10.2 retrieval surface: project-scoped compacted memory and
// dormant chunk hits.
type Memory interface {
	RelevantMemory(ctx context.Context, projectID, query string, limit int) ([]mce.MemoryHit, error)
}

// Insights is the §13.4 statement channel. Only active insights reach a prompt.
type Insights interface {
	ActiveStatements(ctx context.Context) ([]string, error)
}

// Interruptions is the §20.4 queue read side.
type Interruptions interface {
	Pending(ctx context.Context, jobID string) ([]interruptions.Note, error)
}

// Notebook is the ADR-0109 persistent working-memory read side.
type Notebook interface {
	Recent(ctx context.Context, jobID string, limit int) ([]notebook.Entry, error)
}

// Runtime exposes the seams the assembler reads. Every getter is called on each
// Prepare so a seam wired after engine.New (corrections, memory, insights,
// notebook, interruption queue, planner) takes effect without a restart.
type Runtime interface {
	Chunks() Provider
	Evictor() Evictor
	Corrections() Corrections
	Memory() Memory
	Insights() Insights
	Interruptions() Interruptions
	Notebook() Notebook
	Planner() Planner
	AuditCat(ctx context.Context, jobID, category string, data map[string]any)
}

// Assembler owns the context policy for one engine.
type Assembler struct {
	rt    Runtime
	cfg   *config.Config
	state *jobstate.Store
}

// New returns an assembler over the live runtime seams and the job scratch
// store.
func New(rt Runtime, cfg *config.Config, state *jobstate.Store) *Assembler {
	return &Assembler{rt: rt, cfg: cfg, state: state}
}

// PrepareRequest describes one LLM call's context need.
type PrepareRequest struct {
	Job            job.Job
	Project        project.Project
	Task           project.Task
	Phase          string
	Role           string
	Persona        llm.Persona
	Instructions   string
	Candidates     []prompt.CandidateArtifact
	AllowQuestions bool
}

// Prepared is the assembled prompt plus the metadata the caller needs for the
// post-call audit and state updates.
type Prepared struct {
	Persona               llm.Persona
	Messages              []llm.Message
	TotalToken            int
	Sections              []prompt.Section
	TierWeights           map[prompt.Tier]int
	Eviction              prompt.EvictionReport
	CorrectionIDs         []string
	CorrectionsSuppressed bool
	NoteIDs               []string
	StrategyNotes         int
}

// NeedsPause reports that assembly could not produce a safe prompt. The root
// performs the lifecycle transition and audit; this package never transitions a
// job. StopPod preserves the exact pre-split behavior of the pressure path.
type NeedsPause struct {
	Event   string
	Fields  map[string]any
	StopPod bool
}

func (n *NeedsPause) Error() string { return n.Event }

// promptTiers is the §10.5 tier payload set for one call.
type promptTiers struct {
	Tools         []string
	ActiveChunk   *prompt.ChunkText
	WorkingSet    []prompt.ChunkText
	Candidates    []prompt.CandidateArtifact
	DormantIndex  []prompt.IndexLine
	Corrections   []string
	CorrectionIDs []string
	Episodic      []string
}

// maxInjectedCorrections caps how many corrections retrieval feeds the
// assembler. Token pressure is the §10.5 ladder's job; this cap only stops a
// flood of tiny corrections from crowding the section.
const maxInjectedCorrections = 8

// Prepare builds the prompt for one phase call. It may return *NeedsPause when
// the context window cannot be made safe; every other error is a real failure.
func (a *Assembler) Prepare(ctx context.Context, req PrepareRequest) (Prepared, error) {
	if a.cfg == nil {
		return Prepared{}, fmt.Errorf("contextasm: config is nil")
	}
	capability := project.CapabilityFor(req.Project, req.Task)
	persona := req.Persona

	// ADR-0124: an adaptive planner chooses this job's window. A nil planner
	// (or one that declines) preserves the legacy fixed-floor path.
	planned := false
	if planner := a.rt.Planner(); planner != nil {
		demand := project.ContextDemand(req.Project, req.Task)
		pinned := prompt.EstimateTokens(req.Task.Title+"\n"+req.Task.Description+"\n"+strings.Join(req.Task.Criteria, "\n")) + 512
		working := 0
		if capability == project.CapabilityCode && a.cfg.ContextEngine.WorkingSetMaxBytes > 0 {
			working = a.cfg.ContextEngine.WorkingSetMaxBytes / 4
		}
		dec, derr := planner.PlanContext(Request{
			Phase: req.Phase, Capability: capability, Demand: demand,
			Model: persona.Model, ModelMax: persona.ContextTarget,
			Pinned: pinned, Working: working, Output: a.cfg.Inference.MaxOutputTokens,
		})
		if derr != nil {
			return Prepared{}, &NeedsPause{Event: "context_plan_failed", Fields: map[string]any{
				"phase": req.Phase, "persona": req.Role, "error": derr.Error(),
			}}
		}
		if dec.Target > 0 && dec.Target <= persona.ContextTarget {
			persona.ContextTarget = dec.Target
			planned = true
		}
		a.rt.AuditCat(ctx, req.Job.ID, "context", map[string]any{
			"event": "context_plan", "phase": req.Phase, "persona": req.Role,
			"demand": dec.Demand, "floor": dec.Floor, "target": dec.Target,
			"bucket": dec.Bucket, "planned": planned, "binding": dec.Binding,
			"pinned": pinned, "working": working,
		})
	}

	// M1-T2: feasibility before every call — never silently reduce. M5-T4:
	// this is the §12.6 pre-assembly gate (what the window must be); the
	// per-call §10.4 pressure gate below is its complement. The planner is
	// responsible for its own floor when it chose the window.
	if !planned {
		verdict := llm.Check(persona, req.Phase, capability, persona.ContextTarget, a.cfg.ContextEngine)
		if !verdict.Feasible {
			return Prepared{}, &NeedsPause{Event: "context_floor_violation", Fields: map[string]any{
				"phase": req.Phase, "persona": req.Role,
				"required": verdict.Required, "available": verdict.Available,
				"recommendation": verdict.Recommendation,
			}}
		}
	}

	tiers := a.gatherTiers(ctx, req.Job, req.Project, req.Task, req.Phase, req.Candidates)
	suppressed := a.loadSuppressed(ctx, req.Job.ID)
	ceiling := ceilingFor(a.cfg.ContextEngine, persona.ContextTarget)
	notes, noteIDs := a.pendingInterruptions(ctx, req.Job.ID)

	var strategyNotes []string
	if ins := a.rt.Insights(); ins != nil && config.Val(a.cfg.StrategyAnalysis.StrategyNotesInPrompts, true) {
		stmts, err := ins.ActiveStatements(ctx)
		if err != nil {
			slog.Warn("contextasm: reading strategy insights", "job", req.Job.ID, "err", err)
		} else {
			strategyNotes = stmts
		}
	}

	res, err := prompt.Assemble(prompt.Input{
		Phase:                  req.Phase,
		Project:                prompt.Project{Name: req.Project.Name, Archetype: capability, Goal: req.Project.Goal},
		Task:                   prompt.Task{Title: req.Task.Title, Description: req.Task.Description, TestCommand: req.Project.TestCommand()},
		Criteria:               req.Task.Criteria,
		Checks:                 req.Project.Execution.Checks.Describe(),
		DataSchema:             req.Project.Execution.DataSchema,
		AllowQuestions:         req.AllowQuestions,
		Notebook:               a.notebookLines(ctx, req.Job.ID),
		EvaluationInstructions: req.Instructions,
		Tools:                  tiers.Tools,
		ActiveChunk:            tiers.ActiveChunk,
		WorkingSet:             tiers.WorkingSet,
		Corrections:            tiers.Corrections,
		Episodic:               tiers.Episodic,
		Candidates:             tiers.Candidates,
		DormantIndex:           tiers.DormantIndex,
		InterruptionNotes:      notes,
		StrategyNotes:          strategyNotes,
		Ceiling:                ceiling,
		Suppressed:             suppressed,
	})
	if err != nil {
		return Prepared{}, err
	}

	// M5-T5: persist the assembly's own overflow evictions so the next call
	// starts from the reduced set (ADR-0023 §5, §23.6: a restart must not
	// resurrect evicted tiers).
	if len(res.Eviction.Evicted) > 0 {
		a.saveSuppressed(ctx, req.Job.ID, append(append([]prompt.Tier{}, suppressed...), res.Eviction.Evicted...))
	}
	a.rt.AuditCat(ctx, req.Job.ID, "inference", map[string]any{
		"event": "kv_cache_assembled", "phase": req.Phase, "persona": req.Role,
		"ceiling": ceiling, "assembled_tokens": res.TotalToken,
		"fits": res.Eviction.Fits, "dropped_tokens": res.Eviction.DroppedTokens,
		"evicted_tiers":    tierNames(res.Eviction.Evicted),
		"suppressed_tiers": tierNames(res.Eviction.Suppressed),
		"tier_weights":     tierWeights(res.TierWeights),
	})

	assessment := mce.Assess(res.TotalToken, persona.ContextTarget, a.cfg.ContextEngine)
	a.rt.AuditCat(ctx, req.Job.ID, "inference", map[string]any{
		"event": "kv_cache_pressure", "phase": req.Phase, "persona": req.Role,
		"active_tokens": res.TotalToken, "max_context": persona.ContextTarget,
		"pressure": assessment.Pressure, "action": string(assessment.Action),
	})
	switch assessment.Action {
	case mce.ActionNone:
		// Below the warning threshold: nothing to do.
	case mce.ActionWarn:
		// §10.4 85% arm: the audit row is the action.
	case mce.ActionCritical:
		// §10.4 95% arm: force-evict the lowest-priority tier through the
		// seam. The released budget materializes in the next assembly. A nil
		// seam — or one with nothing left to evict — cannot relieve the
		// pressure, so the call pauses rather than sending an over-limit prompt.
		eviction := Eviction{}
		if ev := a.rt.Evictor(); ev != nil {
			var eerr error
			eviction, eerr = ev.Evict(ctx, req.Job.ID, res.TierWeights, res.Eviction.Suppressed)
			if eerr != nil {
				return Prepared{}, fmt.Errorf("kv-cache eviction: %w", eerr)
			}
		}
		if len(eviction.Tiers) == 0 {
			return Prepared{}, &NeedsPause{Event: "context_floor_violation", StopPod: true, Fields: map[string]any{
				"trigger": "kv_cache_monitor", "phase": req.Phase, "persona": req.Role,
				"active_tokens": res.TotalToken, "max_context": persona.ContextTarget,
				"pressure": assessment.Pressure, "action": string(assessment.Action),
				"recommendation": assessment.Recommendation,
			}}
		}
		a.saveSuppressed(ctx, req.Job.ID, append(
			append([]prompt.Tier{}, res.Eviction.Suppressed...), eviction.Tiers...))
		a.rt.AuditCat(ctx, req.Job.ID, "inference", map[string]any{
			"event": "kv_cache_evicted", "phase": req.Phase, "persona": req.Role,
			"tokens_freed": eviction.FreedTokens, "pressure": assessment.Pressure,
			"evicted_tiers": tierNames(eviction.Tiers),
		})
	default: // mce.ActionFloorBreach
		return Prepared{}, &NeedsPause{Event: "context_floor_violation", StopPod: true, Fields: map[string]any{
			"trigger": "kv_cache_monitor", "phase": req.Phase, "persona": req.Role,
			"active_tokens": res.TotalToken, "max_context": persona.ContextTarget,
			"pressure": assessment.Pressure, "action": string(assessment.Action),
			"recommendation": assessment.Recommendation,
		}}
	}

	return Prepared{
		Persona:               persona,
		Messages:              res.Messages,
		TotalToken:            res.TotalToken,
		Sections:              res.Sections,
		TierWeights:           res.TierWeights,
		Eviction:              res.Eviction,
		CorrectionIDs:         tiers.CorrectionIDs,
		CorrectionsSuppressed: res.Eviction.Suppresses(prompt.TierCorrections),
		NoteIDs:               noteIDs,
		StrategyNotes:         len(strategyNotes),
	}, nil
}

// toolManifest resolves the §25 envelope the server would enforce for this
// task, so the prompt can never promise a tool the route would refuse
// (ADR-0023 §8).
func (a *Assembler) toolManifest(t project.Task) []string {
	names := a.cfg.JobPod.DefaultTools
	if len(t.AllowedTools) > 0 {
		names = t.AllowedTools
	}
	env, err := toolenvelope.Parse(names)
	if err != nil {
		slog.Warn("contextasm: unparseable tool envelope; rendering no manifest",
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

// renderCorrection renders one §18.2 record as a single §11.2 §8 line.
func renderCorrection(r corrections.Record) string {
	return fmt.Sprintf("[%s/%s %s] %s", r.Category, r.Severity, r.Scope, r.DerivedRule)
}

// gatherTiers gathers the tier payloads for one call. Provider failures degrade
// to empty tiers with a warning — a missing chunk must not fail a job that can
// otherwise proceed; they are never silently presented as "no working set".
func (a *Assembler) gatherTiers(ctx context.Context, j job.Job, p project.Project, t project.Task,
	phase string, candidates []prompt.CandidateArtifact) promptTiers {

	capability := project.CapabilityFor(p, t)
	pt := promptTiers{Tools: a.toolManifest(t), Candidates: candidates}
	if cs := a.rt.Corrections(); cs != nil {
		recs, err := cs.Relevant(ctx, t.ProjectID, maxInjectedCorrections)
		if err != nil {
			slog.Warn("contextasm: reading corrections", "task", t.ID, "err", err)
		} else {
			for _, rec := range recs {
				pt.Corrections = append(pt.Corrections, renderCorrection(rec))
				pt.CorrectionIDs = append(pt.CorrectionIDs, rec.ID)
			}
		}
	}
	q := a.contextQuery(j, t)
	if ms := a.rt.Memory(); ms != nil && a.cfg.ContextEngine.MemoryInPromptEnabled() {
		limit := a.cfg.ContextEngine.MemorySearchTopK
		if limit <= 0 {
			limit = 8
		}
		hits, merr := ms.RelevantMemory(ctx, j.ProjectID, q.Query, limit)
		if merr != nil {
			slog.Warn("contextasm: reading project memory", "job", j.ID, "err", merr)
		} else {
			ids := make([]string, 0, len(hits))
			bytes := 0
			for _, h := range hits {
				if h.Kind != mce.SourceMemo || strings.TrimSpace(h.Content) == "" {
					continue
				}
				pt.Episodic = append(pt.Episodic, "[memory] "+strings.TrimSpace(h.Content))
				ids = append(ids, h.ID)
				bytes += len(h.Content)
			}
			if len(ids) > 0 {
				a.rt.AuditCat(ctx, j.ID, "context", map[string]any{
					"event": "memory_injected", "project": j.ProjectID,
					"hits": len(ids), "bytes": bytes, "memory_ids": ids,
				})
			}
		}
	}
	chunks := a.rt.Chunks()
	if chunks == nil {
		return pt
	}
	// P1: for a code job with the pack enabled, drive the §10.1 swap from the
	// same ranking the pack uses. The active pointer is persisted, so the MCE's
	// byte-exact swap path is part of the normal loop, not just the route.
	if seeder, ok := chunks.(ActiveSeeder); ok && a.cfg.ContextEngine.WorkingSetEnabled(capability) {
		chunk, seeded, serr := seeder.SeedActive(ctx, q, a.cfg.ContextEngine.SeedActiveMaxBytes)
		switch {
		case serr != nil:
			slog.Warn("contextasm: seeding active chunk", "job", j.ID, "err", serr)
		case seeded:
			pt.ActiveChunk = &chunk
			a.rt.AuditCat(ctx, j.ID, "context", map[string]any{
				"event": "active_chunk_swapped", "chunk_id": chunk.ID, "path": chunk.RelPath,
			})
		}
	}
	if pt.ActiveChunk == nil {
		chunk, ok, err := chunks.ActiveChunk(ctx, q)
		switch {
		case err != nil:
			slog.Warn("contextasm: reading active chunk", "job", j.ID, "err", err)
		case ok:
			pt.ActiveChunk = &chunk
		}
	}
	idx, err := chunks.DormantIndex(ctx, q)
	if err != nil {
		slog.Warn("contextasm: reading dormant index", "job", j.ID, "err", err)
	} else {
		pt.DormantIndex = idx
		if len(idx) > 0 {
			a.rt.AuditCat(ctx, j.ID, "context", map[string]any{
				"event": "dormant_index_sourced", "project": j.ProjectID,
				"rows": len(idx), "limit": q.Limit,
			})
		}
	}
	if ws, ok := chunks.(WorkingSetProvider); ok &&
		a.cfg.ContextEngine.WorkingSetEnabled(capability) &&
		a.cfg.ContextEngine.WorkingSetPhase(phase) {
		work, werr := ws.WorkingSet(ctx, q,
			a.cfg.ContextEngine.WorkingSetMaxChunksValue(),
			a.cfg.ContextEngine.WorkingSetMaxBytesValue())
		if werr != nil {
			slog.Warn("contextasm: reading repository working set", "job", j.ID, "err", werr)
		} else if len(work) > 0 {
			pt.WorkingSet = work
			ids := make([]string, 0, len(work))
			bytes := 0
			for _, c := range work {
				ids = append(ids, c.ID)
				bytes += len(c.Text)
			}
			a.rt.AuditCat(ctx, j.ID, "context", map[string]any{
				"event": "working_set_sourced", "project": j.ProjectID,
				"phase": phase, "chunks": len(work), "bytes": bytes, "chunk_ids": ids,
			})
		}
	}
	return pt
}

// contextQuery builds the MCE working-set query for one call.
func (a *Assembler) contextQuery(j job.Job, t project.Task) Query {
	q := Query{JobID: j.ID, ProjectID: j.ProjectID}
	q.Limit = a.cfg.ContextEngine.RepositoryIndexLimit
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

// pendingInterruptions returns a job's queued notes as (texts, ids). A store
// error degrades to no notes with a warning rather than failing the call.
func (a *Assembler) pendingInterruptions(ctx context.Context, jobID string) ([]string, []string) {
	is := a.rt.Interruptions()
	if is == nil {
		return nil, nil
	}
	notes, err := is.Pending(ctx, jobID)
	if err != nil {
		slog.Warn("contextasm: reading interruption notes", "job", jobID, "err", err)
		return nil, nil
	}
	texts := make([]string, 0, len(notes))
	ids := make([]string, 0, len(notes))
	for _, n := range notes {
		texts = append(texts, n.Text)
		ids = append(ids, n.ID)
	}
	return texts, ids
}

// notebookHistoryLimit is how many recent entries reach one prompt.
const notebookHistoryLimit = 20

// notebookPromptBytes bounds the total notebook text injected per call.
const notebookPromptBytes = 4096

// notebookLines renders the recent notebook for the prompt in chronological
// order, one bounded line per entry.
func (a *Assembler) notebookLines(ctx context.Context, jobID string) []string {
	nb := a.rt.Notebook()
	if nb == nil {
		return nil
	}
	entries, err := nb.Recent(ctx, jobID, notebookHistoryLimit)
	if err != nil {
		slog.Warn("contextasm: reading notebook", "job", jobID, "err", err)
		return nil
	}
	out := make([]string, 0, len(entries))
	used := 0
	for _, e := range entries {
		line := "[" + string(e.Kind) + "/" + e.Phase + "] " + strings.Join(strings.Fields(e.Content), " ")
		if used+len(line) > notebookPromptBytes {
			break
		}
		used += len(line)
		out = append(out, line)
	}
	return out
}

// loadSuppressed reads and normalizes the persisted suppression set.
func (a *Assembler) loadSuppressed(ctx context.Context, jobID string) []prompt.Tier {
	return normalizeSuppressed(a.state.EvictedTiers(ctx, jobID))
}

// saveSuppressed persists a normalized suppression set (an empty set deletes
// the row).
func (a *Assembler) saveSuppressed(ctx context.Context, jobID string, tiers []prompt.Tier) {
	if err := a.state.SetEvictedTiers(ctx, jobID, normalizeSuppressed(tiers)); err != nil {
		slog.Error("contextasm: writing eviction state", "job", jobID, "err", err)
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
		return nil
	}
	return out
}

// tierNames renders tiers for audit rows, ascending, nil-safe.
func tierNames(tiers []prompt.Tier) []string {
	out := make([]string, 0, len(tiers))
	for _, t := range tiers {
		out = append(out, t.String())
	}
	return out
}

// tierWeights renders the per-tier price table for audit rows, keyed by tier
// name so a post-mortem reads without decoding integers.
func tierWeights(weights map[prompt.Tier]int) map[string]int {
	out := make(map[string]int, len(weights))
	for t, w := range weights {
		out[t.String()] = w
	}
	return out
}
