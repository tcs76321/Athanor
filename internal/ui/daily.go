package ui

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tcs76321/athanor/internal/digest"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/internal/strategy"
)

// jobRow is one row of the global run history.
type jobRow struct {
	ID, ProjectID, ProjectName, TaskID, TaskTitle, State string
	Attempt                                              int
	Started, Finished, Created                           string
}

func (u *UI) handleJobsIndex(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := strings.TrimSpace(q.Get("state"))
	projectID := strings.TrimSpace(q.Get("project"))
	rows := queryRows(r.Context(), u.deps.Store.DB(), `
		SELECT j.id, COALESCE(j.project_id,''), COALESCE(p.name,''),
		       COALESCE(j.task_id,''), COALESCE(t.title,''), j.state, j.attempt,
		       COALESCE(j.started_at,''), COALESCE(j.finished_at,''), j.created_at
		  FROM jobs j
		  LEFT JOIN projects p ON p.id = j.project_id
		  LEFT JOIN tasks t ON t.id = j.task_id
		 WHERE (? = '' OR j.state = ?)
		   AND (? = '' OR j.project_id = ?)
		 ORDER BY j.created_at DESC, j.id DESC
		 LIMIT 200`, func(rows *sql.Rows) (jobRow, error) {
		var j jobRow
		if err := rows.Scan(&j.ID, &j.ProjectID, &j.ProjectName, &j.TaskID, &j.TaskTitle,
			&j.State, &j.Attempt, &j.Started, &j.Finished, &j.Created); err != nil {
			return j, err
		}
		return j, nil
	}, state, state, projectID, projectID)
	projects, _ := u.listProjects(r.Context())
	u.render(w, "Runs", jobsBody, map[string]any{
		"Jobs": rows, "State": state, "Project": projectID, "Projects": projects,
		"States": []string{"queued", "planning", "diverging", "evaluating", "reflecting",
			"synthesizing", "comparing", "paused", "awaiting_approval", "completed", "failed", "cancelled"},
	})
}

func (u *UI) handleDigest(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if v := r.URL.Query().Get("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 24*30 {
			hours = n
		}
	}
	until := time.Now()
	since := until.Add(-time.Duration(hours) * time.Hour)
	d, err := digest.Build(r.Context(), u.deps.Store, since, until)
	if err != nil {
		u.fail(w, err)
		return
	}
	var accepted []artifactSummary
	if u.deps.Artifacts != nil {
		arts, _ := u.deps.Artifacts.ListAccepted(r.Context(), 20)
		for _, a := range arts {
			name := a.ProjectID
			if p, err := u.deps.Projects.Get(r.Context(), a.ProjectID); err == nil {
				name = p.Name
			}
			accepted = append(accepted, artifactSummary{ID: a.ID, Kind: string(a.Kind), Version: a.Version, Project: name, Created: a.CreatedAt})
		}
	}
	var pending []string
	if u.deps.HITL != nil {
		if ps, err := u.deps.HITL.Pending(r.Context()); err == nil {
			now := time.Now()
			for _, p := range ps {
				pending = append(pending, hitlSummary(p.Type, p.Severity, p.ExpiresAt, now))
			}
		}
	}
	u.render(w, "Digest", digestBody, map[string]any{
		"Digest": d, "Hours": hours, "Accepted": accepted, "Pending": pending,
	})
}

// artifactSummary is the compact accepted-work row on the digest.
type artifactSummary struct {
	ID, Kind, Project string
	Version           int
	Created           time.Time
}

func (u *UI) handleAlarmResolve(w http.ResponseWriter, r *http.Request) {
	if u.deps.Alarms == nil {
		u.fail(w, fmt.Errorf("alarm resolution is not configured"))
		return
	}
	if err := u.deps.Alarms.Resolve(r.Context(), r.PathValue("id")); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/notifications", http.StatusSeeOther)
}

