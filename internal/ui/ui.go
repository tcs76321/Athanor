// Package ui serves Athanor's local web UI (ROADMAP M6-T8; ARCHITECTURE §27)
// on the daemon's loopback listener. It is dependency-free: html/template,
// SSE, and net/http only.
//
// Pages: Dashboard (jobs + pending approvals + power/freeze), Projects
// (artifact history + diffs + corrections), Corrections, and Job Watch (live
// phase events + streamed tokens + interruption notes + controls). Live
// updates use Server-Sent Events: /ui/events for the dashboard and
// /ui/jobs/{id}/stream for the watch view.
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/corrections"
	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/interruptions"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/strategy"
)

// Freezer reports the §22 kill-switch state.
type Freezer interface{ Frozen() bool }

// Enqueuer starts a job (the engine). Used by retry/resume.
type Enqueuer interface{ Enqueue(jobID string) }

// Deps wires the UI to the daemon's stores and services.
type Deps struct {
	Store         *store.Store
	Projects      *project.Repo
	Jobs          *job.Repository
	Artifacts     *artifact.Store
	Corrections   *corrections.Repo
	HITL          *hitl.Service
	Interruptions *interruptions.Repo
	Strategy      *strategy.Repo
	Freezer       Freezer
	Engine        Enqueuer
	// DefaultTTL is applied to defer decisions made without an explicit
	// duration.
	DefaultTTL time.Duration
}

// UI serves the web interface.
type UI struct {
	deps Deps
	hub  *Hub
}

// New builds a UI.
func New(deps Deps) *UI {
	return &UI{deps: deps, hub: NewHub()}
}

// Hub implements the engine's token sink and fans chunks out to watch-view
// subscribers. Slow subscribers drop chunks rather than block the engine.
func (u *UI) Hub() *Hub { return u.hub }

// Register mounts the UI routes.
func (u *UI) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui", u.handleDashboard)
	mux.HandleFunc("GET /ui/events", u.handleDashboardStream)
	mux.HandleFunc("GET /ui/projects", u.handleProjects)
	mux.HandleFunc("GET /ui/projects/{id}", u.handleProject)
	mux.HandleFunc("POST /ui/projects/{id}/corrections", u.handleCorrectionCreate)
	mux.HandleFunc("GET /ui/corrections", u.handleCorrections)
	mux.HandleFunc("GET /ui/statistics", u.handleStatistics)
	mux.HandleFunc("POST /ui/corrections/{id}", u.handleCorrectionUpdate)
	mux.HandleFunc("GET /ui/jobs/{id}", u.handleWatch)
	mux.HandleFunc("GET /ui/jobs/{id}/stream", u.handleJobStream)
	mux.HandleFunc("POST /ui/jobs/{id}/note", u.handleNote)
	mux.HandleFunc("POST /ui/jobs/{id}/control", u.handleControl)
	mux.HandleFunc("GET /ui/artifacts/{id}/diff", u.handleDiff)
	mux.HandleFunc("POST /ui/approvals/{id}", u.handleApproval)
}

// --- Hub ---

// Hub is a per-job token fan-out.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan string]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{subs: map[string]map[chan string]struct{}{}} }

// Publish sends a token chunk to every subscriber of jobID, dropping it for
// a subscriber whose buffer is full.
func (h *Hub) Publish(jobID, text string) {
	if text == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[jobID] {
		select {
		case ch <- text:
		default:
		}
	}
}

// Subscribe returns a channel of token chunks and an unsubscribe function.
func (h *Hub) Subscribe(jobID string) (<-chan string, func()) {
	ch := make(chan string, 256)
	h.mu.Lock()
	if h.subs[jobID] == nil {
		h.subs[jobID] = map[chan string]struct{}{}
	}
	h.subs[jobID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[jobID], ch)
		h.mu.Unlock()
	}
}

// --- Handlers ---

