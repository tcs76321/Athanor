package ui

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/corrections"
	"github.com/tcs76321/athanor/internal/firstuse"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/strategy"
	"github.com/tcs76321/athanor/internal/trigger"
)

// This file owns the project surface: the project list and create flow, one
// project's work history, follow-up goals, corrections, the review queue, and
// the statistics panel. The watch view is in watch.go.

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
	u.render(w, "New project", newProjectBody, map[string]any{"Start": true})
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
	goal := strings.TrimSpace(r.FormValue("goal"))
	repo := strings.TrimSpace(r.FormValue("repository_path"))
	boundaries := strings.TrimSpace(r.FormValue("boundaries"))
	sources := splitCriteria(r.FormValue("sources"))
	onRamp := strings.TrimSpace(r.FormValue("onramp"))

	values := map[string]any{
		"Name": name, "Goal": goal, "RepositoryPath": repo,
		"Boundaries": boundaries, "Sources": r.FormValue("sources"), "OnRamp": onRamp,
	}
	if goal == "" {
		values["Error"] = "a goal is required"
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
	res, err := firstuse.Create(r.Context(), deps, firstuse.Input{
		Name: name, Goal: goal, RepositoryPath: repo,
		Boundaries: boundaries, Sources: sources, OnRamp: onRamp,
		Start: false,
	})
	if err != nil {
		values["Error"] = err.Error()
		u.render(w, "New project", newProjectBody, values)
		return
	}
	if repo != "" {
		u.startIndex(res.Project)
	}
	http.Redirect(w, r, "/ui/projects/"+res.Project.ID+"/onboarding", http.StatusSeeOther)
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
		Task      project.Task
		Jobs      []job.Job
		TotalJobs int
	}
	const maxProjectJobs = 20
	const maxProjectRows = 100
	artsTotal := len(arts)
	if len(arts) > maxProjectRows {
		arts = arts[:maxProjectRows]
	}
	corrTotal := len(corr)
	if len(corr) > maxProjectRows {
		corr = corr[:maxProjectRows]
	}
	work := make([]workItem, 0, len(tasks))
	for _, t := range tasks {
		jobs, err := u.deps.Jobs.ByTask(ctx, t.ID)
		if err != nil {
			u.fail(w, err)
			return
		}
		total := len(jobs)
		if len(jobs) > maxProjectJobs {
			jobs = jobs[:maxProjectJobs]
		}
		work = append(work, workItem{Task: t, Jobs: jobs, TotalJobs: total})
	}
	var scheduled []trigger.Record
	if u.deps.Triggers != nil {
		if list, err := u.deps.Triggers.ListByProject(ctx, p.ID); err == nil {
			scheduled = list
		}
	}
	u.render(w, "Project "+p.Name, projectBody, map[string]any{
		"Project": p, "Artifacts": arts, "Corrections": corr, "Work": work,
		"ArtifactsTotal": artsTotal, "CorrectionsTotal": corrTotal,
		"CanPush": u.deps.Pusher != nil, "PushRequested": r.URL.Query().Get("push") == "requested",
		"IndexRun": u.indexView(p.ID), "HasIndexer": u.deps.Indexer != nil,
		"Scheduled": scheduled,
	})
}

// handleGoalCreate submits a follow-up goal to an existing project from the
// project page. It creates the M1 task/job and enqueues it, then redirects to
// the watch view.
// handleTaskReset returns a blocked task and its blocked dependents to
// pending through the scheduler's operator recovery path.
func (u *UI) handleTaskReset(w http.ResponseWriter, r *http.Request) {
	if u.deps.TaskResetter == nil {
		u.fail(w, fmt.Errorf("task recovery is not configured"))
		return
	}
	id := r.PathValue("id")
	t, err := u.deps.Projects.Task(r.Context(), id)
	if err != nil {
		u.fail(w, err)
		return
	}
	if _, err := u.deps.TaskResetter.Reset(r.Context(), id); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/projects/"+t.ProjectID+"#work", http.StatusSeeOther)
}

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
	jobID, err := firstuse.StartTaskJob(r.Context(), u.deps.Store, u.deps.Projects, u.deps.Jobs, u.deps.Engine, task.ID, projectID)
	if err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/jobs/"+jobID, http.StatusSeeOther)
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
