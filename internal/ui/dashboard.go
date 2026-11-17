package ui

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/digest"
	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/operator"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

// This file owns the home dashboard: the project cards, the live event stream,
// and the household-mode control. The watch view lives in watch.go; shared
// rendering lives in render.go.

func (u *UI) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if u.needsOnboarding(ctx) {
		http.Redirect(w, r, "/ui/welcome", http.StatusSeeOther)
		return
	}
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
	onboarding := u.loadOnboarding(ctx)
	health := u.homeHealth(ctx, day.JobsFailed, day.ActiveAlarms, len(pending), frozen)
	var cards []projectCard
	projects, perr := u.listProjects(ctx)
	if perr == nil {
		for _, p := range projects {
			cards = append(cards, u.projectCard(ctx, p))
		}
	}
	var opStatus any
	if u.deps.Operator != nil {
		opStatus = u.deps.Operator.Status()
	}
	u.render(w, "Dashboard", dashboardBody, map[string]any{
		"Active": active, "Terminal": terminal, "Pending": pending, "Frozen": frozen,
		"Operator":     opStatus,
		"ProjectNames": names, "ReviewCount": reviewCount, "RecentArtifacts": recentArts,
		"Digest": day, "Projects": cards,
		"OnboardingPending": !onboarding.Completed && !onboarding.Skipped,
		"Health":            health, "HealthStatus": overallHealth(health),
	})
}

// projectCard is the dashboard's per-project summary: the latest job, the
// onboarding status, and the artifact count. Kept to a few bounded queries so
// the home page does not fan out across the whole graph.
type projectCard struct {
	Project       project.Project
	LastState     string
	LastJobID     string
	Onboarding    string
	ArtifactCount int
	TaskCount     int
}

func (u *UI) projectCard(ctx context.Context, p project.Project) projectCard {
	c := projectCard{Project: p, Onboarding: "not started"}
	if u.deps.Store == nil {
		return c
	}
	var jobID, state string
	if err := u.deps.Store.DB().QueryRowContext(ctx,
		`SELECT id, state FROM jobs WHERE project_id = ? ORDER BY created_at DESC LIMIT 1`, p.ID).
		Scan(&jobID, &state); err == nil {
		c.LastJobID, c.LastState = jobID, state
	}
	var onboarding string
	if u.deps.Intake != nil {
		if st, err := u.deps.Intake.State(ctx, p.ID); err == nil {
			onboarding = string(st.Stage)
		}
	}
	if onboarding != "" {
		c.Onboarding = onboarding
	}
	_ = u.deps.Store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM artifacts WHERE project_id = ?`, p.ID).Scan(&c.ArtifactCount)
	_ = u.deps.Store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE project_id = ?`, p.ID).Scan(&c.TaskCount)
	return c
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
func (u *UI) handleMode(w http.ResponseWriter, r *http.Request) {
	if u.deps.Operator == nil {
		u.fail(w, fmt.Errorf("operator mode is not configured"))
		return
	}
	if err := r.ParseForm(); err != nil {
		u.fail(w, fmt.Errorf("invalid mode form: %w", err))
		return
	}
	action := r.FormValue("action")
	var (
		mode operator.Mode
		hold time.Duration
		why  string
	)
	switch action {
	case "deep":
		mode = operator.ModeDeep
	case "full":
		mode = operator.ModeFull
	case "lite":
		mode = operator.ModeLite
	case "auto":
		mode = operator.ModeAuto
	case "stop":
		mode = operator.ModeStopped
		why = "operator stop"
	case "resume":
		mode = operator.ModeAuto
	case "use":
		mode = operator.ModeLite
		why = "using the computer"
		hold = 30 * time.Minute
		if r.FormValue("duration") == "1h" {
			hold = time.Hour
		}
	default:
		u.fail(w, fmt.Errorf("unknown mode action %q", action))
		return
	}
	if err := u.deps.Operator.Set(r.Context(), mode, hold, why); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui", http.StatusSeeOther)
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
		case <-u.done:
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
