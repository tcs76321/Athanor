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
	"github.com/tcs76321/athanor/internal/interview"
	"github.com/tcs76321/athanor/internal/job"
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

func TestPagesRender(t *testing.T) {
	fx := setup(t)
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/ui", "Dashboard"},
		{"/ui/projects", "demo"},
		{"/ui/projects/" + fx.project.ID, "Submit a goal"},
		{"/ui/corrections", "Corrections"},
		{"/ui/statistics", "captured outcomes"},
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
	for _, want := range []string{"New project", `action="/ui/projects"`, `name="repository_path"`, `name="test_command"`} {
		if !strings.Contains(body, want) {
			t.Errorf("new-project page missing %q", want)
		}
	}
}

// TestCreateProjectFromUI covers the metadata path: no job, execution command
// persisted, redirect to the project page.
func TestCreateProjectFromUI(t *testing.T) {
	fx := setup(t)
	form := url.Values{
		"name":            {"ui-project"},
		"archetype":       {"code"},
		"goal":            {"Build a small add function with tests and a docstring."},
		"repository_path": {"/tmp/repo"},
		"test_command":    {"pytest -q"},
		"criteria":        {"pytest passes\ndocstring present"},
	}
	resp := postForm(t, fx.ts.URL+"/ui/projects", form)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/ui/projects/") {
		t.Fatalf("Location = %q, want a project page", loc)
	}
	id := strings.TrimPrefix(loc, "/ui/projects/")
	p, err := fx.ui.deps.Projects.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if p.Execution.TestCommand != "pytest -q" {
		t.Errorf("test command = %q, want pytest -q", p.Execution.TestCommand)
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
		"archetype":       {"code"},
		"goal":            {"Build a small add function with tests and a docstring."},
		"repository_path": {"/tmp/repo"},
		"test_command":    {"pytest -q"},
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
	for _, want := range []string{"How to run", "What it cost", "Known issues", "Human review"} {
		if !strings.Contains(body, want) {
			t.Errorf("presentation surface missing %q", want)
		}
	}
}