func (u *UI) handleInsightAction(w http.ResponseWriter, r *http.Request) {
	if u.deps.Strategy == nil {
		u.fail(w, fmt.Errorf("strategy analysis is not configured"))
		return
	}
	_ = r.ParseForm()
	status := ""
	switch r.FormValue("action") {
	case "promote":
		status = strategy.InsightActive
	case "mute":
		status = strategy.InsightMuted
	default:
		u.fail(w, fmt.Errorf("unknown insight action"))
		return
	}
	if err := u.deps.Strategy.SetInsightStatus(r.Context(), r.PathValue("id"), status); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/statistics", http.StatusSeeOther)
}

// handleProjectPriority sets the operator's project priority. Setting a new
// critical project demotes the old one in the same transaction (migration
// 0037 keeps at most one critical row).
func (u *UI) handleProjectPriority(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	projectID := r.PathValue("id")
	priority := strings.TrimSpace(r.FormValue("priority"))
	if !project.ValidPriority(priority) {
		u.fail(w, fmt.Errorf("priority must be critical, high, medium, or low"))
		return
	}
	if err := u.deps.Projects.SetPriority(r.Context(), projectID, priority); err != nil {
		u.fail(w, err)
		return
	}
	if u.deps.Store != nil {
		_, _ = u.deps.Store.AppendEvent(r.Context(), store.Event{
			Category: "projects", ProjectID: projectID,
			Data: map[string]any{"event": "project_priority_updated", "priority": priority},
		})
	}
	http.Redirect(w, r, "/ui/projects/"+projectID+"#settings", http.StatusSeeOther)
}

// handleProjectExecution edits the project-owned execution configuration. The
// default path infers the test command, so this is the advanced override the
// UI previously said was API/CLI-only.
func (u *UI) handleProjectExecution(w http.ResponseWriter, r *http.Request) {
	p, err := u.deps.Projects.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	ex := p.Execution
	ex.TestCommand = strings.TrimSpace(r.FormValue("test_command"))
	ex.BuildCommand = strings.TrimSpace(r.FormValue("build_command"))
	ex.FixturePath = strings.TrimSpace(r.FormValue("fixture_path"))
	ex.FixtureRoot = strings.TrimSpace(r.FormValue("fixture_root"))
	ex.DataSchema = strings.TrimSpace(r.FormValue("data_schema"))
	ex.Linters = splitCriteria(r.FormValue("linters"))
	rawChecks := strings.TrimSpace(r.FormValue("checks_json"))
	if rawChecks == "" {
		ex.Checks = nil
	} else {
		var checks project.ExecutionChecks
		if err := json.Unmarshal([]byte(rawChecks), &checks); err != nil {
			u.fail(w, fmt.Errorf("declared checks must be a JSON object: %w", err))
			return
		}
		ex.Checks = &checks
	}
	if err := u.deps.Projects.SetExecution(r.Context(), p.ID, ex); err != nil {
		u.fail(w, err)
		return
	}
	if u.deps.Store != nil {
		_, _ = u.deps.Store.AppendEvent(r.Context(), store.Event{
			Category: "projects", ProjectID: p.ID,
			Data: map[string]any{"event": "project_execution_updated", "test_command": ex.TestCommand},
		})
	}
	http.Redirect(w, r, "/ui/projects/"+p.ID+"#settings", http.StatusSeeOther)
}

func (u *UI) handleProjectPush(w http.ResponseWriter, r *http.Request) {
	if u.deps.Pusher == nil {
		u.fail(w, fmt.Errorf("push requests are not configured"))
		return
	}
	_ = r.ParseForm()
	projectID := r.PathValue("id")
	if _, err := u.deps.Pusher.RequestPush(r.Context(), projectID, strings.TrimSpace(r.FormValue("remote"))); err != nil {
		u.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ui/projects/"+projectID+"?push=requested#artifacts", http.StatusSeeOther)
}
