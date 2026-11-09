package ui

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/tcs76321/athanor/internal/discovery"
	"github.com/tcs76321/athanor/internal/intake"
	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/planning"
	"github.com/tcs76321/athanor/internal/project"
)

type fakeDiscovery struct {
	mu   sync.Mutex
	sess discovery.Session
	has  bool
}

func (f *fakeDiscovery) Generate(_ context.Context, projectID string, _ discovery.Request) (discovery.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.has = true
	return f.sess, nil
}

func (f *fakeDiscovery) Get(_ context.Context, _ string) (discovery.Session, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sess, f.has, nil
}

func (f *fakeDiscovery) Select(_ context.Context, _, optionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sess.SelectedID = optionID
	return nil
}

func (f *fakeDiscovery) Reset(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.has = false
	return nil
}

type fakePlanning struct {
	mu   sync.Mutex
	sess planning.Session
	has  bool
}

func (f *fakePlanning) Generate(_ context.Context, _, _ string, _ planning.Request) (planning.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.has = true
	return f.sess, nil
}

func (f *fakePlanning) GenerateSingle(_ context.Context, _, _ string, _ planning.Request) (planning.Session, error) {
	return f.Generate(context.Background(), "", "", planning.Request{})
}

func (f *fakePlanning) Get(_ context.Context, _ string) (planning.Session, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sess, f.has, nil
}

func (f *fakePlanning) Select(_ context.Context, _ string, index int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sess.SelectedIndex = index
	return nil
}

func (f *fakePlanning) SelectByID(_ context.Context, _, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.sess.Candidates {
		if f.sess.Candidates[i].ID == id {
			f.sess.SelectedIndex = i
			return nil
		}
	}
	return nil
}

// setupIntake builds a UI wired to the guided-intake service with a project
// that has a selected direction and one ready plan.
func setupIntake(t *testing.T) *fixtures {
	t.Helper()
	fx := setup(t)
	p, err := fx.ui.deps.Projects.CreateGuided(context.Background(), project.Intent{
		Goal: "Write a daily news brief from approved sources.", OnRamp: project.OnRampExplore,
	})
	if err != nil {
		t.Fatal(err)
	}
	disc := &fakeDiscovery{has: true, sess: discovery.Session{
		ID: "s1", ProjectID: p.ID, Status: "ready", SelectedID: "o1",
		Options: []discovery.Option{{
			ID: "o1", Title: "Daily brief",
			Outcome: "One coherent daily update", Method: "Group by consequence",
			Check: "Five readers review it",
		}},
	}}
	plan := &fakePlanning{has: true, sess: planning.Session{
		ID: "ps1", ProjectID: p.ID, OptionID: "o1", Status: "ready", SelectedIndex: 0,
		Candidates: []planning.Candidate{{
			ID: "c1", Title: "Smallest slice", Summary: "A short daily brief.",
			Status: "ready",
			Tasks: []planning.Task{
				{Key: "baseline", Title: "Baseline", Description: "Read the sources.", Capability: "document", Criteria: []string{"baseline recorded"}},
				{Key: "write", Title: "Write", Description: "Write the brief.", Capability: "document", Criteria: []string{"brief exists"}, DependsOn: []string{"baseline"}},
			},
		}},
	}}
	svc := intake.New(intake.Deps{
		Projects: fx.ui.deps.Projects, Jobs: job.NewRepository(fx.ui.deps.Store),
		Discovery: disc, Planning: plan, Events: fx.ui.deps.Store,
	})
	fx.ui.deps.Intake = svc
	fx.project = p
	return fx
}

func postFormNoRedirect(t *testing.T, url string, form url.Values) *http.Response {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(url, form)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestNewProjectRequiresOnlyGoal(t *testing.T) {
	fx := setup(t)
	form := url.Values{"goal": {"Compile a daily news brief from approved sources."}}
	resp := postFormNoRedirect(t, fx.ts.URL+"/ui/projects", form)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 303: %s", resp.StatusCode, b)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/onboarding") {
		t.Fatalf("redirect = %q, want onboarding", loc)
	}
}

func TestOnboardingIntentRendersOneGoalField(t *testing.T) {
	fx := setup(t)
	p, err := fx.ui.deps.Projects.CreateGuided(context.Background(), project.Intent{
		Goal: "Write a short essay about local-first software.",
	})
	if err != nil {
		t.Fatal(err)
	}
	fx.ui.deps.Intake = intake.New(intake.Deps{
		Projects: fx.ui.deps.Projects, Jobs: job.NewRepository(fx.ui.deps.Store),
		Discovery: &fakeDiscovery{}, Planning: &fakePlanning{}, Events: fx.ui.deps.Store,
	})
	resp, err := http.Get(fx.ts.URL + "/ui/projects/" + p.ID + "/onboarding")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "Acceptance criteria") {
		t.Fatal("new intake still renders an acceptance-criteria field")
	}
	if !strings.Contains(string(body), "Goal") || !strings.Contains(string(body), "Limits") {
		t.Fatal("intake page is missing the goal/limits surface")
	}
}

func TestOnboardingPlanStageRendersSelectedPlan(t *testing.T) {
	fx := setupIntake(t)
	resp, err := http.Get(fx.ts.URL + "/ui/projects/" + fx.project.ID + "/onboarding")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"Chosen plan", "Approve plan and start", "Smallest slice"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("plan page missing %q", want)
		}
	}
}

func TestApproveSelectedPlanCreatesGraph(t *testing.T) {
	fx := setupIntake(t)
	form := url.Values{"action": {"approve"}, "candidate": {"c1"}}
	resp := postFormNoRedirect(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/onboarding", form)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 303: %s", resp.StatusCode, b)
	}
	p, err := fx.ui.deps.Projects.Get(context.Background(), fx.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.ApprovedPlanID != "c1" {
		t.Fatalf("approved plan = %q, want c1", p.ApprovedPlanID)
	}
	tasks, err := fx.ui.deps.Projects.TasksByProject(context.Background(), fx.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks = %d, want the plan's 2 tasks", len(tasks))
	}
}

func TestSettingsIntentUpdatesProject(t *testing.T) {
	fx := setupIntake(t)
	form := url.Values{
		"goal":       {"Write a weekly brief instead of a daily one."},
		"boundaries": {"No external publishing."},
		"sources":    {"https://example.com/feed"},
	}
	resp := postFormNoRedirect(t, fx.ts.URL+"/ui/projects/"+fx.project.ID+"/intent", form)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 303: %s", resp.StatusCode, b)
	}
	p, err := fx.ui.deps.Projects.Get(context.Background(), fx.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Goal != "Write a weekly brief instead of a daily one." {
		t.Fatalf("goal = %q", p.Goal)
	}
	if p.Boundaries != "No external publishing." {
		t.Fatalf("boundaries = %q", p.Boundaries)
	}
	if len(p.Sources) != 1 {
		t.Fatalf("sources = %v", p.Sources)
	}
}
