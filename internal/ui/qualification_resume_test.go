package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/composition"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/onboard"
)

// qualFakeOllama serves a passing screen. The optional fail flag makes every
// chat call a 500 so an infrastructure error can be distinguished from a
// wrong answer.
func qualFakeOllama(t *testing.T, fail *atomic.Bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{{
					"name": "fake:latest", "size": 1,
					"details": map[string]string{"family": "fake"},
				}},
			})
		case "/api/chat":
			if fail != nil && fail.Load() {
				http.Error(w, "model is loading", http.StatusInternalServerError)
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
				"message":       map[string]string{"content": answer},
				"done":          true,
				"eval_count":    10,
				"eval_duration": int64(time.Second),
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func qualTestUI(t *testing.T, url string) *UI {
	t.Helper()
	u := newEmptyUI(t)
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Inference.OllamaURL = url
	u.deps.Config = cfg
	u.deps.Pool.Upsert(composition.ModelInfo{
		Name: "fake:latest", Family: "fake", SizeBytes: 1,
		MaxContext: 8192, ContextTarget: 8192,
	})
	return u
}

// A Core restart clears the persisted running flag. Opening the qualification
// step must resume the run the operator already chose, not silently stop it.
func TestWelcomeResumesInterruptedQualification(t *testing.T) {
	ts := qualFakeOllama(t, nil)
	defer ts.Close()
	u := qualTestUI(t, ts.URL)

	state := newQualState()
	state.StartedAt = time.Now().Add(-time.Minute)
	state.FinishedAt = time.Time{}
	state.Tier = qualTierMinimal
	state.Total = 1
	state.Models = []string{"fake:latest"}
	state.Results["generator|fake:latest|gen-arith"] = true
	u.saveQualState(context.Background(), state)

	mux := http.NewServeMux()
	u.Register(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/ui/welcome?step=qualify")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	waitQualification(t, u)
	after := u.loadQualState(context.Background())
	_, plan := tierPlan(qualTierMinimal)
	if want := len(plan); after.Done != want || after.Total != want {
		t.Fatalf("resumed run = %d/%d, want %d/%d (state %+v)", after.Done, after.Total, want, want, after)
	}
	if _, ok := after.Results["generator|fake:latest|gen-arith"]; !ok {
		t.Fatal("resume discarded the completed case")
	}
}

// A fresh install still waits for the operator to choose a depth: opening the
// step must not start work on its own.
func TestWelcomeDoesNotStartFreshQualification(t *testing.T) {
	ts := qualFakeOllama(t, nil)
	defer ts.Close()
	u := qualTestUI(t, ts.URL)

	mux := http.NewServeMux()
	u.Register(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/ui/welcome?step=qualify")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	state := u.loadQualState(context.Background())
	if state.Running || !state.StartedAt.IsZero() || len(state.Results) != 0 {
		t.Fatalf("fresh step started a run: %+v", state)
	}
}

// A transport failure is not a wrong answer. It must leave the case pending,
// stop the run with a visible error, and allow a retry to complete it.
func TestQualificationTransportErrorLeavesCasePending(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	ts := qualFakeOllama(t, &fail)
	defer ts.Close()
	u := qualTestUI(t, ts.URL)

	if _, ok, err := u.startPoolQualification(context.Background(), false, qualTierMinimal); err != nil || !ok {
		t.Fatalf("start = %v, ok=%v", err, ok)
	}
	waitQualification(t, u)

	after := u.loadQualState(context.Background())
	if after.Error == "" {
		t.Fatal("transport failure did not surface an error")
	}
	if len(after.Results) != 0 {
		t.Fatalf("transport failure was recorded as case results: %+v", after.Results)
	}
	if after.Running {
		t.Fatal("failed run is still marked running")
	}

	fail.Store(false)
	if _, ok, err := u.startPoolQualification(context.Background(), false, qualTierMinimal); err != nil || !ok {
		t.Fatalf("retry = %v, ok=%v", err, ok)
	}
	waitQualification(t, u)
	done := u.loadQualState(context.Background())
	_, plan := tierPlan(qualTierMinimal)
	if done.Done != len(plan) {
		t.Fatalf("retry = %d/%d, want %d", done.Done, done.Total, len(plan))
	}
	if done.Error != "" {
		t.Fatalf("retry left error: %q", done.Error)
	}
}

// A long Complete screen is not "stale". Starting it again must keep every
// completed case instead of resetting the state.
func TestLongQualificationIsNotReset(t *testing.T) {
	ts := qualFakeOllama(t, nil)
	defer ts.Close()
	u := qualTestUI(t, ts.URL)

	state := newQualState()
	state.StartedAt = time.Now().Add(-3 * time.Hour)
	state.Tier = qualTierComplete
	state.Models = []string{"fake:latest"}
	state.Results["generator|fake:latest|gen-arith"] = true
	u.saveQualState(context.Background(), state)

	if _, ok, err := u.startPoolQualification(context.Background(), false, qualTierComplete); err != nil || !ok {
		t.Fatalf("start = %v, ok=%v", err, ok)
	}
	u.cancelQualification()
	waitQualification(t, u)

	after := u.loadQualState(context.Background())
	if !after.Results["generator|fake:latest|gen-arith"] {
		t.Fatal("a long run was treated as stale and its results were discarded")
	}
}

// Stopping and immediately choosing a depth waits for the cancelled worker
// instead of being refused as "already running".
func TestStopThenStartWaitsForTheWorker(t *testing.T) {
	u := newEmptyUI(t)
	done := make(chan struct{})
	u.qualMu.Lock()
	u.qualRunning = true
	u.qualStopping = true
	u.qualDone = done
	u.qualMu.Unlock()

	go func() {
		time.Sleep(20 * time.Millisecond)
		close(done)
	}()
	start := time.Now()
	if err := u.waitForQualificationStop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 10*time.Millisecond {
		t.Fatal("did not wait for the stopping worker")
	}
}

func TestRetainQualModelsDropsRemovedModels(t *testing.T) {
	state := newQualState()
	state.Results["generator|keep|gen-arith"] = true
	state.Results["generator|drop|gen-arith"] = true
	state.Details["generator|keep|gen-arith"] = qualCaseRecord{Model: "keep", CaseID: "gen-arith"}
	state.Details["generator|drop|gen-arith"] = qualCaseRecord{Model: "drop", CaseID: "gen-arith"}

	out := retainQualModels(state, []string{"keep"})
	if _, ok := out.Results["generator|keep|gen-arith"]; !ok {
		t.Fatal("kept model result was dropped")
	}
	if _, ok := out.Results["generator|drop|gen-arith"]; ok {
		t.Fatal("removed model result was retained")
	}
	if _, ok := out.Details["generator|drop|gen-arith"]; ok {
		t.Fatal("removed model detail was retained")
	}
}

func TestQualificationPlanIsNested(t *testing.T) {
	defaultLadder := onboard.DefaultLadder()
	minimal := onboard.QualificationMinimal(defaultLadder)
	balanced := onboard.AllCaseRefs(defaultLadder)
	complete := onboard.AllCaseRefs(onboard.DeepLadder())
	if len(minimal) >= len(balanced) || len(balanced) >= len(complete) {
		t.Fatalf("plans are not strictly nested: %d/%d/%d", len(minimal), len(balanced), len(complete))
	}
	balancedKeys := map[string]bool{}
	for _, ref := range balanced {
		balancedKeys[ref.Kind+"|"+ref.ID] = true
	}
	for _, ref := range minimal {
		if !balancedKeys[ref.Kind+"|"+ref.ID] {
			t.Fatalf("minimal case %s missing from balanced", ref.ID)
		}
	}
	completeKeys := map[string]bool{}
	for _, ref := range complete {
		completeKeys[ref.Kind+"|"+ref.ID] = true
	}
	for _, ref := range balanced {
		if !completeKeys[ref.Kind+"|"+ref.ID] {
			t.Fatalf("balanced case %s missing from complete", ref.ID)
		}
	}
}
