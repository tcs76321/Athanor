package ui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/corrections"
	"github.com/tcs76321/athanor/internal/hitl"
	"github.com/tcs76321/athanor/internal/interruptions"
	"github.com/tcs76321/athanor/internal/interview"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/onboard"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/review"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

type fakeFreezer struct{ frozen bool }

func (f fakeFreezer) Frozen() bool { return f.frozen }

type fakeEngine struct{ enqueued []string }

func (f *fakeEngine) Enqueue(id string) { f.enqueued = append(f.enqueued, id) }

type fakePreindexer struct {
	calls   int
	project string
	path    string
	err     error
}

func (f *fakePreindexer) Preindex(_ context.Context, projectID, path string) error {
	f.calls++
	f.project, f.path = projectID, path
	return f.err
}

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
		Corrections: corrRepo, HITL: hitlSvc, Reviews: review.NewRepo(s),
		HITLRepo:   hitlRepo,
		Interviews: interview.NewRepo(s), Interruptions: interRepo,
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

// TestProjectTitleEscaped proves the H4 fix: a project name (which the LLM can
// produce during autonomous work) is rendered through {{.Title}} data, so it is
// HTML-escaped and never folded into the template source. A name containing
// both an XSS payload and a template action must not inject markup or panic the
// template (previously a name with "{{" made template.Must panic → a permanent
// 500, and one with "<script>" injected unescaped into <title>).
func TestProjectTitleEscaped(t *testing.T) {
	fx := setup(t)
	ctx := context.Background()
	name := `"><script>alert(1)</script>{{.Title}}`
	p, _, err := fx.ui.deps.Projects.Create(ctx, name, project.ArchetypeText,
		"Build a small thing that does a useful job for me.", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	status, body := get(t, fx.ts.URL+"/ui/projects/"+p.ID)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a malicious name must not panic the template)", status)
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatal("project name was injected unescaped into the page")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("project name was not HTML-escaped in the rendered title")
	}
}

// TestProjectInterviewPage proves the F3 page renders the questions and the
// approval path; the API test covers the state transition.
func TestProjectInterviewPage(t *testing.T) {
	fx := setup(t)
	status, body := get(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/interview")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	for _, want := range []string{"Plan:", "Save answers", "Approve plan", "Skip interview"} {
		if !strings.Contains(body, want) {
			t.Errorf("interview page missing %q\n%s", want, body)
		}
	}
}

// TestMissingArtifactIs404 pins the error-status mapping: a missing entity is
// a not-found response, not a generic 400, and the error shell does not claim
// that no state changed.
func TestMissingArtifactIs404(t *testing.T) {
	fx := setup(t)
	status, body := get(t, fx.ts.URL+"/ui/artifacts/does-not-exist")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	if !strings.Contains(body, "not found") {
		t.Errorf("body = %q, want the not-found message", body)
	}
	if strings.Contains(body, "Nothing was changed") {
		t.Error("error shell still makes the false no-change promise")
	}
	if !strings.Contains(body, "Recent errors") {
		t.Error("error shell should offer a path to the event log")
	}
}

type fakeAlarmResolver struct{ resolved string }

func (f *fakeAlarmResolver) Resolve(_ context.Context, id string) error {
	f.resolved = id
	return nil
}

type fakePusher struct {
	project string
	remote  string
}

func (f *fakePusher) RequestPush(_ context.Context, projectID, remote string) (hitl.Request, error) {
	f.project, f.remote = projectID, remote
	return hitl.Request{ID: "req1", Type: hitl.TypeGitPush}, nil
}

