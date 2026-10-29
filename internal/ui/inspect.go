package ui

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/tcs76321/athanor/internal/budget"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/llm"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/strategy"
)

// --- events inspector ---

type eventView struct {
	ID        int64
	TS        time.Time
	Category  string
	Level     string
	ProjectID string
	JobID     string
	DataJSON  string
	Name      string
	Summary   string
}

func views(events []store.EventRecord) []eventView {
	out := make([]eventView, 0, len(events))
	for _, e := range events {
		out = append(out, eventView{
			ID: e.ID, TS: e.TS, Category: e.Category, Level: e.Level,
			ProjectID: e.ProjectID, JobID: e.JobID, DataJSON: e.DataJSON,
			Name: eventName(e.DataJSON), Summary: prettyEvent(e.DataJSON),
		})
	}
	return out
}

func eventName(raw string) string {
	var d struct {
		Event string `json:"event"`
	}
	if json.Unmarshal([]byte(raw), &d) == nil && d.Event != "" {
		return d.Event
	}
	return "event"
}

func (u *UI) handleEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.EventFilter{
		Category: q.Get("category"), JobID: q.Get("job"), ProjectID: q.Get("project"),
		Level: q.Get("level"), Query: q.Get("q"), Desc: true, Limit: 100,
	}
	if before, _ := strconv.ParseInt(q.Get("before"), 10, 64); before > 0 {
		f.BeforeID = before
	}
	events, err := u.deps.Store.QueryEvents(r.Context(), f)
	if err != nil {
		u.fail(w, err)
		return
	}
	nextURL := ""
	if len(events) == f.Limit {
		vals := r.URL.Query()
		vals.Set("before", strconv.FormatInt(events[len(events)-1].ID, 10))
		nextURL = "/ui/events?" + vals.Encode()
	}
	u.render(w, "Events", eventsBody, map[string]any{
		"Events": views(events), "NextURL": nextURL, "Filter": q,
		"Categories": u.eventCategories(r.Context()), "Tab": "events",
	})
}

func (u *UI) eventCategories(ctx context.Context) []string {
	rows, err := u.deps.Store.DB().QueryContext(ctx,
		`SELECT DISTINCT category FROM events ORDER BY category`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var c string
		if rows.Scan(&c) == nil {
			out = append(out, c)
		}
	}
	return out
}

// --- artifact index ---

func (u *UI) handleArtifactIndex(w http.ResponseWriter, r *http.Request) {
	arts, err := u.deps.Artifacts.ListRecent(r.Context(), 200)
	if err != nil {
		u.fail(w, err)
		return
	}
	names := map[string]string{}
	for _, a := range arts {
		u.addProjectName(r.Context(), names, a.ProjectID)
	}
	u.render(w, "Artifacts", artifactIndexBody, map[string]any{
		"Artifacts": arts, "ProjectNames": names, "Tab": "artifacts",
	})
}

// --- job trace ---

type traceModelCall struct {
	Phase, Persona, Model, Think  string
	Prompt, Completion, Reasoning int
	Temperature                   float64
	MaxTokens, BudgetSec          int
	PromptMessages                []llm.Message
	Response, Thinking            string
}

type traceUnit struct {
	ID, Goal, Mode, Reason, Falsifier string
}

type traceAction struct {
	Seq                                                int
	Tool, Status, RequestJSON, ResponseJSON, CreatedAt string
}

