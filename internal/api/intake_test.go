package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/discovery"
	"github.com/tcs76321/athanor/internal/intake"
	"github.com/tcs76321/athanor/internal/planning"
	"github.com/tcs76321/athanor/internal/project"
)

type apiFakeDiscovery struct {
	mu   sync.Mutex
	sess discovery.Session
}

func (f *apiFakeDiscovery) Generate(_ context.Context, projectID string, _ discovery.Request) (discovery.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sess = discovery.Session{ID: "s1", ProjectID: projectID, Status: "ready", Options: []discovery.Option{{
		ID: "o1", Title: "Daily brief", Outcome: "One coherent update",
		Method: "Group by consequence", Check: "Five readers review it",
	}}}
	return f.sess, nil
}

func (f *apiFakeDiscovery) Get(_ context.Context, _ string) (discovery.Session, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sess, f.sess.ID != "", nil
}

func (f *apiFakeDiscovery) Select(_ context.Context, _, optionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sess.SelectedID = optionID
	return nil
}

type apiFakePlanning struct {
	mu   sync.Mutex
	sess planning.Session
}

func (f *apiFakePlanning) Generate(_ context.Context, projectID, optionID string, _ planning.Request) (planning.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sess = planning.Session{ID: "ps1", ProjectID: projectID, OptionID: optionID, Status: "ready",
		Candidates: []planning.Candidate{{ID: "c1", Title: "Plan", Summary: "A plan.", Status: "ready",
			Tasks: []planning.Task{
				{Key: "a", Title: "A", Description: "Do A.", Capability: "document", Criteria: []string{"A exists"}},
				{Key: "b", Title: "B", Description: "Do B.", Capability: "document", Criteria: []string{"B exists"}, DependsOn: []string{"a"}},
			}}}}
	return f.sess, nil
}

func (f *apiFakePlanning) GenerateSingle(ctx context.Context, projectID, optionID string, req planning.Request) (planning.Session, error) {
	return f.Generate(ctx, projectID, optionID, req)
}

func (f *apiFakePlanning) Get(_ context.Context, _ string) (planning.Session, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sess, f.sess.ID != "", nil
}

func (f *apiFakePlanning) Select(_ context.Context, _ string, index int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sess.SelectedIndex = index
	return nil
}

func (f *apiFakePlanning) SelectByID(_ context.Context, _, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.sess.Candidates {
		if f.sess.Candidates[i].ID == id {
			f.sess.SelectedIndex = i
		}
	}
	return nil
}

func waitRun(t *testing.T, svc *intake.Service, projectID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if run := svc.RunFor(projectID); run == nil || run.Status != "running" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("direction run did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestIntakeAPIFullFlow(t *testing.T) {
	h := newHarness(t)
	disc := &apiFakeDiscovery{}
	plan := &apiFakePlanning{}
	svc := intake.New(intake.Deps{
		Projects: h.projects, Jobs: h.jobs, Events: h.st,
		Discovery: disc, Planning: plan,
	})
	h.api.SetIntake(svc)
	p, err := h.projects.CreateGuided(context.Background(), project.Intent{
		Goal: "Help me figure out what small service to build.", OnRamp: project.OnRampExplore,
	})
	if err != nil {
		t.Fatal(err)
	}

	if rec := doJSON(t, http.MethodPost, h.ts.URL+"/projects/"+p.ID+"/intake/onramp", map[string]string{"onramp": "explore"}); rec.Code != http.StatusOK {
		t.Fatalf("onramp = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, http.MethodPost, h.ts.URL+"/projects/"+p.ID+"/directions", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("directions = %d: %s", rec.Code, rec.Body.String())
	}
	waitRun(t, svc, p.ID)
	if rec := doJSON(t, http.MethodPost, h.ts.URL+"/projects/"+p.ID+"/directions/o1/select", nil); rec.Code != http.StatusOK {
		t.Fatalf("select direction = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, http.MethodPost, h.ts.URL+"/projects/"+p.ID+"/plans", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("plans = %d: %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if run := svc.PlanRunFor(p.ID); run != nil && run.Status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("plan run did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if rec := doJSON(t, http.MethodPost, h.ts.URL+"/projects/"+p.ID+"/plans/c1/select", nil); rec.Code != http.StatusOK {
		t.Fatalf("select plan = %d: %s", rec.Code, rec.Body.String())
	}
	rec := doJSON(t, http.MethodPost, h.ts.URL+"/projects/"+p.ID+"/approve", map[string]string{"plan": "c1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("approve = %d: %s", rec.Code, rec.Body.String())
	}
	got, err := h.projects.Get(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ApprovedPlanID != "c1" {
		t.Fatalf("approved plan = %q", got.ApprovedPlanID)
	}
}

func TestIntakeStateAndIntentRoutes(t *testing.T) {
	h := newHarness(t)
	svc := intake.New(intake.Deps{
		Projects: h.projects, Jobs: h.jobs, Events: h.st,
	})
	h.api.SetIntake(svc)
	p, err := h.projects.CreateGuided(context.Background(), project.Intent{
		Goal: "Write a short essay about local-first software.",
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := doJSON(t, http.MethodGet, h.ts.URL+"/projects/"+p.ID+"/intake", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("intake state = %d: %s", resp.Code, resp.Body.String())
	}
	var st intake.State
	if err := json.Unmarshal(resp.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Stage != intake.StageIntent {
		t.Fatalf("stage = %q, want intent", st.Stage)
	}

	resp = doJSON(t, http.MethodPatch, h.ts.URL+"/projects/"+p.ID+"/intent", map[string]any{
		"goal":       "Write a longer weekly essay about local-first software.",
		"boundaries": "No external publishing.",
		"sources":    []string{"https://example.com/feed"},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("intent patch = %d: %s", resp.Code, resp.Body.String())
	}
	got, err := h.projects.Get(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Goal, "weekly essay") || got.Boundaries != "No external publishing." {
		t.Fatalf("project = %+v", got)
	}
}

func doJSON(t *testing.T, method, url string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body == nil {
		rdr = strings.NewReader("")
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	// The harness server is registered directly; route through it.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = rec.Body.ReadFrom(resp.Body)
	rec.Code = resp.StatusCode
	return rec
}
