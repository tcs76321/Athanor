// Package firstuse owns the shared project-create on-ramp: create the project,
// apply the execution override, run one bounded repository pre-index when the
// project has a repository, create the first job, and enqueue it. The HTTP API
// and the local UI both call this so the two surfaces cannot drift.
package firstuse

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

// ErrFrozen reports a create that would start work while the kill switch is
// engaged. Callers map it to their own status (API 409, UI 400).
var ErrFrozen = errors.New("daemon is frozen: no new work is accepted")

// Enqueuer starts a job (the engine).
type Enqueuer interface{ Enqueue(jobID string) }

// Indexer runs one bounded repository index pass and returns its counters.
type Indexer interface {
	IndexOnceSummary(ctx context.Context, projectID, path string) (Summary, error)
}

// Freezer reports the kill-switch state.
type Freezer interface{ Frozen() bool }

// Summary is the pre-index counters the API/UI report.
type Summary struct {
	Discovered int `json:"discovered"`
	Indexed    int `json:"indexed"`
	Skipped    int `json:"skipped"`
	Chunks     int `json:"chunks"`
	Summarized int `json:"summarized"`
}

// Deps is the service's dependency set.
type Deps struct {
	Projects *project.Repo
	Jobs     *job.Repository
	Engine   Enqueuer
	Indexer  Indexer
	Events   *store.Store
	Freezer  Freezer
}

// Input is one project create. Start false creates metadata only.
type Input struct {
	Name           string
	Archetype      string
	Goal           string
	RepositoryPath string
	Criteria       []string
	Execution      *project.Execution
	Start          bool
}

// Result is the created project/task plus the optional pre-index outcome.
type Result struct {
	Project      project.Project
	Task         project.Task
	JobID        string
	Index        *Summary
	IndexWarning string
}

// Create is the single project-create path. It is deliberately synchronous
// about the project/task/job writes (the caller needs the ids) and bounded
// about pre-index: a failed or timed-out pass is a warning, not a failed
// create. The first job is enqueued only after the pre-index attempt, so a
// repo-backed first job is not blind.
func Create(ctx context.Context, deps Deps, in Input) (Result, error) {
	if in.Start && deps.Freezer != nil && deps.Freezer.Frozen() {
		return Result{}, ErrFrozen
	}
	start := in.Start
	p, task, err := deps.Projects.Create(ctx, in.Name, in.Archetype, in.Goal, in.RepositoryPath, in.Criteria)
	if err != nil {
		return Result{}, err
	}
	if in.Execution != nil {
		if err := deps.Projects.SetExecution(ctx, p.ID, *in.Execution); err != nil {
			return Result{}, fmt.Errorf("setting project execution: %w", err)
		}
		p.Execution = *in.Execution
	}

	res := Result{Project: p, Task: task}
	if start && p.RepositoryPath != "" && deps.Indexer != nil {
		ictx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		sum, ierr := deps.Indexer.IndexOnceSummary(ictx, p.ID, p.RepositoryPath)
		cancel()
		if ierr != nil {
			res.IndexWarning = "repository pre-index failed: " + ierr.Error()
			slog.Warn("firstuse: repository pre-index failed",
				"project", p.ID, "repo", p.RepositoryPath, "err", ierr)
		} else {
			res.Index = &sum
		}
	}

	if start {
		j, err := deps.Jobs.Create(ctx, task.ID, task.ProjectID)
		if err != nil {
			return Result{}, fmt.Errorf("creating first job: %w", err)
		}
		res.JobID = j.ID
		if deps.Engine != nil {
			deps.Engine.Enqueue(j.ID)
		}
	}
	if deps.Events != nil && (res.Index != nil || res.IndexWarning != "") {
		data := map[string]any{"event": "repo_preindex", "repo": p.RepositoryPath}
		if res.Index != nil {
			data["discovered"] = res.Index.Discovered
			data["indexed"] = res.Index.Indexed
			data["skipped"] = res.Index.Skipped
			data["chunks"] = res.Index.Chunks
			data["summarized"] = res.Index.Summarized
		}
		if res.IndexWarning != "" {
			data["warning"] = res.IndexWarning
		}
		if _, err := deps.Events.AppendEvent(ctx, store.Event{
			Category: "context", ProjectID: p.ID, JobID: res.JobID, Data: data,
		}); err != nil {
			slog.Warn("firstuse: auditing repository pre-index", "project", p.ID, "err", err)
		}
	}
	return res, nil
}