func (u *UI) handleTrace(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	j, err := u.deps.Jobs.Get(ctx, id)
	if err != nil {
		u.fail(w, err)
		return
	}
	p, _ := u.deps.Projects.Get(ctx, j.ProjectID)
	arts, _ := u.deps.Artifacts.ListByJob(ctx, id)
	var evals []evaluation.Record
	if u.deps.Eval != nil {
		evals, _ = u.deps.Eval.ListByJob(ctx, id)
	}
	var profile strategy.Profile
	var outcome strategy.Outcome
	hasOutcome := false
	if u.deps.Strategy != nil {
		profile, _ = u.deps.Strategy.GetProfileByJob(ctx, id)
		if o, err := u.deps.Strategy.GetOutcomeByJob(ctx, id); err == nil {
			outcome, hasOutcome = o, true
		}
	}
	var notes []string
	if u.deps.Interruptions != nil {
		if ns, err := u.deps.Interruptions.List(ctx, id); err == nil {
			for _, n := range ns {
				notes = append(notes, n.Status+": "+n.Text)
			}
		}
	}
	events, err := u.deps.Store.QueryEvents(ctx, store.EventFilter{JobID: id, Limit: 5000})
	if err != nil {
		u.fail(w, err)
		return
	}
	views := views(events)
	var calls []traceModelCall
	var contextEvents []eventView
	var units []traceUnit
	totalTokens := 0
	for _, e := range views {
		switch e.Name {
		case "llm_call":
			c := parseModelCall(e)
			totalTokens += c.Prompt + c.Completion
			calls = append(calls, c)
		case "task_analysis":
			units = parseUnits(e.DataJSON)
		}
		switch e.Name {
		case "kv_cache_assembled", "kv_cache_pressure", "kv_cache_evicted",
			"context_floor_violation", "memory_injected", "memory_compacted",
			"chunks_stored", "active_chunk_swapped", "chunk_swapped",
			"dormant_index_sourced", "repo_preindex", "working_set_injected":
			contextEvents = append(contextEvents, e)
		}
	}
	traceEvents := views
	if len(traceEvents) > 300 {
		traceEvents = traceEvents[len(traceEvents)-300:]
	}
	actions := queryRows(ctx, u.deps.Store.DB(), `
		SELECT seq, tool, status, request_json, COALESCE(response_json,''), created_at
		  FROM actions WHERE job_id = ? ORDER BY seq`, func(rows *sql.Rows) (traceAction, error) {
		var a traceAction
		return a, rows.Scan(&a.Seq, &a.Tool, &a.Status, &a.RequestJSON, &a.ResponseJSON, &a.CreatedAt)
	}, id)
	u.render(w, "Trace "+id, traceBody, map[string]any{
		"Job": j, "Project": p, "Artifacts": arts, "Evals": evals,
		"Profile": profile, "Outcome": outcome, "HasOutcome": hasOutcome,
		"Calls": calls, "Events": traceEvents, "ContextEvents": contextEvents,
		"Notes": notes, "Units": units, "Tab": "events",
		"EventCount": len(views), "TotalTokens": totalTokens,
		"Capture": u.deps.Config != nil && u.deps.Config.Logging.CaptureLLMPayloads,
		"Actions": actions,
	})
}

func parseModelCall(e eventView) traceModelCall {
	var d struct {
		Phase, Persona, Model         string
		Prompt, Completion, Reasoning int
		Temperature                   float64
		MaxTokens, BudgetSec          int
		Think                         any
		PromptMessages                []llm.Message `json:"prompt_messages"`
		Response, Thinking            string
	}
	_ = json.Unmarshal([]byte(e.DataJSON), &d)
	think := "off"
	switch v := d.Think.(type) {
	case string:
		think = v
	case bool:
		if v {
			think = "on"
		}
	}
	return traceModelCall{
		Phase: d.Phase, Persona: d.Persona, Model: d.Model, Think: think,
		Prompt: d.Prompt, Completion: d.Completion, Reasoning: d.Reasoning,
		Temperature: d.Temperature, MaxTokens: d.MaxTokens, BudgetSec: d.BudgetSec,
		PromptMessages: d.PromptMessages, Response: d.Response, Thinking: d.Thinking,
	}
}

func parseUnits(raw string) []traceUnit {
	var d struct {
		Units []traceUnit `json:"units_detail"`
	}
	_ = json.Unmarshal([]byte(raw), &d)
	return d.Units
}

// --- memory inspector ---

type compactedRow struct {
	ID, Kind, ProfileType, Persona, ProjectID, JobID, CreatedAt string
	SourceBytes, CompactedBytes                                 int
}

