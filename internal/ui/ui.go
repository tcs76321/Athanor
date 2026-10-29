// Package ui serves Athanor's local web UI (ROADMAP M6-T8; ARCHITECTURE §27)
// on the Core's loopback listener. It is dependency-free: html/template,
// SSE, and net/http only.
//
// Pages: Dashboard (jobs + pending approvals + power/freeze), Projects
// (artifact history + diffs + corrections), Corrections, Job Watch (live
// phase events + streamed tokens + interruption notes + controls), Reviews,
// and the read-only inspector (events, artifacts, memory, daydream, runtime,
// and one job's full trace). Live updates use Server-Sent Events:
// /ui/events/stream for the dashboard and /ui/jobs/{id}/stream for the watch
// view.
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/corrections"
	"github.com/tcs76321/athanor/internal/digest"
	"github.com/tcs76321/athanor/internal/evaluation"
	"github.com/tcs76321/athanor/internal/firstuse"
	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/interruptions"
	"github.com/tcs76321/athanor/internal/interview"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/review"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/strategy"
)

// Freezer reports the §22 kill-switch state.
type Freezer interface{ Frozen() bool }

// Enqueuer starts a job (the engine). Used by retry/resume.
type Enqueuer interface{ Enqueue(jobID string) }

// RepoPreindexer runs one bounded repository index pass. It is optional: a UI
// without one still creates projects, but a repo-backed first job starts with
// an empty Dormant Index.
type RepoPreindexer interface {
	Preindex(ctx context.Context, projectID, path string) error
}

// uiPreindexer adapts the UI's error-only pre-index seam to firstuse's
// counter-returning one. The UI does not render the counters; the service still
// audits them.
type uiPreindexer struct{ inner RepoPreindexer }

func (u uiPreindexer) IndexOnceSummary(ctx context.Context, projectID, path string) (firstuse.Summary, error) {
	return firstuse.Summary{}, u.inner.Preindex(ctx, projectID, path)
}

// Deps wires the UI to the Core's stores and services.
type Deps struct {
	Store         *store.Store
	Projects      *project.Repo
	Jobs          *job.Repository
	Artifacts     *artifact.Store
	Corrections   *corrections.Repo
	Reviews       *review.Repo
	Eval          *evaluation.Repo
	HITL          *hitl.Service
	Interviews    *interview.Repo
	Interruptions *interruptions.Repo
	Strategy      *strategy.Repo
	Freezer       Freezer
	Engine        Enqueuer
	// Config is read-only. The inspector shows personas, limits, and the
	// budget shape; it never writes configuration.
	Config *config.Config
	// Runtime and Version identify the process serving the UI: "Core Pod" in
	// the supported runtime, "host (development)" under `serve`. They are
	// display-only.
	Runtime string
	Version string
	// Indexer runs the bounded pre-index on the first-use project create.
	// nil is valid: the job starts without a populated Dormant Index.
	Indexer RepoPreindexer
	// DefaultTTL is applied to defer decisions made without an explicit
	// duration.
	DefaultTTL time.Duration
}

