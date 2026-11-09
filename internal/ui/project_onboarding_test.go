package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/discovery"
	"github.com/tcs76321/athanor/internal/planning"
	"github.com/tcs76321/athanor/internal/project"
)

func TestLoadBriefSeedsSuccessFromCreateCriteria(t *testing.T) {
	fx := setup(t)
	ctx := context.Background()
	p, _, err := fx.ui.deps.Projects.Create(ctx, "criteria-project", project.ArchetypeText,
		"Write a useful brief for a local reader.", "", []string{"Names two concrete benefits", "Stays under 200 words"})
	if err != nil {
		t.Fatal(err)
	}
	brief, err := fx.ui.loadBrief(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if brief.Success != "Names two concrete benefits\nStays under 200 words" {
		t.Fatalf("brief success = %q; create criteria were not carried into the brief", brief.Success)
	}
}

type fakeDiscovery struct {
	mu   sync.Mutex
	sess discovery.Session
	has  bool
}

func (f *fakeDiscovery) Generate(_ context.Context, projectID string, _ discovery.Request) (discovery.Session, error) {
	opts := []discovery.Option{
		{ID: "o1", Title: "Daily news brief", Problem: "Fragmented coverage", Scope: "World news, one daily update; excludes opinion", Approach: "Group by consequence", Deliverable: "One-page brief", Check: "Five readers review it"},
		{ID: "o2", Title: "CSV reconciliation CLI", Problem: "Manual reconciliation", Scope: "Two exports; excludes schema migration", Approach: "Field-level diff", Deliverable: "Working prototype", Check: "Run on a known mismatch"},
		{ID: "o3", Title: "Procurement policy memo", Problem: "Inconsistent rules", Scope: "A two-page memo for the committee", Approach: "Compare and propose a checklist", Deliverable: "Two-page memo", Check: "Two policy owners review it"},
		{ID: "o4", Title: "Air-quality dataset", Problem: "Scattered formats", Scope: "One month of local readings", Approach: "Normalize and document gaps", Deliverable: "One cleaned month", Check: "Re-run on a second month"},
		{ID: "o5", Title: "Water-cycle diagrams", Problem: "Abstract process", Scope: "A secondary-school lesson", Approach: "Step-by-step annotation", Deliverable: "Three diagrams", Check: "A teacher uses them"},
		{ID: "o6", Title: "Battery-recycling digest", Problem: "Papers are easy to miss", Scope: "A small lab team; five papers per issue", Approach: "Annotate claims and limits", Deliverable: "Five annotated papers", Check: "The team rates it"},
	}
	f.mu.Lock()
	f.sess = discovery.Session{ID: "s1", ProjectID: projectID, Status: "ready", Options: opts}
	f.has = true
	f.mu.Unlock()
	return f.sess, nil
}

func (f *fakeDiscovery) Get(_ context.Context, projectID string) (discovery.Session, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.has {
		return discovery.Session{}, false, nil
	}
	return f.sess, true, nil
}

func (f *fakeDiscovery) Select(_ context.Context, _, optionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sess.SelectedID = optionID
	return nil
}

type fakePlanning struct {
	mu        sync.Mutex
	sess      planning.Session
	has       bool
	generated bool
}

func planCandidate(title string) planning.Candidate {
	return planning.Candidate{
		Title: title, Summary: "A plan for " + title,
		Tasks: []planning.Task{
			{Title: title + " task", Description: "A concrete first step.", Capability: "document", Criteria: []string{"done"}},
			{Title: title + " verify", Description: "Verify the first step.", Capability: "document", Criteria: []string{"verified"}},
		},
		Status: "ready",
	}
}

func (f *fakePlanning) Generate(_ context.Context, projectID, optionID string, _ planning.Request) (planning.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generated = true
	f.sess = planning.Session{
		ID: "ps1", ProjectID: projectID, OptionID: optionID, Status: "ready", SelectedIndex: -1,
		Candidates: []planning.Candidate{
			planCandidate("Smallest verifiable slice"),
			planCandidate("Deepest coverage"),
			planCandidate("Lowest-risk sequence"),
		},
	}
	f.has = true
	return f.sess, nil
}

func (f *fakePlanning) Get(_ context.Context, _ string) (planning.Session, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sess, f.has, nil
}

func (f *fakePlanning) Select(_ context.Context, _ string, index int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.has || f.sess.Status != "ready" {
		return fmt.Errorf("plan not ready")
	}
	if index < 0 || index >= len(f.sess.Candidates) {
		return fmt.Errorf("index out of range")
	}
	f.sess.SelectedIndex = index
	return nil
}

func TestHostAllowedSuffixBoundary(t *testing.T) {
	allow := []string{"wikipedia.org", "example.com"}
	cases := map[string]bool{
		"wikipedia.org":     true,
		"en.wikipedia.org":  true,
		"evilwikipedia.org": false,
		"example.com":       true,
		"sub.example.com":   true,
		"notexample.com":    false,
		"":                  false,
	}
	for host, want := range cases {
		if got := hostAllowed(host, allow); got != want {
			t.Errorf("hostAllowed(%q) = %v, want %v", host, got, want)
		}
	}
}

// TestPlanningProgressIsHonest pins the removal of fabricated progress. The
// page shows the real stage, model, sizes, and tail, plus the seed/expand
// pipeline, rather than a percentage.
func TestPlanningProgressIsHonest(t *testing.T) {
	if strings.Contains(projectOnboardingBody, `value="45"`) {
		t.Error("planning page still shows a fabricated 45% progress value")
	}
	if strings.Contains(projectOnboardingBody, `<progress class="progress" max="100"></progress>`) {
		t.Error("planning page still shows an indeterminate progress bar")
	}
	for _, want := range []string{
		"One call drafts three distinct seeds",
		"three calls expand each into a complete plan",
		"model stays pinned",
		"Raw model output",
		"Cancel and choose another direction",
	} {
		if !strings.Contains(projectOnboardingBody, want) {
			t.Errorf("planning page missing %q", want)
		}
	}
}

func TestProjectOnboardingShowsDiscoveryTransparency(t *testing.T) {
	fx := setup(t)
	sd := &slowDiscovery{started: make(chan struct{}), release: make(chan struct{})}
	fx.ui.deps.Discovery = sd
	fx.ui.startDiscovery(fx.project, projectBrief{Goal: "a daily news brief", Constraints: []string{"two hours a day"}})
	select {
	case <-sd.started:
	case <-time.After(time.Second):
		t.Fatal("discovery did not start")
	}
	_, body := get(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding")
	for _, want := range []string{
		"Discovery in progress", "What Athanor is doing", "waiting for fake-model",
		"Athanor is exploring directions inside your goal", "target 10 options",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("transparent discovery page missing %q", want)
		}
	}
	close(sd.release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if run := fx.ui.discoveryRunFor(fx.project.ID); run != nil && run.Status == "done" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("discovery run did not finish after release")
}

type slowDiscovery struct {
	fakeDiscovery
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *slowDiscovery) Generate(ctx context.Context, projectID string, req discovery.Request) (discovery.Session, error) {
	s.once.Do(func() { close(s.started) })
	req.Report(discovery.ProgressEvent{Stage: "prompt", Detail: "prompt assembled for fake-model", Bytes: 800})
	req.Report(discovery.ProgressEvent{Stage: "model", Detail: "waiting for fake-model", Bytes: 800})
	select {
	case <-s.release:
	case <-ctx.Done():
		return discovery.Session{}, ctx.Err()
	}
	return s.fakeDiscovery.Generate(ctx, projectID, req)
}

func TestCancelDiscovery(t *testing.T) {
	fx := setup(t)
	sd := &slowDiscovery{started: make(chan struct{}), release: make(chan struct{})}
	fx.ui.deps.Discovery = sd
	fx.ui.startDiscovery(fx.project, projectBrief{Goal: "a daily news brief"})
	select {
	case <-sd.started:
	case <-time.After(time.Second):
		t.Fatal("discovery did not start")
	}
	resp := postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding",
		url.Values{"action": {"cancel_discovery"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		run := fx.ui.discoveryRunFor(fx.project.ID)
		if run != nil && run.Status == "failed" {
			if !strings.Contains(run.Error, "cancelled") {
				t.Fatalf("run error = %q, want a cancellation", run.Error)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("discovery was not cancelled: %+v", run)
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, body := get(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding")
	if !strings.Contains(body, "cancelled") {
		t.Error("onboarding page did not report the cancellation")
	}
}

func TestDiscoveryProgressMapping(t *testing.T) {
	cases := []struct {
		stage string
		bytes int
		want  int
	}{
		{"starting", 0, 5},
		{"model", 0, 20},
		{"streaming", 4000, 35},
		{"response", 0, 78},
		{"salvage", 0, 88},
		{"repair", 0, 90},
		{"validate", 0, 92},
		{"done", 0, 100},
	}
	for _, c := range cases {
		got, _ := discoveryProgress(&discoveryRun{Stage: c.stage, ResponseBytes: c.bytes, StartedAt: time.Now()})
		if got != c.want {
			t.Errorf("discoveryProgress(%q, %d) = %d, want %d", c.stage, c.bytes, got, c.want)
		}
	}
}

func TestProjectOnboardingDiscoveryFlow(t *testing.T) {
	fx := setup(t)
	fd := &fakeDiscovery{}
	fx.ui.deps.Discovery = fd

	code, body := get(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding")
	if code != 200 || !strings.Contains(body, "What are we actually trying to do") {
		t.Fatalf("brief page code=%d body missing brief (%.120q)", code, body)
	}

	resp := postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding", url.Values{
		"action":      {"save"},
		"goal":        {"a daily news brief"},
		"constraints": {"two hours a day\nno ads"},
		"sources":     {"https://example.com"},
		"success":     {"a brief a reader can finish in ten minutes"},
	})
	if resp.StatusCode != 303 {
		t.Fatalf("save status = %d", resp.StatusCode)
	}
	_, body = get(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding")
	if !strings.Contains(body, "needs access") || !strings.Contains(body, "example.com") {
		t.Fatalf("research source status missing (%.200q)", body)
	}

	resp = postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding", url.Values{
		"action": {"request_access"}, "source": {"https://example.com/report"},
	})
	if resp.StatusCode != 303 {
		t.Fatalf("request access status = %d", resp.StatusCode)
	}
	reqs, err := fx.repo.List(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	foundAccess := false
	for _, req := range reqs {
		if req.Type == "network_access" && strings.Contains(req.PayloadJSON, "example.com") {
			foundAccess = true
		}
	}
	if !foundAccess {
		t.Fatal("network access request was not queued")
	}

	resp = postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding", url.Values{
		"action":      {"discover"},
		"goal":        {"a daily news brief"},
		"constraints": {"two hours a day\nno ads"},
		"sources":     {"https://example.com"},
		"success":     {"a brief a reader can finish in ten minutes"},
	})
	if resp.StatusCode != 303 {
		t.Fatalf("discover status = %d", resp.StatusCode)
	}

	deadline := time.Now().Add(3 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		_, body = get(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding")
		if strings.Contains(body, "Develop this direction") {
			found = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !found {
		t.Fatal("discovery never produced the option board")
	}
	for _, want := range []string{"Daily news brief", "CSV reconciliation CLI", "Battery-recycling digest", "Develop this direction"} {
		if !strings.Contains(body, want) {
			t.Errorf("option board missing %q", want)
		}
	}

	resp = postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding", url.Values{
		"action": {"select"}, "option": {"o1"},
	})
	if resp.StatusCode != 303 {
		t.Fatalf("select status = %d", resp.StatusCode)
	}
	_, body = get(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding")
	if !strings.Contains(body, "Approve plan and start") || !strings.Contains(body, "First iteration") {
		t.Fatalf("plan stage missing (%.200q)", body)
	}

	resp = postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding", url.Values{"action": {"approve"}})
	if resp.StatusCode != 303 {
		t.Fatalf("approve status = %d", resp.StatusCode)
	}
	tasks, err := fx.ui.deps.Projects.TasksByProject(context.Background(), fx.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) < 4 {
		t.Fatalf("approved plan created %d tasks, want at least 4", len(tasks))
	}
	if len(fx.engine.enqueued) == 0 {
		t.Error("approving the plan did not start the first job")
	}
}

// advanceToPlan runs the brief, discovery, and direction selection steps so a
// test starts at the plan stage with option o1 selected.
func advanceToPlan(t *testing.T, fx *fixtures, fd *fakeDiscovery) string {
	t.Helper()
	fx.ui.deps.Discovery = fd
	if resp := postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding", url.Values{
		"action": {"discover"}, "goal": {"a daily news brief"},
		"constraints": {"two hours a day"}, "sources": {"https://example.com"},
		"success": {"a brief a reader can finish in ten minutes"},
	}); resp.StatusCode != 303 {
		t.Fatalf("discover status = %d", resp.StatusCode)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, body := get(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding")
		if strings.Contains(body, "Develop this direction") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if resp := postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding", url.Values{
		"action": {"select"}, "option": {"o1"},
	}); resp.StatusCode != 303 {
		t.Fatalf("select status = %d", resp.StatusCode)
	}
	_, body := get(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding")
	return body
}

func TestProjectOnboardingAIPlanFlow(t *testing.T) {
	fx := setup(t)
	fd := &fakeDiscovery{}
	fp := &fakePlanning{}
	fx.ui.deps.Planning = fp

	body := advanceToPlan(t, fx, fd)
	if !strings.Contains(body, "Draft three plans") || !strings.Contains(body, "Approve plan and start") {
		t.Fatalf("plan stage missing generate/approve actions (%.300q)", body)
	}

	if resp := postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding", url.Values{
		"action": {"generate_plans"},
	}); resp.StatusCode != 303 {
		t.Fatalf("generate status = %d", resp.StatusCode)
	}
	deadline := time.Now().Add(3 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		_, body = get(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding")
		if strings.Contains(body, "Three plans") && strings.Contains(body, "Smallest verifiable slice") {
			found = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !found {
		t.Fatalf("AI plan candidates never appeared (%.300q)", body)
	}

	if resp := postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding", url.Values{
		"action": {"select_plan"}, "plan": {"1"},
	}); resp.StatusCode != 303 {
		t.Fatalf("select_plan status = %d", resp.StatusCode)
	}
	_, body = get(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding")
	if !strings.Contains(body, "Chosen plan") || !strings.Contains(body, "Deepest coverage") {
		t.Fatalf("selected plan missing (%.300q)", body)
	}

	if resp := postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding", url.Values{
		"action": {"approve"},
	}); resp.StatusCode != 303 {
		t.Fatalf("approve status = %d", resp.StatusCode)
	}
	tasks, err := fx.ui.deps.Projects.TasksByProject(context.Background(), fx.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundTask := false
	for _, task := range tasks {
		if task.Title == "Deepest coverage task" {
			foundTask = true
		}
	}
	if !foundTask {
		t.Fatalf("approved tasks do not come from the selected AI plan: %+v", tasks)
	}
}

func TestProjectOnboardingIgnoresStalePlanAfterDirectionChange(t *testing.T) {
	fx := setup(t)
	fd := &fakeDiscovery{}
	fp := &fakePlanning{}
	fx.ui.deps.Planning = fp

	body := advanceToPlan(t, fx, fd)
	if !strings.Contains(body, "Draft three plans") {
		t.Fatalf("plan stage missing (%.200q)", body)
	}
	if resp := postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding", url.Values{
		"action": {"generate_plans"},
	}); resp.StatusCode != 303 {
		t.Fatalf("generate status = %d", resp.StatusCode)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, body = get(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding")
		if strings.Contains(body, "Fastest first draft") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Change direction: reset, then choose o2. The o1 plan must disappear.
	if resp := postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding", url.Values{
		"action": {"reset"},
	}); resp.StatusCode != 303 {
		t.Fatalf("reset status = %d", resp.StatusCode)
	}
	if resp := postForm(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding", url.Values{
		"action": {"select"}, "option": {"o2"},
	}); resp.StatusCode != 303 {
		t.Fatalf("select o2 status = %d", resp.StatusCode)
	}
	_, body = get(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding")
	if strings.Contains(body, "Fastest first draft") || strings.Contains(body, "Chosen plan") {
		t.Fatalf("stale plan for the previous direction is still shown (%.300q)", body)
	}
	if !strings.Contains(body, "Draft three plans") {
		t.Fatalf("new direction did not return to the plan prompt (%.300q)", body)
	}
}

func TestWithSourceAccessKeepsCodeTools(t *testing.T) {
	got := withSourceAccess(project.CapabilityCode, nil)
	for _, want := range append(project.CodeArchetypeTools(), "fetch_url") {
		if !slices.Contains(got, want) {
			t.Errorf("code tools = %v, missing %q", got, want)
		}
	}
	text := withSourceAccess(project.CapabilityText, nil)
	if !slices.Equal(text, []string{"fetch_url"}) {
		t.Errorf("text tools = %v, want only fetch_url", text)
	}
	// A pre-existing tool stays and is not duplicated.
	got = withSourceAccess(project.CapabilityCode, []string{"fetch_url"})
	n := 0
	for _, tool := range got {
		if tool == "fetch_url" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("fetch_url count = %d, want 1: %v", n, got)
	}
}
