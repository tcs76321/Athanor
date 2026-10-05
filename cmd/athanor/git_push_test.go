package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

type fakeGitPush struct {
	calls                int
	repo, remote, branch string
	err                  error
}

func (f *fakeGitPush) Push(_ context.Context, repo, remote, branch string) error {
	f.calls++
	f.repo, f.remote, f.branch = repo, remote, branch
	return f.err
}

func pushFixture(t *testing.T, repositoryPath string) (*project.Repo, *store.Store, project.Project) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	projects := project.NewRepo(s)
	p, _, err := projects.Create(context.Background(), "demo", project.ArchetypeCode,
		"Build a small REST endpoint with tests and documentation.", repositoryPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	return projects, s, p
}

func TestGitPushApproverPushesOnApprove(t *testing.T) {
	projects, s, proj := pushFixture(t, "/tmp/demo-repo")
	git := &fakeGitPush{}
	approver := gitPushApprover{git: git, projects: projects, events: s}
	payload, _ := json.Marshal(map[string]string{
		"project_id": proj.ID, "remote": "upstream", "branch": "athanor/x",
	})

	if err := approver.Approve(context.Background(), hitl.Request{ID: "r1", PayloadJSON: string(payload)}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if git.calls != 1 || git.repo != "/tmp/demo-repo" || git.remote != "upstream" || git.branch != "athanor/x" {
		t.Fatalf("push args = %+v", git)
	}
	events, err := s.QueryEvents(context.Background(), store.EventFilter{Category: "jobs"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if strings.Contains(e.DataJSON, "git_pushed") {
			found = true
		}
	}
	if !found {
		t.Errorf("missing git_pushed audit event")
	}
}

func TestGitPushApproverNoRepository(t *testing.T) {
	projects, s, proj := pushFixture(t, "")
	git := &fakeGitPush{}
	approver := gitPushApprover{git: git, projects: projects, events: s}
	payload, _ := json.Marshal(map[string]string{"project_id": proj.ID})

	if err := approver.Approve(context.Background(), hitl.Request{PayloadJSON: string(payload)}); err == nil {
		t.Fatal("expected an error for a project without a repository")
	}
	if git.calls != 0 {
		t.Errorf("push ran without a repository")
	}
}

func TestGitPushApproverReportsPushFailure(t *testing.T) {
	projects, s, proj := pushFixture(t, "/tmp/demo-repo")
	git := &fakeGitPush{err: errors.New("remote rejected")}
	approver := gitPushApprover{git: git, projects: projects, events: s}
	payload, _ := json.Marshal(map[string]string{"project_id": proj.ID})

	if err := approver.Approve(context.Background(), hitl.Request{PayloadJSON: string(payload)}); err == nil {
		t.Fatal("expected the push error to surface")
	}
}

func TestGitPusherCreatesRequest(t *testing.T) {
	projects, s, proj := pushFixture(t, "/tmp/demo-repo")
	repo := hitl.NewRepo(s)
	req, err := (gitPusher{projects: projects, hitl: repo}).RequestPush(context.Background(), proj.ID, "")
	if err != nil {
		t.Fatalf("RequestPush: %v", err)
	}
	if req.Type != hitl.TypeGitPush || req.Severity != hitl.SeverityHigh || req.Status != hitl.StatusPending {
		t.Fatalf("request = %+v", req)
	}
	pending, err := repo.Pending(context.Background())
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %v, %v", pending, err)
	}
}

func TestGitPusherRejectsMissingRepository(t *testing.T) {
	projects, s, proj := pushFixture(t, "")
	_, err := (gitPusher{projects: projects, hitl: hitl.NewRepo(s)}).RequestPush(context.Background(), proj.ID, "")
	if !errors.Is(err, project.ErrNoRepository) {
		t.Fatalf("err = %v, want ErrNoRepository", err)
	}
}