func (u *UI) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	active, err := u.deps.Jobs.Active(ctx)
	if err != nil {
		u.fail(w, err)
		return
	}
	terminal, err := u.deps.Jobs.Terminal(ctx, 10)
	if err != nil {
		u.fail(w, err)
		return
	}
	var pending []hitl.Request
	if u.deps.HITL != nil {
		pending, err = u.deps.HITL.Pending(ctx)
		if err != nil {
			u.fail(w, err)
			return
		}
	}
	frozen := u.deps.Freezer != nil && u.deps.Freezer.Frozen()
	u.render(w, "Dashboard", dashboardBody, map[string]any{
		"Active": active, "Terminal": terminal, "Pending": pending, "Frozen": frozen,
	})
}

func (u *UI) handleProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := u.listProjects(r.Context())
	if err != nil {
		u.fail(w, err)
		return
	}
	u.render(w, "Projects", projectsBody, map[string]any{"Projects": projects})
}

func (u *UI) handleProject(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := u.deps.Projects.Get(ctx, r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	arts, err := u.deps.Artifacts.ListByProject(ctx, p.ID)
	if err != nil {
		u.fail(w, err)
		return
	}
	corr, err := u.deps.Corrections.ListByProject(ctx, p.ID)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.render(w, "Project "+p.Name, projectBody, map[string]any{
		"Project": p, "Artifacts": arts, "Corrections": corr,
	})
}

func (u *UI) handleCorrections(w http.ResponseWriter, r *http.Request) {
	active, err := u.deps.Corrections.Active(r.Context())
	if err != nil {
		u.fail(w, err)
		return
	}
	u.render(w, "Corrections", correctionsBody, map[string]any{"Corrections": active})
}

// handleStatistics is the read-only §13.4 panel: captured outcomes plus the
// strategy insights (proposed/active/muted).
func (u *UI) handleStatistics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var insights []strategy.Insight
	var outcomes []strategy.Outcome
	if u.deps.Strategy != nil {
		insights, _ = u.deps.Strategy.ListInsights(ctx, "")
		outcomes, _ = u.deps.Strategy.ListOutcomes(ctx, 500)
	}
	accepted, tokens := 0, 0
	for _, o := range outcomes {
		if o.Result == strategy.ResultAcceptedNew || o.Result == strategy.ResultAcceptedPrevious {
			accepted++
		}
		tokens += o.TokenCost
	}
	u.render(w, "Statistics", statisticsBody, map[string]any{
		"Insights": insights, "Jobs": len(outcomes), "Accepted": accepted, "Tokens": tokens,
	})
}

func (u *UI) handleWatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	j, err := u.deps.Jobs.Get(ctx, id)
	if err != nil {
		u.fail(w, err)
		return
	}
	arts, err := u.deps.Artifacts.ListByJob(ctx, id)
	if err != nil {
		u.fail(w, err)
		return
	}
	var notes []interruptions.Note
	if u.deps.Interruptions != nil {
		notes, _ = u.deps.Interruptions.List(ctx, id)
	}
	events, err := u.deps.Store.QueryEvents(ctx, store.EventFilter{JobID: id})
	if err != nil {
		u.fail(w, err)
		return
	}
	u.render(w, "Watch "+id, watchBody, map[string]any{
		"Job": j, "Artifacts": arts, "Notes": notes, "Events": events,
	})
}

// handleJobStream is the watch view's SSE stream: token chunks plus new job
// events.
func (u *UI) handleJobStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	jobID := r.PathValue("id")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	tokens, cancel := u.hub.Subscribe(jobID)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastID int64
	// Seed lastID so the initial page's events are not re-sent.
	if events, err := u.deps.Store.QueryEvents(r.Context(), store.EventFilter{JobID: jobID}); err == nil && len(events) > 0 {
		lastID = events[len(events)-1].ID
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case tok := <-tokens:
			writeSSE(w, "token", mustJSON(tok))
			flusher.Flush()
		case <-ticker.C:
			for _, ev := range u.newEvents(r.Context(), jobID, &lastID) {
				writeSSE(w, "event", mustJSON(map[string]any{
					"id": ev.ID, "ts": ev.TS.Format(time.RFC3339), "category": ev.Category, "data": ev.DataJSON,
				}))
			}
			flusher.Flush()
		}
	}
}