// UI serves the web interface.
type UI struct {
	deps Deps
	hub  *Hub
	// qualMu/qualRunning guard the one in-process qualification worker.
	qualMu      sync.Mutex
	qualRunning bool
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
	mux.HandleFunc("GET /ui/events/stream", u.handleDashboardStream)
	mux.HandleFunc("GET /ui/projects", u.handleProjects)
	mux.HandleFunc("GET /ui/projects/new", u.handleNewProject)
	mux.HandleFunc("POST /ui/projects", u.handleProjectCreate)
	mux.HandleFunc("GET /ui/projects/{id}", u.handleProject)
	mux.HandleFunc("GET /ui/projects/{id}/interview", u.handleInterviewPage)
	mux.HandleFunc("POST /ui/projects/{id}/interview", u.handleInterviewSave)
	mux.HandleFunc("POST /ui/projects/{id}/interview/approve", u.handleInterviewApprove)
	mux.HandleFunc("POST /ui/projects/{id}/interview/skip", u.handleInterviewSkip)
	mux.HandleFunc("POST /ui/projects/{id}/goals", u.handleGoalCreate)
	mux.HandleFunc("POST /ui/projects/{id}/corrections", u.handleCorrectionCreate)
	mux.HandleFunc("GET /ui/corrections", u.handleCorrections)
	mux.HandleFunc("GET /ui/reviews", u.handleReviews)
	mux.HandleFunc("GET /ui/notifications", u.handleNotifications)
	mux.HandleFunc("GET /ui/notifications/count", u.handleNotificationCount)
	mux.HandleFunc("GET /ui/setup", u.handleSetup)
	mux.HandleFunc("POST /ui/setup/qualify", u.handleQualifyStart)
	mux.HandleFunc("GET /ui/statistics", u.handleStatistics)
	mux.HandleFunc("POST /ui/corrections/{id}", u.handleCorrectionUpdate)
	mux.HandleFunc("GET /ui/jobs/{id}", u.handleWatch)
	mux.HandleFunc("GET /ui/jobs/{id}/stream", u.handleJobStream)
	mux.HandleFunc("POST /ui/jobs/{id}/note", u.handleNote)
	mux.HandleFunc("POST /ui/jobs/{id}/control", u.handleControl)
	mux.HandleFunc("GET /ui/artifacts/{id}", u.handleArtifact)
	mux.HandleFunc("POST /ui/artifacts/{id}/review", u.handleReviewCreate)
	mux.HandleFunc("GET /ui/artifacts/{id}/diff", u.handleDiff)
	mux.HandleFunc("POST /ui/approvals/{id}", u.handleApproval)
	mux.HandleFunc("GET /ui/events", u.handleEvents)
	mux.HandleFunc("GET /ui/artifacts", u.handleArtifactIndex)
	mux.HandleFunc("GET /ui/memory", u.handleMemory)
	mux.HandleFunc("GET /ui/memory/compacted/{id}", u.handleCompacted)
	mux.HandleFunc("GET /ui/memory/chunks/{id}", u.handleChunk)
	mux.HandleFunc("GET /ui/daydream", u.handleDaydream)
	mux.HandleFunc("GET /ui/runtime", u.handleRuntime)
	mux.HandleFunc("GET /ui/jobs/{id}/trace", u.handleTrace)
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
	names := u.projectNames(ctx, active, terminal)
	var recentArts []artifact.Artifact
	if u.deps.Artifacts != nil {
		recentArts, _ = u.deps.Artifacts.ListAccepted(ctx, 5)
	}
	for _, a := range recentArts {
		u.addProjectName(ctx, names, a.ProjectID)
	}
	reviewCount := 0
	if u.deps.Reviews != nil {
		if ids, qerr := u.deps.Reviews.Queue(ctx, 100); qerr == nil {
			reviewCount = len(ids)
		}
	}
	day, _ := digest.Build(ctx, u.deps.Store, time.Now().Add(-24*time.Hour), time.Now())
	u.render(w, "Dashboard", dashboardBody, map[string]any{
		"Active": active, "Terminal": terminal, "Pending": pending, "Frozen": frozen,
		"ProjectNames": names, "ReviewCount": reviewCount, "RecentArtifacts": recentArts,
		"Digest": day,
	})
}

// projectNames resolves the distinct project ids on a page to display names.
// The store has a single connection (ADR-0027), so callers must pass data that
// is already drained; Get is called sequentially.
func (u *UI) projectNames(ctx context.Context, active, terminal []job.Job) map[string]string {
	names := map[string]string{}
	for _, j := range active {
		u.addProjectName(ctx, names, j.ProjectID)
	}
	for _, j := range terminal {
		u.addProjectName(ctx, names, j.ProjectID)
	}
	return names
}

func (u *UI) addProjectName(ctx context.Context, names map[string]string, id string) {
	if id == "" {
		return
	}
	if _, ok := names[id]; ok {
		return
	}
	names[id] = id
	if p, err := u.deps.Projects.Get(ctx, id); err == nil {
		names[id] = p.Name
	}
}