type chunkRow struct {
	ID, SourceRelpath, Kind, ProjectID, CreatedAt string
	LineStart, LineEnd, Bytes                     int
}

type dormantRow struct {
	ChunkID, Summary, Status, CreatedAt string
}

type embeddingRow struct {
	OwnerID, SourceKind, Model, ProjectID, JobID, CreatedAt string
	Dim                                                     int
}

type sourceRow struct {
	ProjectID, Relpath, Lang, Status, IndexedAt string
	Size, Chunks                                int
}

type activeRow struct {
	ScopeID, ChunkID, UpdatedAt string
}

func (u *UI) handleMemory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	compacted := queryRows(ctx, u.deps.Store.DB(), `
		SELECT id, kind, profile_type, persona, COALESCE(project_id,''), COALESCE(job_id,''),
		       source_bytes, compacted_bytes, created_at
		  FROM compacted_memory ORDER BY created_at DESC LIMIT 50`, func(rows *sql.Rows) (compactedRow, error) {
		var c compactedRow
		return c, rows.Scan(&c.ID, &c.Kind, &c.ProfileType, &c.Persona, &c.ProjectID,
			&c.JobID, &c.SourceBytes, &c.CompactedBytes, &c.CreatedAt)
	})
	chunks := queryRows(ctx, u.deps.Store.DB(), `
		SELECT id, source_relpath, kind, COALESCE(project_id,''), line_start, line_end,
		       length(content), created_at
		  FROM context_chunks ORDER BY created_at DESC LIMIT 50`, func(rows *sql.Rows) (chunkRow, error) {
		var c chunkRow
		return c, rows.Scan(&c.ID, &c.SourceRelpath, &c.Kind, &c.ProjectID,
			&c.LineStart, &c.LineEnd, &c.Bytes, &c.CreatedAt)
	})
	dormant := queryRows(ctx, u.deps.Store.DB(), `
		SELECT chunk_id, summary, summary_status, created_at
		  FROM dormant_index ORDER BY created_at DESC LIMIT 50`, func(rows *sql.Rows) (dormantRow, error) {
		var d dormantRow
		return d, rows.Scan(&d.ChunkID, &d.Summary, &d.Status, &d.CreatedAt)
	})
	embeddings := queryRows(ctx, u.deps.Store.DB(), `
		SELECT owner_id, source_kind, model, dim, COALESCE(project_id,''), COALESCE(job_id,''), created_at
		  FROM memory_embeddings ORDER BY created_at DESC LIMIT 25`, func(rows *sql.Rows) (embeddingRow, error) {
		var e embeddingRow
		return e, rows.Scan(&e.OwnerID, &e.SourceKind, &e.Model, &e.Dim, &e.ProjectID, &e.JobID, &e.CreatedAt)
	})
	sources := queryRows(ctx, u.deps.Store.DB(), `
		SELECT project_id, relpath, lang, size, chunks, status, indexed_at
		  FROM indexed_sources ORDER BY indexed_at DESC LIMIT 25`, func(rows *sql.Rows) (sourceRow, error) {
		var s sourceRow
		return s, rows.Scan(&s.ProjectID, &s.Relpath, &s.Lang, &s.Size, &s.Chunks, &s.Status, &s.IndexedAt)
	})
	active := queryRows(ctx, u.deps.Store.DB(), `
		SELECT scope_id, chunk_id, updated_at
		  FROM context_active ORDER BY updated_at DESC LIMIT 25`, func(rows *sql.Rows) (activeRow, error) {
		var a activeRow
		return a, rows.Scan(&a.ScopeID, &a.ChunkID, &a.UpdatedAt)
	})
	var counts struct{ Compacted, Chunks, Dormant, Embeddings int }
	_ = u.deps.Store.DB().QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM compacted_memory),
		       (SELECT COUNT(*) FROM context_chunks),
		       (SELECT COUNT(*) FROM dormant_index),
		       (SELECT COUNT(*) FROM memory_embeddings)`).
		Scan(&counts.Compacted, &counts.Chunks, &counts.Dormant, &counts.Embeddings)
	u.render(w, "Memory", memoryBody, map[string]any{
		"Compacted": compacted, "Chunks": chunks, "Dormant": dormant, "Counts": counts,
		"Embeddings": embeddings, "Sources": sources, "Active": active, "Tab": "memory",
	})
}

// queryRows is a small typed scan helper; it always drains and closes rows
// before the next single-connection query.
func queryRows[T any](ctx context.Context, db *sql.DB, query string, scan func(*sql.Rows) (T, error), args ...any) []T {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return out
		}
		out = append(out, v)
	}
	return out
}

func (u *UI) handleCompacted(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var c compactedRow
	var content string
	err := u.deps.Store.DB().QueryRowContext(ctx, `
		SELECT id, kind, profile_type, persona, COALESCE(project_id,''), COALESCE(job_id,''),
		       source_bytes, compacted_bytes, created_at, content
		  FROM compacted_memory WHERE id = ?`, r.PathValue("id")).
		Scan(&c.ID, &c.Kind, &c.ProfileType, &c.Persona, &c.ProjectID, &c.JobID,
			&c.SourceBytes, &c.CompactedBytes, &c.CreatedAt, &content)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.render(w, "Compacted memory", memoryDetailBody, map[string]any{
		"Title": c.ID, "Kind": "compacted", "Content": content,
		"Meta": map[string]any{
			"kind": c.Kind, "profile_type": c.ProfileType, "persona": c.Persona,
			"project_id": c.ProjectID, "job_id": c.JobID,
			"source_bytes": c.SourceBytes, "compacted_bytes": c.CompactedBytes,
			"created_at": c.CreatedAt,
		},
	})
}

func (u *UI) handleChunk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var c chunkRow
	var content []byte
	err := u.deps.Store.DB().QueryRowContext(ctx, `
		SELECT id, source_relpath, kind, COALESCE(project_id,''), line_start, line_end,
		       length(content), created_at, content
		  FROM context_chunks WHERE id = ?`, r.PathValue("id")).
		Scan(&c.ID, &c.SourceRelpath, &c.Kind, &c.ProjectID, &c.LineStart,
			&c.LineEnd, &c.Bytes, &c.CreatedAt, &content)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.render(w, "Context chunk", memoryDetailBody, map[string]any{
		"Title": c.SourceRelpath, "Kind": "chunk", "Content": string(content),
		"Meta": map[string]any{
			"source": c.SourceRelpath, "kind": c.Kind, "project_id": c.ProjectID,
			"line_start": c.LineStart, "line_end": c.LineEnd, "bytes": c.Bytes,
			"created_at": c.CreatedAt,
		},
	})
}

// --- daydream ---

type daydreamRow struct {
	ID, Action, Persona, StartedAt, FinishedAt   string
	Chunks, TokensSaved, Dormant                 int
	ArtifactsJSON, CorrectionsJSON, InsightsJSON string
}

func (u *UI) handleDaydream(w http.ResponseWriter, r *http.Request) {
	rows := queryRows(r.Context(), u.deps.Store.DB(), `
		SELECT id, action, persona_used, started_at, finished_at,
		       chunks_processed, tokens_saved, dormant_index_entries_added,
		       artifacts_json, corrections_json, insights_json
		  FROM daydream_logs ORDER BY started_at DESC LIMIT 50`, func(rows *sql.Rows) (daydreamRow, error) {
		var d daydreamRow
		return d, rows.Scan(&d.ID, &d.Action, &d.Persona, &d.StartedAt, &d.FinishedAt,
			&d.Chunks, &d.TokensSaved, &d.Dormant, &d.ArtifactsJSON,
			&d.CorrectionsJSON, &d.InsightsJSON)
	})
	u.render(w, "Daydream", daydreamBody, map[string]any{"Logs": rows, "Tab": "daydream"})
}

// --- runtime ---

type alarmRow struct {
	ID, Category, Level, Message, Status, CreatedAt string
}

type hitlRow struct {
	ID, Type, Severity, Status, JobID, CreatedAt, Payload string
}

type quarantineRow struct {
	Relpath, Reason, Pipeline, IngestedAt string
	Size                                  int
}

type personaRow struct {
	Role, Model, Family string
	ContextTarget       int
}

type stateRow struct {
	Key, Value string
}

func (u *UI) handleRuntime(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var personas []personaRow
	if u.deps.Config != nil {
		for _, name := range []string{"wide", "tall", "main", "security", "alternative"} {
			if pc, ok := u.deps.Config.Personas.Role(name); ok {
				personas = append(personas, personaRow{
					Role: name, Model: pc.Model, Family: pc.Family, ContextTarget: pc.ContextTarget,
				})
			}
		}
	}
	var bstatus budget.Status
	hasBudget := false
	if u.deps.Config != nil {
		s := u.deps.Config.BudgetSettings()
		if s.Enabled() {
			if win, err := budget.WindowFor(time.Now(), s); err == nil {
				if st, err := budget.New(u.deps.Store.DB()).Status(ctx, win, s); err == nil {
					bstatus, hasBudget = st, true
				}
			}
		}
	}
	alarms := queryRows(ctx, u.deps.Store.DB(), `
		SELECT id, category, level, message, status, created_at
		  FROM alarms ORDER BY created_at DESC LIMIT 25`, func(rows *sql.Rows) (alarmRow, error) {
		var a alarmRow
		return a, rows.Scan(&a.ID, &a.Category, &a.Level, &a.Message, &a.Status, &a.CreatedAt)
	})
	decisions := queryRows(ctx, u.deps.Store.DB(), `
		SELECT id, type, severity, status, COALESCE(job_id,''), created_at, payload_json
		  FROM hitl_requests ORDER BY created_at DESC LIMIT 25`, func(rows *sql.Rows) (hitlRow, error) {
		var h hitlRow
		return h, rows.Scan(&h.ID, &h.Type, &h.Severity, &h.Status, &h.JobID, &h.CreatedAt, &h.Payload)
	})
	quarantine := queryRows(ctx, u.deps.Store.DB(), `
		SELECT relpath, reason, source_size, pipeline, ingested_at
		  FROM quarantined_files ORDER BY ingested_at DESC LIMIT 25`, func(rows *sql.Rows) (quarantineRow, error) {
		var q quarantineRow
		return q, rows.Scan(&q.Relpath, &q.Reason, &q.Size, &q.Pipeline, &q.IngestedAt)
	})
	var counts struct{ Jobs, Events, Artifacts, Corrections, Outcomes, State, Quarantined int }
	_ = u.deps.Store.DB().QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM jobs), (SELECT COUNT(*) FROM events),
		       (SELECT COUNT(*) FROM artifacts), (SELECT COUNT(*) FROM corrections),
		       (SELECT COUNT(*) FROM strategy_outcomes),
		       (SELECT COUNT(*) FROM system_state), (SELECT COUNT(*) FROM quarantined_files)`).
		Scan(&counts.Jobs, &counts.Events, &counts.Artifacts, &counts.Corrections,
			&counts.Outcomes, &counts.State, &counts.Quarantined)
	state := queryRows(ctx, u.deps.Store.DB(), `
		SELECT key, value FROM system_state ORDER BY key LIMIT 100`, func(rows *sql.Rows) (stateRow, error) {
		var s stateRow
		return s, rows.Scan(&s.Key, &s.Value)
	})
	u.render(w, "Runtime", runtimeBody, map[string]any{
		"Personas": personas, "Budget": bstatus, "HasBudget": hasBudget,
		"Alarms": alarms, "Decisions": decisions, "Counts": counts,
		"State": state, "Quarantine": quarantine,
		"ConfigPath": configPath(u.deps.Config), "Tab": "runtime",
	})
}

func configPath(c *config.Config) string {
	if c == nil {
		return ""
	}
	return c.SourcePath
}