// handleDashboardStream streams every new event; the dashboard script updates
// a job's phase from `transition` rows.
func (u *UI) handleDashboardStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	var lastID int64
	if events, err := u.deps.Store.QueryEvents(r.Context(), store.EventFilter{}); err == nil && len(events) > 0 {
		lastID = events[len(events)-1].ID
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			for _, ev := range u.newEvents(r.Context(), "", &lastID) {
				writeSSE(w, "event", mustJSON(map[string]any{
					"id": ev.ID, "job_id": ev.JobID, "category": ev.Category, "data": ev.DataJSON,
				}))
			}
			flusher.Flush()
		}
	}
}

// newEvents returns events with id > *lastID (optionally for one job) and
// advances *lastID. A query error yields nothing (the next tick retries).
func (u *UI) newEvents(ctx context.Context, jobID string, lastID *int64) []store.EventRecord {
	events, err := u.deps.Store.QueryEvents(ctx, store.EventFilter{JobID: jobID})
	if err != nil {
		return nil
	}
	var out []store.EventRecord
	for _, ev := range events {
		if ev.ID > *lastID {
			out = append(out, ev)
			*lastID = ev.ID
		}
	}
	return out
}

func (u *UI) handleApproval(w http.ResponseWriter, r *http.Request) {
	if u.deps.HITL == nil {
		u.fail(w, fmt.Errorf("HITL queue is not configured"))
		return
	}
	_ = r.ParseForm()
	action := r.FormValue("action")
	note := r.FormValue("note")
	deferFor := u.deps.DefaultTTL
	if v := r.FormValue("defer_for"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			deferFor = d
		}
	}
	if _, err := u.deps.HITL.Decide(r.Context(), r.PathValue("id"), action, note, deferFor); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui", http.StatusSeeOther)
}

func (u *UI) handleCorrectionCreate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	src := corrections.Source(r.FormValue("source"))
	if src == "" {
		src = corrections.SourceUserRejection
	}
	_, err := u.deps.Corrections.Capture(r.Context(), corrections.CaptureInput{
		Source: src, ProjectID: r.PathValue("id"), ArtifactID: r.FormValue("artifact_id"),
		Category: r.FormValue("category"), Severity: r.FormValue("severity"),
		UserFeedback: r.FormValue("reason"), DerivedRule: r.FormValue("desired_behavior"),
		Scope: r.FormValue("scope"),
	})
	if err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/projects/"+r.PathValue("id"), http.StatusSeeOther)
}

func (u *UI) handleCorrectionUpdate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id := r.PathValue("id")
	if status := r.FormValue("status"); status != "" {
		if err := u.deps.Corrections.SetStatus(r.Context(), id, status); err != nil {
			u.fail(w, err)
			return
		}
	}
	edit := corrections.EditInput{
		Category: r.FormValue("category"), Severity: r.FormValue("severity"), Scope: r.FormValue("scope"),
		UserFeedback: r.FormValue("user_feedback"), DerivedRule: r.FormValue("derived_rule"),
	}
	if edit != (corrections.EditInput{}) {
		if _, err := u.deps.Corrections.Update(r.Context(), id, edit); err != nil {
			u.fail(w, err)
			return
		}
	}
	http.Redirect(w, r, "/ui/corrections", http.StatusSeeOther)
}

func (u *UI) handleNote(w http.ResponseWriter, r *http.Request) {
	if u.deps.Interruptions == nil {
		u.fail(w, fmt.Errorf("interruption queue is not configured"))
		return
	}
	_ = r.ParseForm()
	note := strings.TrimSpace(r.FormValue("note"))
	if note == "" {
		u.fail(w, fmt.Errorf("note must not be empty"))
		return
	}
	if _, err := u.deps.Interruptions.Add(r.Context(), r.PathValue("id"), note); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/jobs/"+r.PathValue("id"), http.StatusSeeOther)
}