// TestCompareArtifacts pins the candidate comparison: two artifacts render a
// structured diff, not just a pair of pages.
func TestCompareArtifacts(t *testing.T) {
	fx := setup(t)
	ctx := context.Background()
	a, err := fx.ui.deps.Artifacts.CreateDraftFor(ctx, fx.project.ID, fx.task.ID, fx.job.ID,
		artifact.KindProposal, []byte("print('a')\n"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := fx.ui.deps.Artifacts.CreateDraftFor(ctx, fx.project.ID, fx.task.ID, fx.job.ID,
		artifact.KindProposal, []byte("print('b')\n"))
	if err != nil {
		t.Fatal(err)
	}
	status, body := get(t, fx.ts.URL+"/ui/compare?a="+a.ID+"&b="+b.ID)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	for _, want := range []string{"Compare artifacts", "print(&#39;a&#39;)", "print(&#39;b&#39;)", "class=\"del\"", "class=\"add\""} {
		if !strings.Contains(body, want) {
			t.Errorf("compare page missing %q", want)
		}
	}
	status, _ = get(t, fx.ts.URL+"/ui/compare?a="+a.ID)
	if status != http.StatusBadRequest {
		t.Errorf("one-sided compare status = %d, want 400", status)
	}
}

// TestProjectIndexIsAsync pins the background index path: the request returns
// immediately and the status becomes visible on the project page.
func TestProjectIndexIsAsync(t *testing.T) {
	fx := setup(t)
	pre := &fakePreindexer{}
	fx.ui.deps.Indexer = pre
	resp := postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/index", url.Values{})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		run := fx.ui.indexView(fx.project.ID)
		if run != nil && run.Done {
			if run.Status != "done" || run.Error != "" {
				t.Fatalf("index run = %+v, want done", run)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("index did not finish: %+v", run)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pre.calls != 1 || pre.project != fx.project.ID || pre.path != "/tmp/demo" {
		t.Errorf("indexer = %+v, want one call for the project path", pre)
	}
	_, body := get(t, fx.ts.URL+"/ui/projects/"+fx.project.ID)
	if !strings.Contains(body, "Repository index") || !strings.Contains(body, "done") {
		t.Error("project page does not show the index status")
	}
}

// TestSearchFindsTasksAndArtifactsByProject pins the wider search: a project
// name matches its tasks and artifacts, not only the project row.
func TestSearchFindsTasksAndArtifactsByProject(t *testing.T) {
	fx := setup(t)
	status, body := get(t, fx.ts.URL+"/ui/search?format=json&q=demo")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	var out struct {
		Results []struct {
			Kind, Label string
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, r := range out.Results {
		kinds[r.Kind] = true
	}
	for _, want := range []string{"Project", "Task", "Artifact"} {
		if !kinds[want] {
			t.Errorf("search for demo did not return a %s result: %#v", want, out.Results)
		}
	}
}

// TestDailySurfaces pins the new run history and digest pages.
func TestDailySurfaces(t *testing.T) {
	fx := setup(t)
	for _, tc := range []struct{ path, want string }{
		{"/ui/jobs", "Runs"},
		{"/ui/jobs?state=failed", "No runs match"},
		{"/ui/digest", "Morning digest"},
	} {
		status, body := get(t, fx.ts.URL+tc.path)
		if status != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", tc.path, status)
			continue
		}
		if !strings.Contains(body, tc.want) {
			t.Errorf("GET %s missing %q", tc.path, tc.want)
		}
	}
	_, body := get(t, fx.ts.URL+"/ui/jobs")
	if !strings.Contains(body, fx.job.ID[:8]) {
		t.Errorf("runs page does not list the fixture job")
	}
}

func TestAlarmResolveFromInbox(t *testing.T) {
	fx := setup(t)
	res := &fakeAlarmResolver{}
	fx.ui.deps.Alarms = res
	resp := postForm(t, fx.ts.URL+"/ui/alarms/alarm-1/resolve", url.Values{})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if res.resolved != "alarm-1" {
		t.Errorf("resolved = %q, want alarm-1", res.resolved)
	}
}

func TestPushFilesARequest(t *testing.T) {
	fx := setup(t)
	push := &fakePusher{}
	fx.ui.deps.Pusher = push
	resp := postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/push", url.Values{"remote": {"origin"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if push.project != fx.project.ID || push.remote != "origin" {
		t.Errorf("push = %+v, want project %s remote origin", push, fx.project.ID)
	}
	_, body := get(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"?push=requested")
	if !strings.Contains(body, "Push request queued") {
		t.Error("project page did not confirm the push request")
	}
}

// TestAssetsUseETag pins the asset validator: an unchanged stylesheet/script
// revalidates with 304 instead of re-downloading on every page load.
func TestAssetsUseETag(t *testing.T) {
	fx := setup(t)
	resp, err := http.Get(fx.ts.URL + "/ui/assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("asset response has no ETag")
	}
	req, err := http.NewRequest(http.MethodGet, fx.ts.URL+"/ui/assets/app.css", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("If-None-Match", etag)
	again, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = again.Body.Close() }()
	if again.StatusCode != http.StatusNotModified {
		t.Fatalf("revalidated status = %d, want 304", again.StatusCode)
	}
}

// TestCodeProposalRendersAsSource pins the presentation fix: a divergence
// candidate is stored as `proposal`, but when the task is code it must render
// as source, not through the Markdown renderer.
func TestCodeProposalRendersAsSource(t *testing.T) {
	fx := setup(t)
	ctx := context.Background()
	art, err := fx.ui.deps.Artifacts.CreateDraftFor(ctx, fx.project.ID, fx.task.ID, fx.job.ID,
		artifact.KindProposal, []byte("# this is a comment\nprint(1)\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, body := get(t, fx.ts.URL+"/ui/artifacts/"+art.ID)
	if !strings.Contains(body, "<pre><code>") {
		t.Error("code proposal was not rendered as source")
	}
	if strings.Contains(body, "<h1>this is a comment</h1>") {
		t.Error("code proposal was rendered as Markdown")
	}
}

// TestArtifactDownloadServesExactBytes pins the download path and its inert
// content type; model output must never be served as renderable HTML.
func TestArtifactDownloadServesExactBytes(t *testing.T) {
	fx := setup(t)
	resp, err := http.Get(fx.ts.URL + "/ui/artifacts/" + fx.artifact.ID + "/download")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "line one\nline TWO\n" {
		t.Errorf("body = %q, want the exact artifact bytes", body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q, want text/markdown", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
}

// TestMultiFileArtifactShowsFilesAndBranch pins the multi-file reader and the
// Git-as-undo provenance block.
func TestMultiFileArtifactShowsFilesAndBranch(t *testing.T) {
	fx := setup(t)
	ctx := context.Background()
	content := "=== FILE: pkg/a.py ===\nprint(1)\n=== FILE: pkg/b.py ===\nprint(2)\n"
	art, err := fx.ui.deps.Artifacts.CreateDraftFor(ctx, fx.project.ID, fx.task.ID, fx.job.ID,
		artifact.KindCode, []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.ui.deps.Artifacts.SetGitCommit(ctx, art.ID, "abc123"); err != nil {
		t.Fatal(err)
	}
	_, body := get(t, fx.ts.URL+"/ui/artifacts/"+art.ID)
	for _, want := range []string{"pkg/a.py", "pkg/b.py", "data-copy-pre", "athanor/" + fx.project.ID, "abc123"} {
		if !strings.Contains(body, want) {
			t.Errorf("artifact page missing %q", want)
		}
	}
}

// TestDashboardApprovalIsReadable pins the HITL presentation: an approval shows
// its human label, fields, and expiry instead of a raw JSON blob.
func TestDashboardApprovalIsReadable(t *testing.T) {
	fx := setup(t)
	expires := time.Now().Add(2 * time.Hour)
	if _, err := fx.repo.Create(context.Background(), hitl.Request{
		ProjectID: fx.project.ID, Type: hitl.TypeGitPush, Severity: hitl.SeverityHigh,
		PayloadJSON: `{"branch":"athanor/p1","remote":"origin","commit":"abc123"}`,
		ExpiresAt:   &expires,
	}); err != nil {
		t.Fatal(err)
	}
	status, body := get(t, fx.ts.URL+"/ui")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	for _, want := range []string{"Push a branch to a remote", "athanor/p1", "origin", "abc123", "expires in 1h"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	if strings.Contains(body, `{"branch"`) {
		t.Error("dashboard still renders the raw approval JSON")
	}
}

// TestWatchShowsFailure pins the failure card: the event log is the only place
// a job's error lives, so the watch page must promote it instead of making the
// operator read raw events.
func TestWatchShowsFailure(t *testing.T) {
	fx := setup(t)
	ctx := context.Background()
	if _, err := fx.ui.deps.Jobs.Transition(ctx, fx.job.ID, job.StateContextBuilding); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.ui.deps.Jobs.Transition(ctx, fx.job.ID, job.StateFailed); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.ui.deps.Store.AppendEvent(ctx, store.Event{
		Category: "jobs", JobID: fx.job.ID,
		Data: map[string]any{"event": "job_failed", "state": "context_building", "error": "the model refused the task"},
	}); err != nil {
		t.Fatal(err)
	}
	status, body := get(t, fx.ts.URL+"/ui/jobs/"+fx.job.ID)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	for _, want := range []string{"This run did not finish", "the model refused the task", "Retry this task", "Open the full trace"} {
		if !strings.Contains(body, want) {
			t.Errorf("watch page missing %q", want)
		}
	}
}

// TestWatchHidesIllegalPause pins the control guard: queued and
// awaiting_approval jobs have no edge to paused, so the button must not be
// offered (clicking it used to render a 400 page).
func TestWatchHidesIllegalPause(t *testing.T) {
	fx := setup(t)
	_, body := get(t, fx.ts.URL+"/ui/jobs/"+fx.job.ID)
	if strings.Contains(body, `value="pause"`) {
		t.Error("queued job page offers Pause, which is an illegal transition")
	}
	ctx := context.Background()
	if _, err := fx.ui.deps.Jobs.Transition(ctx, fx.job.ID, job.StateContextBuilding); err != nil {
		t.Fatal(err)
	}
	_, body = get(t, fx.ts.URL+"/ui/jobs/"+fx.job.ID)
	if !strings.Contains(body, `value="pause"`) {
		t.Error("running job page should offer Pause")
	}
}

// TestWatchCarriesLastEventID pins the SSE catch-up plumbing: the page tells
// the stream the newest event it rendered, so events written before the
// EventSource connects are delivered rather than silently skipped.
func TestWatchCarriesLastEventID(t *testing.T) {
	fx := setup(t)
	ctx := context.Background()
	id, err := fx.ui.deps.Store.AppendEvent(ctx, store.Event{
		Category: "jobs", JobID: fx.job.ID,
		Data: map[string]any{"event": "phase_started", "phase": "planning"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, body := get(t, fx.ts.URL+"/ui/jobs/"+fx.job.ID)
	want := `data-last-event="` + strconv.FormatInt(id, 10) + `"`
	if !strings.Contains(body, want) {
		t.Errorf("watch page missing %s", want)
	}
}

func TestPagesRender(t *testing.T) {
	fx := setup(t)
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/ui", "Home"},
		{"/ui/projects", "demo"},
		{"/ui/projects/" + fx.project.ID, "Submit a goal"},
		{"/ui/corrections", "Corrections"},
		{"/ui/statistics", "captured outcomes"},
		{"/ui/jobs/" + fx.job.ID, "Live activity"},
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

// TestNewEventsAdvancesWatermark pins the M9 bounded-delta contract: a read
// returns only events after the watermark and advances it.
func TestNewEventsAdvancesWatermark(t *testing.T) {
	fx := setup(t)
	ctx := context.Background()
	lastID, err := fx.ui.deps.Store.LatestEventID(ctx, store.EventFilter{JobID: fx.job.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.ui.deps.Store.AppendEvent(ctx, store.Event{
		Category: "jobs", JobID: fx.job.ID, Data: map[string]any{"event": "one"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.ui.deps.Store.AppendEvent(ctx, store.Event{
		Category: "jobs", JobID: fx.job.ID, Data: map[string]any{"event": "two"},
	}); err != nil {
		t.Fatal(err)
	}
	got := fx.ui.newEvents(ctx, fx.job.ID, &lastID)
	if len(got) != 2 {
		t.Fatalf("events = %d, want 2", len(got))
	}
	if lastID != got[1].ID {
		t.Errorf("watermark = %d, want %d", lastID, got[1].ID)
	}
	if again := fx.ui.newEvents(ctx, fx.job.ID, &lastID); len(again) != 0 {
		t.Errorf("second read = %d events, want 0", len(again))
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

// TestLineDiffBuildsValidEditScript (known-issues M10): the Hirschberg diff
// reconstructs the new text from old + the edit script, and an oversized input
// is summarized rather than diffed in full.
func TestLineDiffBuildsValidEditScript(t *testing.T) {
	cases := [][2]string{
		{"", ""},
		{"a\nb\nc", "a\nb\nc"},
		{"a\nb\nc", "a\nx\nc"},
		{"only old", ""},
		{"", "only new"},
		{"1\n2\n3\n4\n5", "1\n3\n5\n6"},
	}
	for _, c := range cases {
		d := lineDiff(c[0], c[1])
		var rebuilt strings.Builder
		for _, l := range d {
			if l.Kind == "+" || l.Kind == " " {
				rebuilt.WriteString(l.Text)
				rebuilt.WriteByte('\n')
			}
		}
		want := c[1]
		if want != "" {
			want += "\n"
		}
		if got := rebuilt.String(); got != want {
			t.Errorf("lineDiff(%q,%q) rebuilt %q, want %q", c[0], c[1], got, want)
		}
	}

	big := strings.Repeat("line\n", maxDiffLines+10)
	summarized := false
	for _, l := range lineDiff(big, big+"extra\n") {
		if l.Kind == "@" {
			summarized = true
		}
	}
	if !summarized {
		t.Error("oversized input was not summarized (known-issues M10)")
	}
}

// TestInspectorPagesRender proves the read-only inspector surfaces are
// reachable and expose the recorded internals.
// TestFirstUseCopy covers the dashboard copy fixes: the kill switch reads as a
// state, not as an enabled control, and singular counts are not "artifact(s)".
func TestFirstUseCopy(t *testing.T) {
	fx := setup(t)
	_, body := get(t, fx.ts.URL+"/ui")
	for _, want := range []string{"Not frozen", "Needs you", "Inbox", "review-count"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	if strings.Contains(body, "artifact(s)") {
		t.Error("dashboard still contains the artifact(s) placeholder")
	}
}

// TestHostDevBanner proves the supported-runtime warning appears only under the
// host development process, not under the Core Pod.
func TestHostDevBanner(t *testing.T) {
	fx := setup(t)
	fx.ui.deps.Runtime = "host (development)"
	_, body := get(t, fx.ts.URL+"/ui")
	if !strings.Contains(body, "Development runtime") {
		t.Error("host dev page is missing the supported-runtime banner")
	}
	fx.ui.deps.Runtime = "Core Pod"
	_, body = get(t, fx.ts.URL+"/ui")
	if strings.Contains(body, "Development runtime") {
		t.Error("Core Pod page should not show the host dev banner")
	}
}

// TestLongRepositoryPathWraps pins the layout fix: the project metadata path
// must be in a wrapping element so it cannot overlap the Created column.
func TestLongRepositoryPathWraps(t *testing.T) {
	fx := setup(t)
	_, body := get(t, fx.ts.URL+"/ui/projects/"+fx.project.ID)
	if !strings.Contains(body, `class="mono wrap"`) {
		t.Error("project repository path is not marked as wrapping")
	}
}

func TestResultLabel(t *testing.T) {
	if got := funcs["resultLabel"].(func(string) string)("accepted_new"); got != "accepted — new winner" {
		t.Errorf("accepted_new label = %q", got)
	}
	if got := funcs["resultLabel"].(func(string) string)("accepted_previous"); got != "accepted — previous kept" {
		t.Errorf("accepted_previous label = %q", got)
	}
	plural := funcs["plural"].(func(int, string, string) string)
	if got := plural(1, "artifact", "artifacts"); got != "artifact" {
		t.Errorf("plural(1) = %q", got)
	}
	if got := plural(2, "artifact", "artifacts"); got != "artifacts" {
		t.Errorf("plural(2) = %q", got)
	}
}

func TestInspectorPagesRender(t *testing.T) {
	fx := setup(t)
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/ui/events", "Every recorded step"},
		{"/ui/events?category=jobs&level=info", "Events"},
		{"/ui/artifacts", "Every artifact"},
		{"/ui/memory", "Compacted memory"},
		{"/ui/daydream", "Daydream"},
		{"/ui/runtime", "Personas"},
		{"/ui/jobs/" + fx.job.ID + "/trace", "Model calls"},
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

// TestSetupPageRenders proves the onboarding surface is reachable.
func TestSetupPageRenders(t *testing.T) {
	fx := setup(t)
	status, body := get(t, fx.ts.URL+"/ui/setup")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	for _, want := range []string{"Setup", "Model inventory", "Personas", "Qualification"} {
		if !strings.Contains(body, want) {
			t.Errorf("setup page missing %q", want)
		}
	}
}

func TestSetupPageShowsMeasuredModelAudit(t *testing.T) {
	fx := setup(t)
	audit := onboard.Audit{
		GeneratedAt: "2026-11-04T00:00:00Z", HostBytes: 32 << 30,
		ReserveBytes: 8 << 30, BudgetBytes: 24 << 30,
		Roles: []onboard.AuditRole{{
			Role: "main", Model: "gemma4:12b-mlx", ContextTarget: 32768,
			FileBytes: 8 << 30, LoadedBytes: 12 << 30, Fits: true,
		}},
	}
	raw, err := json.Marshal(audit)
	if err != nil {
		t.Fatal(err)
	}
	fx.ui.deps.ModelAudit = string(raw)
	status, body := get(t, fx.ts.URL+"/ui/setup")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	for _, want := range []string{"Measured model fit", "gemma4:12b-mlx", "12.0 GiB", "fits"} {
		if !strings.Contains(body, want) {
			t.Errorf("setup page missing audit detail %q", want)
		}
	}
}

// TestSetupPageWithModels proves the live inventory and persona rows render
// against the configured Ollama endpoint.
func TestSetupPageWithModels(t *testing.T) {
	fx := setup(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{{
				"name": "fake:latest", "size": 1234567,
				"details": map[string]string{"family": "qwen"},
			}},
		})
	}))
	defer ts.Close()

	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Inference.OllamaURL = ts.URL
	cfg.Personas.Security.Model = "fake:latest"
	fx.ui.deps.Config = cfg

	status, body := get(t, fx.ts.URL+"/ui/setup")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	for _, want := range []string{"fake:latest", "Model inventory", "Run qualification", "security"} {
		if !strings.Contains(body, want) {
			t.Errorf("setup page missing %q", want)
		}
	}
}

func TestQualMeasurementsFromState(t *testing.T) {
	s := newQualState()
	s.Results["security|m|c1"] = true
	s.Results["security|m|c2"] = false
	s.Results["main|m|c3"] = true
	ms := qualMeasurementsFromState(s)
	if len(ms) != 2 {
		t.Fatalf("measurements = %+v, want 2", ms)
	}
	for _, m := range ms {
		switch m.Role {
		case "security":
			if m.Samples != 2 || m.Score != 0.5 {
				t.Errorf("security measurement = %+v", m)
			}
		case "main":
			if m.Samples != 1 || m.Score != 1 {
				t.Errorf("main measurement = %+v", m)
			}
		}
	}
}

// TestQualificationRunsAgainstFakeOllama exercises the bounded worker end to
// end without a real model.
func TestQualificationRunsAgainstFakeOllama(t *testing.T) {
	fx := setup(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		prompt := ""
		if len(body.Messages) > 0 {
			prompt = body.Messages[0].Content
		}
		answer := "A"
		if strings.Contains(prompt, "17 * 23") {
			answer = "391"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{"content": answer},
			"done":    true,
		})
	}))
	defer ts.Close()

	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Inference.OllamaURL = ts.URL
	cfg.Personas.Security.Model = "fake"
	cfg.Personas.Main.Model = "fake"
	fx.ui.deps.Config = cfg

	state := newQualState()
	roles := []onboard.Role{{Role: "security", Model: "fake"}, {Role: "main", Model: "fake"}}
	models := []onboard.Model{{Name: "fake", SizeBytes: 1}}
	fx.ui.runQualification(context.Background(), cfg, roles, models, &state)

	if state.Running {
		t.Error("qualification still marked running")
	}
	if state.Total == 0 || state.Done != state.Total {
		t.Errorf("done/total = %d/%d, want a complete run", state.Done, state.Total)
	}
	ms := qualMeasurementsFromState(state)
	if len(ms) != 2 {
		t.Fatalf("measurements = %+v, want security and main", ms)
	}
}

// TestNotificationsPageAndCount proves the in-app notification surface and the
// header badge JSON.
func TestNotificationsPageAndCount(t *testing.T) {
	fx := setup(t)
	status, body := get(t, fx.ts.URL+"/ui/notifications")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	for _, want := range []string{"Notifications", "Pending approvals", "Enable desktop notifications"} {
		if !strings.Contains(body, want) {
			t.Errorf("notifications page missing %q", want)
		}
	}
	_, raw := get(t, fx.ts.URL+"/ui/notifications/count")
	var counts map[string]int
	if err := json.Unmarshal([]byte(raw), &counts); err != nil {
		t.Fatalf("count JSON: %v (%q)", err, raw)
	}
	if counts["pending"] < 1 {
		t.Errorf("pending = %d, want at least the fixture approval", counts["pending"])
	}
	if counts["total"] < 1 {
		t.Errorf("total = %d, want at least 1", counts["total"])
	}
}

// TestEventsInspectorShowsRawEvents proves the event inspector exposes the
// recorded row and honors the text/job filters.
func TestEventsInspectorShowsRawEvents(t *testing.T) {
	fx := setup(t)
	ctx := context.Background()
	if _, err := fx.ui.deps.Store.AppendEvent(ctx, store.Event{
		Category: "jobs", JobID: fx.job.ID,
		Data: map[string]any{"event": "test_marker", "detail": "unique_needle"},
	}); err != nil {
		t.Fatal(err)
	}
	_, body := get(t, fx.ts.URL+"/ui/events?job="+fx.job.ID)
	for _, want := range []string{"test_marker", "unique_needle", "raw event"} {
		if !strings.Contains(body, want) {
			t.Errorf("events inspector missing %q", want)
		}
	}
	_, filtered := get(t, fx.ts.URL+"/ui/events?q=unique_needle")
	if !strings.Contains(filtered, "test_marker") {
		t.Error("text filter did not return the matching event")
	}
	_, none := get(t, fx.ts.URL+"/ui/events?q=no_such_needle")
	if !strings.Contains(none, "No events match") {
		t.Error("empty filter state missing")
	}
}

// TestDashboardShowsDigest proves the morning-digest strip renders.
func TestDashboardShowsDigest(t *testing.T) {
	fx := setup(t)
	_, body := get(t, fx.ts.URL+"/ui")
	for _, want := range []string{"Last 24 hours", "jobs completed", "artifacts accepted"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
}

// TestReviewQueuePage proves the blinded human review queue is reachable and
// lists an accepted artifact that has no review.
func TestReviewQueuePage(t *testing.T) {
	fx := setup(t)
	ctx := context.Background()
	if err := fx.ui.deps.Artifacts.SetStatus(ctx, fx.artifact.ID, artifact.StatusCandidate); err != nil {
		t.Fatal(err)
	}
	if err := fx.ui.deps.Artifacts.SetStatus(ctx, fx.artifact.ID, artifact.StatusAccepted); err != nil {
		t.Fatal(err)
	}
	status, body := get(t, fx.ts.URL+"/ui/reviews")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	for _, want := range []string{"Review queue", "Review this", "demo"} {
		if !strings.Contains(body, want) {
			t.Errorf("review queue missing %q", want)
		}
	}
	// The dashboard surfaces accepted work, not only active jobs.
	_, dash := get(t, fx.ts.URL+"/ui")
	for _, want := range []string{"Recently accepted", "document"} {
		if !strings.Contains(dash, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
}

// TestNewProjectPage proves the first-use GUI form is reachable and points at
// the project-create POST route.
func TestNewProjectPage(t *testing.T) {
	fx := setup(t)
	status, body := get(t, fx.ts.URL+"/ui/projects/new")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	for _, want := range []string{"New project", `action="/ui/projects"`, `name="repository_path"`, `name="goal"`, "infers what it can produce"} {
		if !strings.Contains(body, want) {
			t.Errorf("new-project page missing %q", want)
		}
	}
	for _, gone := range []string{`name="archetype"`, `name="test_command"`} {
		if strings.Contains(body, gone) {
			t.Errorf("new-project page still exposes the old field %s", gone)
		}
	}
}

// TestCreateProjectFromUI covers the metadata path: no job, execution command
// persisted, redirect to the project page.
func TestCreateProjectFromUI(t *testing.T) {
	fx := setup(t)
	form := url.Values{
		"name":            {"ui-project"},
		"goal":            {"Build a small add function with tests and a docstring."},
		"repository_path": {"/tmp/repo"},
		"criteria":        {"pytest passes\ndocstring present"},
	}
	resp := postForm(t, fx.ts.URL+"/ui/projects", form)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/ui/projects/") {
		t.Fatalf("Location = %q, want a project onboarding page", loc)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(loc, "/ui/projects/"), "/onboarding")
	p, err := fx.ui.deps.Projects.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if p.Execution.TestCommand != "" {
		t.Errorf("test command = %q, want no explicit command (repository detection only)", p.Execution.TestCommand)
	}
	if !slices.Contains(p.Capabilities, project.CapabilityCode) {
		t.Errorf("capabilities = %v, want code", p.Capabilities)
	}
	if len(fx.engine.enqueued) != 0 {
		t.Errorf("enqueued = %v, want none without start", fx.engine.enqueued)
	}
}

// TestCreateProjectFromUIStartsJob covers the human happy path: pre-index the
// repo, create the first job, enqueue it, and redirect to the watch view.
func TestCreateProjectFromUIStartsJob(t *testing.T) {
	fx := setup(t)
	idx := &fakePreindexer{}
	fx.ui.deps.Indexer = idx
	form := url.Values{
		"name":            {"ui-starts"},
		"goal":            {"Build a small add function with tests and a docstring."},
		"repository_path": {"/tmp/repo"},
		"criteria":        {"pytest passes"},
		"start":           {"1"},
	}
	resp := postForm(t, fx.ts.URL+"/ui/projects", form)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/ui/jobs/") {
		t.Fatalf("Location = %q, want a job watch page", loc)
	}
	if idx.calls != 1 || idx.path != "/tmp/repo" {
		t.Fatalf("pre-index calls=%d path=%q, want one call for /tmp/repo", idx.calls, idx.path)
	}
	if len(fx.engine.enqueued) != 1 {
		t.Fatalf("enqueued = %v, want one job", fx.engine.enqueued)
	}
	jobID := strings.TrimPrefix(loc, "/ui/jobs/")
	if _, err := fx.ui.deps.Jobs.Get(context.Background(), jobID); err != nil {
		t.Fatalf("job %s not persisted: %v", jobID, err)
	}
}

// TestCreateGoalFromUI covers the iterative human loop: submit a follow-up
// goal from the project page and land on its watch view.
func TestCreateGoalFromUI(t *testing.T) {
	fx := setup(t)
	form := url.Values{
		"goal":     {"Add a small subtract function with tests."},
		"criteria": {"pytest passes\nsubtract works"},
	}
	resp := postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/goals", form)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/ui/jobs/") {
		t.Fatalf("Location = %q, want a job watch page", loc)
	}
	if len(fx.engine.enqueued) != 1 {
		t.Fatalf("enqueued = %v, want one job", fx.engine.enqueued)
	}
	jobID := strings.TrimPrefix(loc, "/ui/jobs/")
	if _, err := fx.ui.deps.Jobs.Get(context.Background(), jobID); err != nil {
		t.Fatalf("job %s not persisted: %v", jobID, err)
	}
}

// TestArtifactView proves the human can read the accepted bytes, not just the
// status and diff.
func TestArtifactView(t *testing.T) {
	fx := setup(t)
	status, body := get(t, fx.ts.URL+"/ui/artifacts/"+fx.artifact.ID)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if !strings.Contains(body, "line TWO") {
		t.Fatal("artifact content is not rendered")
	}
	for _, want := range []string{"How to use it", "What it cost", "Known issues", "Human review"} {
		if !strings.Contains(body, want) {
			t.Errorf("presentation surface missing %q", want)
		}
	}
}
