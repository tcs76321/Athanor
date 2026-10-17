package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/strategy"
)

type fakeStrategy struct {
	insights     []strategy.Insight
	mined        []strategy.Insight
	err          error
	status       string
	lastOpts     strategy.MineOptions
	activatedIDs []string
}

func (f *fakeStrategy) Mine(_ context.Context, opts strategy.MineOptions) ([]strategy.Insight, error) {
	f.lastOpts = opts
	return f.mined, f.err
}

func (f *fakeStrategy) ListInsights(_ context.Context, _ string) ([]strategy.Insight, error) {
	return f.insights, f.err
}

func (f *fakeStrategy) SetInsightStatus(_ context.Context, _, status string) error {
	f.status = status
	return f.err
}

func (f *fakeStrategy) GetInsight(_ context.Context, _ string) (strategy.Insight, error) {
	return f.insights[0], f.err
}

func (f *fakeStrategy) CreateInsight(_ context.Context, in strategy.Insight) (strategy.Insight, error) {
	f.insights = append(f.insights, in)
	return in, f.err
}

func (f *fakeStrategy) ActivateWinningTechniques(_ context.Context, _ strategy.AutoActivateOptions) ([]string, error) {
	return f.activatedIDs, f.err
}

type fakePromoter struct {
	activated bool
	requestID string
	err       error
}

func (f *fakePromoter) Promote(_ context.Context, _ string) (bool, string, error) {
	return f.activated, f.requestID, f.err
}

func TestInsightCreate(t *testing.T) {
	h := newHarness(t)
	t.Cleanup(h.ts.Close)
	fake := &fakeStrategy{}
	h.api.SetStrategy(fake)

	body := `{"statement": "Validate every field before converting.", "scope": "global"}`
	resp, err := http.Post(h.ts.URL+"/strategy/insights", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	if len(fake.insights) != 1 {
		t.Fatalf("created insights = %d, want 1", len(fake.insights))
	}
	got := fake.insights[0]
	if got.Status != strategy.InsightActive || got.Scope != strategy.ScopeGlobal ||
		got.Statement != "Validate every field before converting." {
		t.Errorf("created insight = %+v", got)
	}
	if got.Pattern.Feature != "manual.memory" {
		t.Errorf("pattern = %+v, want manual.memory", got.Pattern)
	}

	empty, err := http.Post(h.ts.URL+"/strategy/insights", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = empty.Body.Close() }()
	if empty.StatusCode != http.StatusBadRequest {
		t.Errorf("empty statement status = %d, want 400", empty.StatusCode)
	}
}

func TestStrategyRoutes(t *testing.T) {
	h := newHarness(t)
	ins := strategy.Insight{
		ID: "i1", Scope: strategy.ScopeGlobal, Polarity: strategy.PolarityWinning, Status: strategy.InsightProposed,
		Pattern:   strategy.Pattern{Feature: "diverging.persona", Value: "alternative", Context: "archetype=code"},
		Evidence:  strategy.Evidence{CohortJobs: 20, AcceptRate: 0.8, BaselineAcceptRate: 0.6},
		Statement: "alternative wins on code tasks",
	}
	fake := &fakeStrategy{insights: []strategy.Insight{ins}, mined: []strategy.Insight{ins}, activatedIDs: []string{"i1"}}
	h.api.SetStrategy(fake)
	h.api.SetStrategyPromoter(&fakePromoter{activated: false, requestID: "r1"})
	h.api.SetStrategyAutoActivate(true, strategy.AutoActivateOptions{MinCohortSize: 10, MinAcceptRateDelta: 0.2, MaxActiveInsights: 5})

	// List.
	resp, err := http.Get(h.ts.URL + "/strategy/insights?status=proposed")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", resp.StatusCode)
	}
	var list struct {
		Insights []insightResponse `json:"insights"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Insights) != 1 || list.Insights[0].Feature != "diverging.persona" {
		t.Fatalf("list = %+v", list)
	}

	// Mine applies defaults when the body omits them.
	mr, err := http.Post(h.ts.URL+"/strategy/mine", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mr.Body.Close() }()
	if mr.StatusCode != http.StatusCreated {
		t.Fatalf("mine status = %d, want 201", mr.StatusCode)
	}
	if fake.lastOpts.MinCohortSize != 20 || fake.lastOpts.MinAcceptRateDelta != 0.15 {
		t.Errorf("mine opts = %+v, want defaults", fake.lastOpts)
	}
	// M8-T28: grounded winning techniques auto-activate; the count is reported.
	var mineBody struct {
		Activated int `json:"activated"`
	}
	if err := json.NewDecoder(mr.Body).Decode(&mineBody); err != nil {
		t.Fatal(err)
	}
	if mineBody.Activated != 1 {
		t.Errorf("mine activated = %d, want 1", mineBody.Activated)
	}

	// Promote files a HITL request (not activated, request id returned).
	pr, err := http.Post(h.ts.URL+"/strategy/insights/i1/promote", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pr.Body.Close() }()
	var prBody struct {
		Activated bool   `json:"activated"`
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(pr.Body).Decode(&prBody); err != nil {
		t.Fatal(err)
	}
	if prBody.Activated || prBody.RequestID != "r1" {
		t.Fatalf("promote body = %+v", prBody)
	}

	// Mute sets the status directly.
	mur, err := http.Post(h.ts.URL+"/strategy/insights/i1/mute", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = mur.Body.Close()
	if mur.StatusCode != http.StatusOK || fake.status != strategy.InsightMuted {
		t.Fatalf("mute = %d status %q", mur.StatusCode, fake.status)
	}
}

func TestStrategyRoutesUnwired(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/strategy/insights", http.StatusServiceUnavailable},
		{"POST", "/strategy/mine", http.StatusServiceUnavailable},
		{"POST", "/strategy/insights/i1/promote", http.StatusServiceUnavailable},
		{"POST", "/strategy/insights/i1/mute", http.StatusServiceUnavailable},
	} {
		req, err := http.NewRequest(tc.method, h.ts.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
		}
	}
}