// handleControl pauses, resumes, cancels, or retries a job.
func (u *UI) handleControl(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	j, err := u.deps.Jobs.Get(ctx, id)
	if err != nil {
		u.fail(w, err)
		return
	}
	_ = r.ParseForm()
	switch r.FormValue("action") {
	case "pause":
		if !job.CanTransition(j.State, job.StatePaused) {
			u.fail(w, fmt.Errorf("job %s cannot pause from %s", id, j.State))
			return
		}
		if _, err := u.deps.Jobs.Transition(ctx, id, job.StatePaused); err != nil {
			u.fail(w, err)
			return
		}
	case "resume":
		if j.State != job.StatePaused {
			u.fail(w, fmt.Errorf("job %s is not paused", id))
			return
		}
		if _, err := u.deps.Jobs.Transition(ctx, id, j.PausedFrom); err != nil {
			u.fail(w, err)
			return
		}
		if u.deps.Engine != nil {
			u.deps.Engine.Enqueue(id)
		}
	case "cancel":
		if j.State.Terminal() {
			return
		}
		if _, err := u.deps.Jobs.Transition(ctx, id, job.StateCancelled); err != nil {
			u.fail(w, err)
			return
		}
	case "retry":
		if !j.State.Terminal() {
			u.fail(w, fmt.Errorf("job %s is not terminal", id))
			return
		}
		next, err := u.deps.Jobs.Create(ctx, j.TaskID, j.ProjectID)
		if err != nil {
			u.fail(w, err)
			return
		}
		if u.deps.Engine != nil {
			u.deps.Engine.Enqueue(next.ID)
		}
		http.Redirect(w, r, "/ui/jobs/"+next.ID, http.StatusSeeOther)
		return
	default:
		u.fail(w, fmt.Errorf("unknown control action"))
		return
	}
	http.Redirect(w, r, "/ui/jobs/"+id, http.StatusSeeOther)
}

// handleDiff renders a line diff between an artifact and the version it
// superseded.
func (u *UI) handleDiff(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, err := u.deps.Artifacts.Get(ctx, r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	newContent, err := u.deps.Artifacts.ReadContent(ctx, a.ID)
	if err != nil {
		u.fail(w, err)
		return
	}
	var oldContent []byte
	if a.SupersedesID != "" {
		old, err := u.deps.Artifacts.Get(ctx, a.SupersedesID)
		if err != nil {
			u.fail(w, err)
			return
		}
		oldContent, err = u.deps.Artifacts.ReadContent(ctx, old.ID)
		if err != nil {
			u.fail(w, err)
			return
		}
	}
	u.render(w, "Diff "+a.ID, diffBody, map[string]any{
		"Artifact": a, "Lines": lineDiff(string(oldContent), string(newContent)),
	})
}

// listProjects reads every project. project.Repo has no List; the UI queries
// the projects table directly (read-only), which is ordinary for a view.
//
// The store holds a single connection (ADR-0027), so the id rows must be
// drained and closed before the per-id Get queries, or the Get waits forever
// for the connection the open rows hold.
func (u *UI) listProjects(ctx context.Context) ([]project.Project, error) {
	rows, err := u.deps.Store.DB().QueryContext(ctx,
		`SELECT id FROM projects ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]project.Project, 0, len(ids))
	for _, id := range ids {
		p, err := u.deps.Projects.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// --- rendering ---

func (u *UI) render(w http.ResponseWriter, title, body string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["Title"] = title
	t := template.Must(template.New("page").Funcs(funcs).Parse(document(title, body)))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (u *UI) fail(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusBadRequest)
}

var funcs = template.FuncMap{
	"short": func(s string) string {
		if len(s) > 12 {
			return s[:12]
		}
		return s
	},
	"ts": func(v any) string {
		switch t := v.(type) {
		case time.Time:
			return t.Format("15:04:05")
		case *time.Time:
			if t != nil {
				return t.Format("15:04:05")
			}
		}
		return ""
	},
	"join": func(ss []string) string { return strings.Join(ss, ", ") },
}

func writeSSE(w http.ResponseWriter, event, data string) {
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `""`
	}
	return string(b)
}

// lineDiff is a classic LCS line diff. Equal lines are context; removed and
// added lines are tagged.
type diffLine struct {
	Kind string // " ", "-", "+"
	Text string
}

func lineDiff(oldText, newText string) []diffLine {
	a := splitLines(oldText)
	b := splitLines(newText)
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var out []diffLine
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, diffLine{" ", a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, diffLine{"-", a[i]})
			i++
		default:
			out = append(out, diffLine{"+", b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, diffLine{"-", a[i]})
	}
	for ; j < m; j++ {
		out = append(out, diffLine{"+", b[j]})
	}
	return out
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}