// handleReviews renders the blinded human review queue (F4). It lists accepted
// artifacts with no review yet, newest first, and the recent reviews.
func (u *UI) handleReviews(w http.ResponseWriter, r *http.Request) {
	if u.deps.Reviews == nil {
		u.fail(w, fmt.Errorf("review queue is not configured"))
		return
	}
	ctx := r.Context()
	ids, err := u.deps.Reviews.Queue(ctx, 50)
	if err != nil {
		u.fail(w, err)
		return
	}
	type queueItem struct {
		Artifact    artifact.Artifact
		ProjectName string
	}
	items := make([]queueItem, 0, len(ids))
	for _, id := range ids {
		a, err := u.deps.Artifacts.Get(ctx, id)
		if err != nil {
			continue
		}
		name := a.ProjectID
		if p, err := u.deps.Projects.Get(ctx, a.ProjectID); err == nil {
			name = p.Name
		}
		items = append(items, queueItem{Artifact: a, ProjectName: name})
	}
	recent, _ := u.deps.Reviews.ListRecent(ctx, 20)
	u.render(w, "Review queue", reviewsBody, map[string]any{
		"Queue": items, "Recent": recent,
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

// handleNewProject renders the first-use project form. It is deliberately
// small: the full project interview (F3) is still to-build, but a human can
// create a project without touching the CLI.
func (u *UI) handleNewProject(w http.ResponseWriter, r *http.Request) {
	u.render(w, "New project", newProjectBody, map[string]any{
		"Archetype": "code", "Start": true,
	})
}

// handleProjectCreate creates a project from the UI form, applies the optional
// execution command, runs a bounded pre-index when a repository is supplied,
// and starts the first job. It mirrors the POST /projects API path; the GUI is
// the human-facing surface, not a second semantics.
func (u *UI) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		u.fail(w, fmt.Errorf("invalid form: %w", err))
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	archetype := strings.TrimSpace(r.FormValue("archetype"))
	if archetype == "" {
		archetype = project.ArchetypeCode
	}
	goal := strings.TrimSpace(r.FormValue("goal"))
	repo := strings.TrimSpace(r.FormValue("repository_path"))
	testCmd := strings.TrimSpace(r.FormValue("test_command"))
	criteria := splitCriteria(r.FormValue("criteria"))
	start := r.FormValue("start") != ""

	values := map[string]any{
		"Name": name, "Archetype": archetype, "Goal": goal,
		"RepositoryPath": repo, "TestCommand": testCmd,
		"Criteria": r.FormValue("criteria"), "Start": start,
	}
	if name == "" || goal == "" {
		values["Error"] = "name and goal are required"
		u.render(w, "New project", newProjectBody, values)
		return
	}
	deps := firstuse.Deps{
		Projects: u.deps.Projects, Jobs: u.deps.Jobs, Engine: u.deps.Engine,
		Events: u.deps.Store, Freezer: u.deps.Freezer,
	}
	if u.deps.Indexer != nil {
		deps.Indexer = uiPreindexer{inner: u.deps.Indexer}
	}
	var execution *project.Execution
	if testCmd != "" {
		execution = &project.Execution{TestCommand: testCmd}
	}
	res, err := firstuse.Create(r.Context(), deps, firstuse.Input{
		Name: name, Archetype: archetype, Goal: goal,
		RepositoryPath: repo, Criteria: criteria, Execution: execution, Start: start,
	})
	if err != nil {
		values["Error"] = err.Error()
		u.render(w, "New project", newProjectBody, values)
		return
	}
	if !start {
		http.Redirect(w, r, "/ui/projects/"+res.Project.ID, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/ui/jobs/"+res.JobID, http.StatusSeeOther)
}

// splitCriteria turns a textarea (one criterion per line) into the API's
// criteria slice. Blank lines are ignored; order is preserved.
func splitCriteria(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
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
	tasks, err := u.deps.Projects.TasksByProject(ctx, p.ID)
	if err != nil {
		u.fail(w, err)
		return
	}
	type workItem struct {
		Task project.Task
		Jobs []job.Job
	}
	work := make([]workItem, 0, len(tasks))
	for _, t := range tasks {
		jobs, err := u.deps.Jobs.ByTask(ctx, t.ID)
		if err != nil {
			u.fail(w, err)
			return
		}
		work = append(work, workItem{Task: t, Jobs: jobs})
	}
	u.render(w, "Project "+p.Name, projectBody, map[string]any{
		"Project": p, "Artifacts": arts, "Corrections": corr, "Work": work,
	})
}

// interviewState loads the project, its first task, and the resumable
// interview row. The plan is generated only when the operator saves answers;
// this page can be revisited at any time.
func (u *UI) interviewState(ctx context.Context, projectID string) (project.Project, project.Task, interview.Interview, error) {
	if u.deps.Interviews == nil {
		return project.Project{}, project.Task{}, interview.Interview{}, fmt.Errorf("project interview is not configured")
	}
	p, err := u.deps.Projects.Get(ctx, projectID)
	if err != nil {
		return project.Project{}, project.Task{}, interview.Interview{}, err
	}
	tasks, err := u.deps.Projects.TasksByProject(ctx, projectID)
	if err != nil {
		return project.Project{}, project.Task{}, interview.Interview{}, err
	}
	if len(tasks) == 0 {
		return project.Project{}, project.Task{}, interview.Interview{}, fmt.Errorf("project has no task")
	}
	in, err := u.deps.Interviews.Ensure(ctx, projectID, interview.Questions(p.Archetype))
	return p, tasks[0], in, err
}

func (u *UI) handleInterviewPage(w http.ResponseWriter, r *http.Request) {
	p, task, in, err := u.interviewState(r.Context(), r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	u.render(w, "Plan "+p.Name, interviewBody, map[string]any{
		"Project": p, "Task": task, "Interview": in,
		"Questions": in.Questions, "Plan": in.Plan, "Answers": in.Answers,
	})
}

func (u *UI) handleInterviewSave(w http.ResponseWriter, r *http.Request) {
	p, task, in, err := u.interviewState(r.Context(), r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	answers := map[string]string{}
	for _, q := range in.Questions {
		if v := strings.TrimSpace(r.FormValue(q.ID)); v != "" {
			answers[q.ID] = v
		}
	}
	plan := interview.BuildPlan(interview.PlanInput{
		Name: p.Name, Archetype: p.Archetype, Goal: p.Goal,
		Criteria: task.Criteria, TestCommand: p.TestCommand(),
		Answers: answers, Questions: in.Questions,
	})
	if _, err := u.deps.Interviews.SaveAnswers(r.Context(), p.ID, answers, plan); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/projects/"+p.ID+"/interview", http.StatusSeeOther)
}

func (u *UI) handleInterviewApprove(w http.ResponseWriter, r *http.Request) {
	u.decideInterview(w, r, interview.StatusApproved)
}

func (u *UI) handleInterviewSkip(w http.ResponseWriter, r *http.Request) {
	u.decideInterview(w, r, interview.StatusSkipped)
}

func (u *UI) decideInterview(w http.ResponseWriter, r *http.Request, status string) {
	projectID := r.PathValue("id")
	if _, err := u.deps.Interviews.Decide(r.Context(), projectID, status); err != nil {
		u.fail(w, err)
		return
	}
	deps := firstuse.Deps{
		Projects: u.deps.Projects, Jobs: u.deps.Jobs, Engine: u.deps.Engine,
		Events: u.deps.Store, Freezer: u.deps.Freezer,
	}
	jobID, err := firstuse.StartProject(r.Context(), deps, projectID)
	if err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/jobs/"+jobID, http.StatusSeeOther)
}

// handleGoalCreate submits a follow-up goal to an existing project from the
// project page. It creates the M1 task/job and enqueues it, then redirects to
// the watch view. The full project interview (F3) is still to-build; this is
// the minimal human loop.
func (u *UI) handleGoalCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		u.fail(w, fmt.Errorf("invalid form: %w", err))
		return
	}
	projectID := r.PathValue("id")
	goal := strings.TrimSpace(r.FormValue("goal"))
	if goal == "" {
		u.fail(w, fmt.Errorf("goal is required"))
		return
	}
	criteria := splitCriteria(r.FormValue("criteria"))
	task, err := u.deps.Projects.SubmitGoal(r.Context(), projectID, goal, criteria)
	if err != nil {
		u.fail(w, err)
		return
	}
	j, err := u.deps.Jobs.Create(r.Context(), task.ID, projectID)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.deps.Engine.Enqueue(j.ID)
	http.Redirect(w, r, "/ui/jobs/"+j.ID, http.StatusSeeOther)
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
		"Outcomes": outcomes,
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
	if len(events) > 100 {
		events = events[len(events)-100:]
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	projectName := j.ProjectID
	if p, err := u.deps.Projects.Get(ctx, j.ProjectID); err == nil {
		projectName = p.Name
	}
	u.render(w, "Watch "+id, watchBody, map[string]any{
		"Job": j, "Artifacts": arts, "Notes": notes, "Events": events,
		"ProjectName": projectName,
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
	// Seed lastID so the initial page's events are not re-sent; LatestEventID
	// is an index lookup, not a scan of the job's event history (M9).
	lastID, _ = u.deps.Store.LatestEventID(r.Context(), store.EventFilter{JobID: jobID})
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

	// Seed lastID with the current tail so the backlog is not replayed; it is
	// an index lookup, not a scan of the unbounded events table (M9).
	lastID, _ := u.deps.Store.LatestEventID(r.Context(), store.EventFilter{})
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

// uiEventBatch bounds how many events the SSE loops fetch per tick: a large
// backlog drains over several ticks instead of one unbounded scan of the
// append-only events table (known-issues M9).
const uiEventBatch = 500

// newEvents returns events newer than *lastID (optionally for one job) and
// advances *lastID to the newest returned id. The store applies the
// `id > lastID` delta and a batch limit, so the work per tick is bounded. A
// query error yields nothing (the next tick retries).
func (u *UI) newEvents(ctx context.Context, jobID string, lastID *int64) []store.EventRecord {
	events, err := u.deps.Store.QueryEvents(ctx, store.EventFilter{
		JobID:   jobID,
		AfterID: *lastID,
		Limit:   uiEventBatch,
	})
	if err != nil {
		return nil
	}
	if len(events) > 0 {
		*lastID = events[len(events)-1].ID
	}
	return events
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
		// Prefer the engine's cancellation path: it cancels the phase context
		// and commits `cancelled` from the job goroutine, so a long LLM or pod
		// call stops instead of continuing against an already-cancelled row.
		if c, ok := u.deps.Engine.(interface{ Cancel(string) bool }); ok && c.Cancel(id) {
			break
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
// handleArtifact renders one artifact's full content. The project page links
// here; without it the human could see statuses and diffs but not the accepted
// bytes.
func (u *UI) handleArtifact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	a, err := u.deps.Artifacts.Get(ctx, id)
	if err != nil {
		u.fail(w, err)
		return
	}
	content, err := u.deps.Artifacts.ReadContent(ctx, id)
	if err != nil {
		u.fail(w, err)
		return
	}
	var reviews []review.Review
	if u.deps.Reviews != nil {
		reviews, _ = u.deps.Reviews.ListByArtifact(ctx, id)
	}
	// The presentation surface (first-use F5): what was built, how to run it,
	// what it cost, and what is known-broken. Each source is optional; the
	// page renders what exists rather than failing.
	p, _ := u.deps.Projects.Get(ctx, a.ProjectID)
	var outcome strategy.Outcome
	hasOutcome := false
	if u.deps.Strategy != nil && a.JobID != "" {
		if o, err := u.deps.Strategy.GetOutcomeByJob(ctx, a.JobID); err == nil {
			outcome, hasOutcome = o, true
		}
	}
	var issues []corrections.Record
	if u.deps.Corrections != nil {
		if all, err := u.deps.Corrections.ListByProject(ctx, a.ProjectID); err == nil {
			for _, c := range all {
				if c.ArtifactID == a.ID {
					issues = append(issues, c)
				}
			}
		}
	}
	u.render(w, "Artifact "+a.ID, artifactBody, map[string]any{
		"Artifact": a, "Content": string(content), "Reviews": reviews,
		"Project": p, "Outcome": outcome, "HasOutcome": hasOutcome, "Issues": issues,
	})
}

func (u *UI) handleReviewCreate(w http.ResponseWriter, r *http.Request) {
	if u.deps.Reviews == nil {
		u.fail(w, fmt.Errorf("review queue is not configured"))
		return
	}
	_ = r.ParseForm()
	score, err := strconv.ParseFloat(r.FormValue("score"), 64)
	if err != nil {
		u.fail(w, fmt.Errorf("score must be a number between 0 and 1"))
		return
	}
	art, err := u.deps.Artifacts.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	if _, err := u.deps.Reviews.Create(r.Context(), review.CreateInput{
		ArtifactID: art.ID, JobID: art.JobID, ProjectID: art.ProjectID,
		Score: score, Accepted: r.FormValue("accepted") != "",
		Blinded: true, Note: r.FormValue("note"),
	}); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/artifacts/"+art.ID, http.StatusSeeOther)
}

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
	u.renderStatus(w, http.StatusOK, title, body, data)
}

// renderStatus renders the shared shell with an explicit status code. The
// header must be written after Content-Type is set and before the body.
func (u *UI) renderStatus(w http.ResponseWriter, status int, title, body string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["Title"] = title
	if _, ok := data["Runtime"]; !ok {
		data["Runtime"] = u.deps.Runtime
	}
	if _, ok := data["Version"]; !ok {
		data["Version"] = u.deps.Version
	}
	t := template.Must(template.New("page").Funcs(funcs).Parse(document(body)))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := t.Execute(w, data); err != nil {
		slog.Error("ui: rendering page", "title", title, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (u *UI) fail(w http.ResponseWriter, err error) {
	u.renderStatus(w, http.StatusBadRequest, "Error", errorBody, map[string]any{
		"Error": err.Error(),
	})
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
	"gib": func(n int64) string {
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	},
	"when": func(v any) string {
		var t time.Time
		switch x := v.(type) {
		case time.Time:
			t = x
		case *time.Time:
			if x == nil {
				return ""
			}
			t = *x
		default:
			return ""
		}
		if t.IsZero() {
			return ""
		}
		now := time.Now()
		if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
			return t.Format("15:04")
		}
		if t.Year() == now.Year() {
			return t.Format("Jan 2 15:04")
		}
		return t.Format("Jan 2 2006")
	},
	"prettyEvent": prettyEvent,
	"tojson": func(v any) string {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return ""
		}
		return string(b)
	},
	"interviewDone": func(s string) bool { return s == "approved" || s == "skipped" },
	"stateClass": func(v any) string {
		s := fmt.Sprint(v)
		switch s {
		case "completed":
			return "ok"
		case "failed", "cancelled":
			return "bad"
		case "paused", "awaiting_approval":
			return "warn"
		case "queued":
			return ""
		default:
			return "run"
		}
	},
}

func writeSSE(w http.ResponseWriter, event, data string) {
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

// prettyEvent renders one audit row as a compact, human-readable line. The raw
// JSON remains the source of truth; this is display only.
func prettyEvent(raw string) string {
	var d map[string]any
	if json.Unmarshal([]byte(raw), &d) != nil {
		return raw
	}
	name, _ := d["event"].(string)
	if name == "" {
		name = "event"
	}
	keys := []string{
		"phase", "state", "to", "winner", "result", "passed", "score",
		"reason", "model", "prompt_tokens", "completion_tokens",
		"reasoning_tokens", "decision", "unit", "iter", "candidates",
		"count", "error", "difficulty", "note",
	}
	var parts []string
	for _, k := range keys {
		v, ok := d[k]
		if !ok || v == nil || v == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		if len(parts) == 5 {
			break
		}
	}
	line := name
	if len(parts) > 0 {
		line += " · " + strings.Join(parts, " · ")
	}
	if len(line) > 220 {
		line = line[:220] + "…"
	}
	return line
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `""`
	}
	return string(b)
}

// lineDiff is an LCS line diff. Equal lines are context; removed and added
// lines are tagged with "-"/"+". It uses Hirschberg's algorithm (known-issues
// M10), so memory is linear in the smaller input rather than the O(n*m)
// matrix the earlier implementation allocated (a ~20k-line pair allocated
// gigabytes). Inputs above maxDiffLines are summarized as a full replace to
// bound the O(n*m) time as well.
type diffLine struct {
	Kind string // " ", "-", "+", "@"
	Text string
}

// maxDiffLines caps the line diff (known-issues M10). Above it the change is
// rendered coarsely rather than burning minutes of CPU on a UI request.
const maxDiffLines = 4000

func lineDiff(oldText, newText string) []diffLine {
	a := splitLines(oldText)
	b := splitLines(newText)
	if len(a) > maxDiffLines || len(b) > maxDiffLines {
		out := make([]diffLine, 0, len(a)+len(b)+1)
		for _, l := range a {
			out = append(out, diffLine{"-", l})
		}
		out = append(out, diffLine{"@", fmt.Sprintf("… large change: %d lines → %d lines (diff summarized) …", len(a), len(b))})
		for _, l := range b {
			out = append(out, diffLine{"+", l})
		}
		return out
	}
	return mergeDiff(a, b, lcsSeq(a, b))
}

// lcsSeq returns a longest common subsequence of a and b using Hirschberg's
// algorithm: O(len(a)*len(b)) time, O(min(len(a),len(b))) space.
func lcsSeq(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	if len(a) == 1 {
		for _, x := range b {
			if x == a[0] {
				return []string{a[0]}
			}
		}
		return nil
	}
	mid := len(a) / 2
	left := lcsRow(a[:mid], b)
	right := lcsRow(reverseStrings(a[mid:]), reverseStrings(b))
	best, bestK := -1, 0
	for k := 0; k <= len(b); k++ {
		if v := left[k] + right[len(b)-k]; v > best {
			best, bestK = v, k
		}
	}
	out := lcsSeq(a[:mid], b[:bestK])
	return append(out, lcsSeq(a[mid:], b[bestK:])...)
}

// lcsRow returns the final DP row: row[j] = LCS length of a and b[:j].
func lcsRow(a, b []string) []int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := range a {
		cur[0] = 0
		for j := range b {
			switch {
			case a[i] == b[j]:
				cur[j+1] = prev[j] + 1
			case prev[j+1] >= cur[j]:
				cur[j+1] = prev[j+1]
			default:
				cur[j+1] = cur[j]
			}
		}
		prev, cur = cur, prev
	}
	return prev
}

func reverseStrings(s []string) []string {
	out := make([]string, len(s))
	for i := range s {
		out[i] = s[len(s)-1-i]
	}
	return out
}

// mergeDiff walks a, b, and their common subsequence to produce the diff.
// lcs must be a subsequence of both a and b (which lcsSeq guarantees).
func mergeDiff(a, b, lcs []string) []diffLine {
	var out []diffLine
	i, j, k := 0, 0, 0
	for k < len(lcs) {
		for a[i] != lcs[k] {
			out = append(out, diffLine{"-", a[i]})
			i++
		}
		for b[j] != lcs[k] {
			out = append(out, diffLine{"+", b[j]})
			j++
		}
		out = append(out, diffLine{" ", a[i]})
		i++
		j++
		k++
	}
	for ; i < len(a); i++ {
		out = append(out, diffLine{"-", a[i]})
	}
	for ; j < len(b); j++ {
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
