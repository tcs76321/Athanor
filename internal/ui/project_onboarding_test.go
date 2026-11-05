package ui

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/discovery"
	"github.com/tcs76321/athanor/internal/planning"
)

type fakeDiscovery struct {
	mu   sync.Mutex
	sess discovery.Session
	has  bool
}

func (f *fakeDiscovery) Generate(_ context.Context, projectID string, _ discovery.Request) (discovery.Session, error) {
	opts := []discovery.Option{
		{ID: "o1", Title: "Local bookkeeping", OneLiner: "Monthly close for trades", Industry: "Finance", Niche: "Trades", Customer: "Electricians", Pain: "Receipts", Wedge: "Done-for-you", BusinessModel: "Retainer", Pricing: "$300/mo", FirstOffer: "One free month", ValidationExperiment: "Five calls", TimeToFirstRevenueDays: 14, StartupCostUSD: 100, Confidence: 0.7},
		{ID: "o2", Title: "Niche newsletter", OneLiner: "Paid brief for labs", Industry: "Media", Niche: "Labs", Customer: "Lab managers", Pain: "Fragmented news", Wedge: "Curated", BusinessModel: "Subscription", Pricing: "$10/mo", FirstOffer: "First issue free", ValidationExperiment: "Landing page", TimeToFirstRevenueDays: 30, StartupCostUSD: 50, Confidence: 0.6},
		{ID: "o3", Title: "Compliance templates", OneLiner: "Checklist packs", Industry: "Healthcare", Niche: "Clinics", Customer: "Clinic owners", Pain: "Audit prep", Wedge: "Ready-made", BusinessModel: "One-off", Pricing: "$99", FirstOffer: "Sample pack", ValidationExperiment: "Ten downloads", TimeToFirstRevenueDays: 10, StartupCostUSD: 20, Confidence: 0.6},
		{ID: "o4", Title: "Recruiter notes", OneLiner: "Structured summaries", Industry: "Software", Niche: "Recruiting", Customer: "Agencies", Pain: "Manual notes", Wedge: "Templates", BusinessModel: "Subscription", Pricing: "$29/mo", FirstOffer: "Three free", ValidationExperiment: "Five demos", TimeToFirstRevenueDays: 21, StartupCostUSD: 200, Confidence: 0.5},
		{ID: "o5", Title: "Workshop booking", OneLiner: "Deposits for makers", Industry: "Education", Niche: "Crafts", Customer: "Workshops", Pain: "No-shows", Wedge: "Deposit-first", BusinessModel: "SaaS", Pricing: "$19/mo", FirstOffer: "Free setup", ValidationExperiment: "Three pilots", TimeToFirstRevenueDays: 7, StartupCostUSD: 0, Confidence: 0.7},
		{ID: "o6", Title: "Cafe supplier shortlists", OneLiner: "Independent comparisons", Industry: "Hospitality", Niche: "Cafes", Customer: "Cafe owners", Pain: "Opaque pricing", Wedge: "Paid shortlist", BusinessModel: "Service", Pricing: "$150", FirstOffer: "One category", ValidationExperiment: "Five interviews", TimeToFirstRevenueDays: 28, StartupCostUSD: 300, Confidence: 0.5},
	}
	for i := range opts {
		opts[i].Scores = map[string]int{"total": 80 - i, "clarity": 20, "evidence": 10, "speed": 15, "capital": 15, "risk": 15}
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
		Title: title, Summary: "A 30-day plan for " + title,
		Tasks: []planning.Task{
			{Title: title + " task", Description: "A concrete first step.", Capability: "document", Criteria: []string{"done"}},
			{Title: title + " verify", Description: "Verify the first step.", Capability: "document", Criteria: []string{"verified"}},
		},
		Confidence: 0.6,
	}
}

func (f *fakePlanning) Generate(_ context.Context, projectID, optionID string, _ planning.Request) (planning.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generated = true
	f.sess = planning.Session{
		ID: "ps1", ProjectID: projectID, OptionID: optionID, Status: "ready", SelectedIndex: -1,
		Candidates: []planning.Candidate{
			planCandidate("Fastest validation"),
			planCandidate("Deepest quality"),
			planCandidate("Lowest cost"),
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
		"goal":        {"make money online"},
		"constraints": {"two hours a day\nno ads"},
		"sources":     {"https://example.com"},
		"success":     {"five customer conversations"},
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
		"goal":        {"make money online"},
		"constraints": {"two hours a day\nno ads"},
		"sources":     {"https://example.com"},
		"success":     {"five customer conversations"},
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
	for _, want := range []string{"Local bookkeeping", "Niche newsletter", "Cafe supplier shortlists", "Develop this direction"} {
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
	if !strings.Contains(body, "Approve plan and start") || !strings.Contains(body, "First 30 days") {
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
		"action": {"discover"}, "goal": {"make money online"},
		"constraints": {"two hours a day"}, "sources": {"https://example.com"},
		"success": {"five customer conversations"},
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
		if strings.Contains(body, "Three plans") && strings.Contains(body, "Fastest validation") {
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
	if !strings.Contains(body, "Chosen plan") || !strings.Contains(body, "Deepest quality") {
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
		if task.Title == "Deepest quality task" {
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
		if strings.Contains(body, "Fastest validation") {
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
	if strings.Contains(body, "Fastest validation") || strings.Contains(body, "Chosen plan") {
		t.Fatalf("stale plan for the previous direction is still shown (%.300q)", body)
	}
	if !strings.Contains(body, "Draft three plans") {
		t.Fatalf("new direction did not return to the plan prompt (%.300q)", body)
	}
}
