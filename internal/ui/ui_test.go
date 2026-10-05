package ui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/corrections"
	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/interruptions"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

type fakeFreezer struct{ frozen bool }

func (f fakeFreezer) Frozen() bool { return f.frozen }

type fakeEngine struct{ enqueued []string }

func (f *fakeEngine) Enqueue(id string) { f.enqueued = append(f.enqueued, id) }

type fixtures struct {
	ui       *UI
	ts       *httptest.Server
	project  project.Project
	task     project.Task
	job      job.Job
	artifact artifact.Artifact
	corr     corrections.Record
	hitlID   string
	repo     *hitl.Repo
	engine   *fakeEngine
}

func setup(t *testing.T) *fixtures {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	projects := project.NewRepo(s)
	p, task, err := projects.Create(ctx, "demo", project.ArchetypeCode,
		"Build a small REST endpoint with tests and documentation.", "/tmp/demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	jobs := job.NewRepository(s)
	j, err := jobs.Create(ctx, task.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	arts := artifact.NewStore(s, filepath.Join(t.TempDir(), "artifacts"))
	a1, err := arts.CreateDraftFor(ctx, p.ID, task.ID, j.ID, artifact.KindDocument, []byte("line one\nline two\n"))
	if err != nil {
		t.Fatal(err)
	}
	a2, err := arts.NewVersion(ctx, a1.ID, []byte("line one\nline TWO\n"))
	if err != nil {
		t.Fatal(err)
	}
	corrRepo := corrections.NewRepo(s)
	corr, err := corrRepo.Capture(ctx, corrections.CaptureInput{
		Source: corrections.SourceTestFailure, ProjectID: p.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	hitlRepo := hitl.NewRepo(s)
	hitlReq, err := hitlRepo.Create(ctx, hitl.Request{
		ProjectID: p.ID, Type: hitl.TypeTaskEscalation, Severity: hitl.SeverityHigh,
	})
	if err != nil {
		t.Fatal(err)
	}
	hitlSvc := hitl.NewService(hitlRepo, jobs, &fakeEngine{}, time.Hour)
	interRepo := interruptions.NewRepo(s)

	eng := &fakeEngine{}
	webUI := New(Deps{
		Store: s, Projects: projects, Jobs: jobs, Artifacts: arts,
		Corrections: corrRepo, HITL: hitlSvc, Interruptions: interRepo,
		Freezer: fakeFreezer{}, Engine: eng, DefaultTTL: time.Hour,
	})
	mux := http.NewServeMux()
	webUI.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	return &fixtures{ui: webUI, ts: ts, project: p, task: task, job: j,
		artifact: a2, corr: corr, hitlID: hitlReq.ID, repo: hitlRepo, engine: eng}
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// noRedirectClient returns the 3xx itself instead of following it, so a
// handler's Post/Redirect/Get is observable.
var noRedirectClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func postForm(t *testing.T, url string, form url.Values) *http.Response {
	t.Helper()
	resp, err := noRedirectClient.PostForm(url, form)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp
}

func TestPagesRender(t *testing.T) {
	fx := setup(t)
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/ui", "Dashboard"},
		{"/ui/projects", "demo"},
		{"/ui/projects/" + fx.project.ID, "Artifacts"},
		{"/ui/corrections", "Corrections"},
		{"/ui/jobs/" + fx.job.ID, "Live output"},
		{"/ui/artifacts/" + fx.artifact.ID + "/diff", "line TWO"},
	} {
		status, body := get(t, fx.ts.URL+tc.path)
		if status != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", tc.path, status)
			continue
		}
		if !strings.Contains(body, tc.want) {
			t.Errorf("GET %s body missing %q", tc.path, tc.want)
		}
	}
}

func TestRejectionFormAndMute(t *testing.T) {
	fx := setup(t)
	form := url.Values{
		"source": {"user_rejection"}, "category": {"style"}, "severity": {"high"},
		"reason": {"used global state"}, "desired_behavior": {"prefer injection"},
		"scope": {"project"}, "artifact_id": {fx.artifact.ID},
	}
	resp := postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/corrections", form)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("rejection status = %d, want 303", resp.StatusCode)
	}
	list, err := fx.ui.deps.Corrections.ListByProject(context.Background(), fx.project.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("corrections = %v, want 2 (seeded + form)", list)
	}

	// Incomplete form is rejected (the store enforces §18.4).
	bad := postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/corrections", url.Values{"category": {"style"}})
	if bad.StatusCode == http.StatusSeeOther {
		t.Errorf("incomplete rejection form accepted")
	}

	// Mute the seeded correction.
	mute := postForm(t, fx.ts.URL+"/ui/corrections/"+fx.corr.ID, url.Values{"status": {"muted"}})
	if mute.StatusCode != http.StatusSeeOther {
		t.Fatalf("mute status = %d, want 303", mute.StatusCode)
	}
	if rec, _ := fx.ui.deps.Corrections.Get(context.Background(), fx.corr.ID); rec.Status != corrections.StatusMuted {
		t.Errorf("status = %q, want muted", rec.Status)
	}
}

func TestApprovalDecision(t *testing.T) {
	fx := setup(t)
	resp := postForm(t, fx.ts.URL+"/ui/approvals/"+fx.hitlID, url.Values{"action": {"approve"}, "note": {"ok"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("approval status = %d, want 303", resp.StatusCode)
	}
	req, err := fx.repo.Get(context.Background(), fx.hitlID)
	if err != nil || req.Status != hitl.StatusApproved {
		t.Fatalf("request = %+v, want approved", req)
	}
}

func TestInterruptionNoteAndCancel(t *testing.T) {
	fx := setup(t)
	resp := postForm(t, fx.ts.URL+"/ui/jobs/"+fx.job.ID+"/note", url.Values{"note": {"be brief"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("note status = %d, want 303", resp.StatusCode)
	}
	notes, _ := fx.ui.deps.Interruptions.List(context.Background(), fx.job.ID)
	if len(notes) != 1 || notes[0].Text != "be brief" {
		t.Fatalf("notes = %+v", notes)
	}

	// Cancel a queued job.
	cancel := postForm(t, fx.ts.URL+"/ui/jobs/"+fx.job.ID+"/control", url.Values{"action": {"cancel"}})
	if cancel.StatusCode != http.StatusSeeOther {
		t.Fatalf("cancel status = %d, want 303", cancel.StatusCode)
	}
	if j, _ := fx.ui.deps.Jobs.Get(context.Background(), fx.job.ID); j.State != job.StateCancelled {
		t.Errorf("job state = %s, want cancelled", j.State)
	}

	// Retry creates a new job and enqueues it.
	retry := postForm(t, fx.ts.URL+"/ui/jobs/"+fx.job.ID+"/control", url.Values{"action": {"retry"}})
	if retry.StatusCode != http.StatusSeeOther {
		t.Fatalf("retry status = %d, want 303", retry.StatusCode)
	}
	if len(fx.engine.enqueued) != 1 {
		t.Errorf("enqueued = %v, want 1", fx.engine.enqueued)
	}
}

func TestHubFanout(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe("j1")
	defer cancel()
	h.Publish("j1", "hello")
	select {
	case got := <-ch:
		if got != "hello" {
			t.Errorf("got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no token delivered")
	}
	h.Publish("j2", "other") // no subscriber; must not block
}
