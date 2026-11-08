package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/composition"
	"github.com/tcs76321/athanor/internal/config"
)

func waitQualification(t *testing.T, u *UI) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !u.qualificationRunning() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("qualification did not finish")
}

func TestPoolQualificationDepthsComplete(t *testing.T) {
	u := newEmptyUI(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/chat" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message":       map[string]string{"content": "A"},
				"done":          true,
				"eval_count":    10,
				"eval_duration": int64(time.Second),
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Inference.OllamaURL = ts.URL
	u.deps.Config = cfg
	u.deps.Pool.Upsert(composition.ModelInfo{
		Name: "fake:latest", Family: "fake", SizeBytes: 1,
		MaxContext: 8192, ContextTarget: 8192,
	})
	models := []string{"fake:latest"}

	for _, tier := range []string{qualTierMinimal, qualTierBalanced, qualTierComplete} {
		startedState, ok, err := u.startPoolQualification(context.Background(), false, tier)
		if err != nil {
			t.Fatalf("%s: %v", tier, err)
		}
		if !ok {
			t.Fatalf("%s: run did not start (state %+v)", tier, startedState)
		}
		waitQualification(t, u)
		state := u.loadQualState(context.Background())
		if state.Running {
			t.Fatalf("%s: still marked running", tier)
		}
		if state.Tier != tier {
			t.Fatalf("%s: state tier = %q", tier, state.Tier)
		}
		_, plan := tierPlan(tier)
		want := len(plan) * len(models)
		view := qualificationViewForPool(state, models, tier, true)
		if view.Total != want || view.Done != want {
			t.Fatalf("%s: view progress = %d/%d, want %d/%d", tier, view.Done, view.Total, want, want)
		}
		if !view.AllCases || !view.Complete || view.Percent != 100 {
			t.Fatalf("%s: view = %+v, want a completed screen", tier, view)
		}
		if !view.CoreComplete {
			t.Fatalf("%s: Minimal coverage missing after completion", tier)
		}
		if tier == qualTierComplete && !state.Deep {
			t.Fatal("complete screen did not use the deep ladder")
		}
		if tier == qualTierBalanced && state.Deep {
			t.Fatal("balanced screen used the deep ladder")
		}
		if tier == qualTierMinimal {
			prof, ok := u.pool().Profile("fake:latest")
			if !ok || prof.TokensPerSecond <= 0 {
				t.Fatalf("speed was not recorded: %+v", prof)
			}
		}
	}
}
