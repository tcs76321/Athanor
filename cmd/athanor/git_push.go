package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
)

// gitPusher creates a HITL request for a push attempt (M6-T5, ADR-0036).
// Creating a request is the whole "attempt": no push happens until an
// operator approves it.
type gitPusher struct {
	projects *project.Repo
	hitl     *hitl.Repo
}

// RequestPush records a high-severity git_push request for the project's
// agent branch. A project without a repository_path cannot be pushed.
func (g gitPusher) RequestPush(ctx context.Context, projectID, remote string) (hitl.Request, error) {
	proj, err := g.projects.Get(ctx, projectID)
	if err != nil {
		return hitl.Request{}, err
	}
	if proj.RepositoryPath == "" {
		return hitl.Request{}, fmt.Errorf("%w: project %s", project.ErrNoRepository, projectID)
	}
	if remote == "" {
		remote = "origin"
	}
	payload, err := json.Marshal(map[string]string{
		"project_id": proj.ID, "remote": remote, "branch": "athanor/" + proj.ID,
	})
	if err != nil {
		return hitl.Request{}, err
	}
	return g.hitl.Create(ctx, hitl.Request{
		ProjectID: proj.ID, Type: hitl.TypeGitPush, Severity: hitl.SeverityHigh,
		PayloadJSON: string(payload),
	})
}

// gitPushCommand is the git surface the approver needs (the real
// implementation is gitClient.Push; tests inject a fake).
type gitPushCommand interface {
	Push(ctx context.Context, repoPath, remote, branch string) error
}

// gitPushApprover runs the approved push (ADR-0036). It is registered as the
// git_push approver, so it is invoked only on approval.
type gitPushApprover struct {
	git      gitPushCommand
	projects *project.Repo
	events   *store.Store
}

func (a gitPushApprover) Approve(ctx context.Context, req hitl.Request) error {
	var p struct {
		ProjectID string `json:"project_id"`
		Remote    string `json:"remote"`
		Branch    string `json:"branch"`
	}
	if err := json.Unmarshal([]byte(req.PayloadJSON), &p); err != nil {
		return fmt.Errorf("git_push payload: %w", err)
	}
	proj, err := a.projects.Get(ctx, p.ProjectID)
	if err != nil {
		return err
	}
	if proj.RepositoryPath == "" {
		return errors.New("git_push: project has no repository_path")
	}
	if p.Remote == "" {
		p.Remote = "origin"
	}
	if p.Branch == "" {
		p.Branch = "athanor/" + proj.ID
	}

	if err := a.git.Push(ctx, proj.RepositoryPath, p.Remote, p.Branch); err != nil {
		a.audit(ctx, proj.ID, map[string]any{
			"event": "git_push_failed", "remote": p.Remote, "branch": p.Branch, "error": err.Error(),
		})
		return err
	}
	a.audit(ctx, proj.ID, map[string]any{
		"event": "git_pushed", "remote": p.Remote, "branch": p.Branch, "request_id": req.ID,
	})
	return nil
}

func (a gitPushApprover) audit(ctx context.Context, projectID string, data map[string]any) {
	if a.events == nil {
		return
	}
	if _, err := a.events.AppendEvent(ctx, store.Event{Category: "jobs", ProjectID: projectID, Data: data}); err != nil {
		slog.Error("git_push: appending event", "err", err)
	}
}
