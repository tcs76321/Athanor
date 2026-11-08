package ui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/composition"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/doctor"
	"github.com/tcs76321/athanor/internal/onboard"
	"github.com/tcs76321/athanor/internal/store"
	"github.com/tcs76321/athanor/migrations"
)

func newEmptyUI(t *testing.T) *UI {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	return New(Deps{Store: s, Pool: composition.New(s), Runtime: "Core Pod", Version: "test"})
}

func TestFirstRunRedirectsToWelcome(t *testing.T) {
	u := newEmptyUI(t)
	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp, err := noRedirectClient.Get(ts.URL + "/ui")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("fresh dashboard status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/ui/welcome" {
		t.Fatalf("redirect = %q, want /ui/welcome", loc)
	}
}

func TestWelcomeRendersBareShell(t *testing.T) {
	u := newEmptyUI(t)
	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/ui/welcome")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		"Welcome to Athanor", "the alchemical LLM furnace",
		"Environment", "Model pool", "Qualification", "Preferences", "Ready",
		"Start setup",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("welcome missing %q", want)
		}
	}
	if strings.Contains(body, `<div class="app">`) {
		t.Error("welcome page rendered the full app shell instead of the bare wizard")
	}
}

func TestOnboardingAdvancesAndCompletes(t *testing.T) {
	u := newEmptyUI(t)
	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp := postForm(t, ts.URL+"/ui/welcome", url.Values{
		"step": {"welcome"}, "action": {"next"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("next status = %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/ui/welcome?step=resources" {
		t.Fatalf("next location = %q", loc)
	}
	if got := u.loadOnboarding(context.Background()).Step; got != "resources" {
		t.Fatalf("persisted step = %q, want resources", got)
	}

	resp = postForm(t, ts.URL+"/ui/welcome", url.Values{
		"step": {"ready"}, "action": {"complete"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("complete status = %d", resp.StatusCode)
	}
	st := u.loadOnboarding(context.Background())
	if !st.Completed {
		t.Fatal("onboarding was not marked complete")
	}
	// A completed wizard redirects /ui/welcome back to the dashboard.
	resp, err := noRedirectClient.Get(ts.URL + "/ui/welcome")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/ui" {
		t.Fatalf("completed welcome = %d %q, want 303 /ui", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestQualificationViewPercentAndEstimate(t *testing.T) {
	started := time.Now().Add(-10 * time.Second)
	v := qualificationViewFor(qualState{
		Running: true, StartedAt: started, Total: 10, Done: 3,
		Results: map[string]bool{"a": true, "b": true, "c": true},
	}, false)
	if v.Percent != 30 || v.Done != 3 || v.Total != 10 {
		t.Errorf("view = %+v, want 30%% (3/10)", v)
	}
	if v.Remaining == "" {
		t.Error("running view has no remaining estimate")
	}
	if v.Complete {
		t.Error("running view should not be complete")
	}
}

func TestWelcomeQualificationShowsInlineProgress(t *testing.T) {
	u := newEmptyUI(t)
	state := newQualState()
	state.Running = true
	state.Phase = "core"
	state.StartedAt = time.Now().Add(-8 * time.Second)
	state.Total = 10
	state.Done = 4
	for i := 0; i < 4; i++ {
		state.Results[qualCaseKey("main", "m", string(rune('a'+i)))] = true
	}
	u.saveQualState(context.Background(), state)

	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ui/welcome?step=qualify")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	for _, want := range []string{
		"Measuring the pool", "40%", "4 / 10", `data-live-card="welcome-qualification"`,
		"Continue while it runs",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("qualification step missing %q", want)
		}
	}
	if strings.Contains(body, "Open setup and qualification") {
		t.Error("qualification step still sends the operator out to Setup")
	}
}

func TestWelcomeQualificationStartsInline(t *testing.T) {
	u := newEmptyUI(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{{
					"name": "fake:latest", "size": 1234,
					"details": map[string]string{"family": "fake"},
				}},
			})
		case "/api/chat":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]string{"content": "A"},
				"done":    true,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Inference.OllamaURL = ts.URL
	cfg.Personas.Security.Model = "fake:latest"
	cfg.Personas.Main.Model = "fake:latest"
	u.deps.Config = cfg

	mux := http.NewServeMux()
	u.Register(mux)
	uiTS := httptest.NewServer(mux)
	defer uiTS.Close()

	resp := postForm(t, uiTS.URL+"/ui/welcome", url.Values{
		"step": {"qualify"}, "action": {"run_qualify"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("start status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/ui/welcome?step=qualify" {
		t.Fatalf("start location = %q, want the wizard step (not Setup)", loc)
	}
	state := u.loadQualState(context.Background())
	if state.Total == 0 {
		t.Errorf("state = %+v, want a persisted qualification plan", state)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		u.qualMu.Lock()
		running := u.qualRunning
		u.qualMu.Unlock()
		if !running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWelcomeQualificationStartsOnChoice(t *testing.T) {
	u := newEmptyUI(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{{
					"name": "fake:latest", "size": 1234,
					"details": map[string]string{"family": "fake"},
				}},
			})
		case "/api/chat":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]string{"content": "A"},
				"done":    true,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Inference.OllamaURL = ts.URL
	cfg.Personas.Security.Model = "fake:latest"
	cfg.Personas.Main.Model = "fake:latest"
	u.deps.Config = cfg

	mux := http.NewServeMux()
	u.Register(mux)
	uiTS := httptest.NewServer(mux)
	defer uiTS.Close()

	resp, err := http.Get(uiTS.URL + "/ui/welcome?step=qualify")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	for _, want := range []string{`name="tier" value="minimal"`, `name="tier" value="balanced"`, `name="tier" value="complete"`, "Minimal", "Balanced", "Complete"} {
		if !strings.Contains(body, want) {
			t.Errorf("qualification step missing %q", want)
		}
	}
	if strings.Contains(body, "Measuring the pool") {
		t.Error("server started the run before a depth was chosen")
	}
	if strings.Contains(body, "qualification-auto-start") {
		t.Error("step still contains a hidden auto-start form")
	}
	for _, unwanted := range []string{"Minimum run time", `name="minutes"`, `name="deep"`, "Run minimal core", "Run deep core"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("qualification step still shows the old control %q", unwanted)
		}
	}
	state := u.loadQualState(context.Background())
	if state.Running || !state.StartedAt.IsZero() {
		t.Fatalf("state = %+v, want no run before a depth is chosen", state)
	}
	start := postForm(t, uiTS.URL+"/ui/welcome", url.Values{
		"step": {"qualify"}, "action": {"set_qual_tier"}, "tier": {qualTierBalanced},
	})
	if start.StatusCode != http.StatusSeeOther {
		t.Fatalf("start status = %d, want 303", start.StatusCode)
	}
	state = u.loadQualState(context.Background())
	if state.Total == 0 || !state.Running {
		t.Fatalf("state = %+v, want a started qualification plan", state)
	}
	u.cancelQualification()
}

func TestWelcomeQualificationOffersSimpleChoices(t *testing.T) {
	u := newEmptyUI(t)
	state := newQualState()
	state.CoreComplete = true
	state.Tier = qualTierMinimal
	state.Phase = "core"
	state.StartedAt = time.Now().Add(-2 * time.Minute)
	state.FinishedAt = time.Now()
	state.Total = 40
	state.Done = 20
	u.saveQualState(context.Background(), state)

	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ui/welcome?step=qualify")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	for _, want := range []string{"Minimal screen complete", "Minimal", "Balanced", "Complete", "Continue"} {
		if !strings.Contains(body, want) {
			t.Errorf("completed qualification step missing %q", want)
		}
	}
	for _, unwanted := range []string{"Minimum run time", "Run minimal core", "Run deep core", `name="deep"`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("completed qualification step still shows %q", unwanted)
		}
	}
}

func TestRestartReconcilesInterruptedQualification(t *testing.T) {
	u := newEmptyUI(t)
	state := newQualState()
	state.Running = true
	state.StartedAt = time.Now().Add(-time.Minute)
	state.Total = 20
	state.Done = 4
	state.Models = []string{"fake:latest"}
	state.Results["generator|fake:latest|gen-arith"] = true
	u.saveQualState(context.Background(), state)

	restarted := New(Deps{Store: u.deps.Store, Pool: composition.New(u.deps.Store), Runtime: "Core Pod", Version: "test"})
	after := restarted.loadQualState(context.Background())
	if after.Running {
		t.Fatal("restart left the qualification marked running")
	}
	if !after.FinishedAt.IsZero() {
		t.Fatal("restart marked the interrupted run as an operator stop")
	}
	if _, ok := after.Results["generator|fake:latest|gen-arith"]; !ok {
		t.Fatal("restart discarded completed cases")
	}
}

func TestInterruptedQualificationResumesOnStepFour(t *testing.T) {
	u := newEmptyUI(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/chat" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]string{"content": "A"},
				"done":    true,
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

	state := newQualState()
	state.StartedAt = time.Now().Add(-time.Minute)
	state.Total = 20
	state.Done = 1
	state.Models = []string{"fake:latest"}
	state.Results["generator|fake:latest|gen-arith"] = true
	u.saveQualState(context.Background(), state)

	if err := u.setQualificationTier(context.Background(), qualTierMinimal); err != nil {
		t.Fatal(err)
	}
	after := u.loadQualState(context.Background())
	if !after.Running || after.Total == 0 {
		t.Fatalf("state = %+v, want a resumed run", after)
	}
	u.cancelQualification()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		u.qualMu.Lock()
		running := u.qualRunning
		u.qualMu.Unlock()
		if !running {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTierChangeWhileRunningIsAppliedLive(t *testing.T) {
	u := newEmptyUI(t)
	u.qualMu.Lock()
	u.qualRunning = true
	u.qualTier = qualTierMinimal
	u.qualMu.Unlock()
	t.Cleanup(func() {
		u.qualMu.Lock()
		u.qualRunning = false
		u.qualTier = ""
		u.qualMu.Unlock()
	})
	if err := u.setQualificationTier(context.Background(), qualTierComplete); err != nil {
		t.Fatal(err)
	}
	if got := u.currentQualTier(); got != qualTierComplete {
		t.Fatalf("live tier = %q, want complete", got)
	}
}

func TestInterruptedFullQualificationResumesFull(t *testing.T) {
	u := newEmptyUI(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/chat" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]string{"content": "A"},
				"done":    true,
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

	state := newQualState()
	state.StartedAt = time.Now().Add(-time.Minute)
	state.Total = 34
	state.Done = 17
	state.Models = []string{"fake:latest"}
	state.Results["generator|fake:latest|gen-arith"] = true
	state.CoreComplete = true
	state.Tier = qualTierComplete
	state.Deep = true
	u.saveQualState(context.Background(), state)

	if err := u.setQualificationTier(context.Background(), qualTierComplete); err != nil {
		t.Fatal(err)
	}
	after := u.loadQualState(context.Background())
	if !after.Running || !after.Deep {
		t.Fatalf("state = %+v, want a resumed full run", after)
	}
	want := poolQualificationPlan([]string{"fake:latest"}, onboard.DeepLadder())
	if after.Total != want {
		t.Fatalf("total = %d, want the full plan %d", after.Total, want)
	}
	u.cancelQualification()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		u.qualMu.Lock()
		running := u.qualRunning
		u.qualMu.Unlock()
		if !running {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTierSwitchRecomputesProgress(t *testing.T) {
	models := []string{"m"}
	minimal := onboard.QualificationMinimal(onboard.DefaultLadder())
	state := newQualState()
	state.Running = true
	state.StartedAt = time.Now().Add(-10 * time.Second)
	for i := 0; i < 8 && i < len(minimal); i++ {
		ref := minimal[i]
		state.Results[ref.Kind+"|m|"+ref.ID] = true
	}
	minimalView := qualificationViewForPool(state, models, qualTierMinimal, false)
	balancedView := qualificationViewForPool(state, models, qualTierBalanced, false)
	if minimalView.Total >= balancedView.Total {
		t.Fatalf("minimal total = %d, balanced total = %d; want balanced larger", minimalView.Total, balancedView.Total)
	}
	if minimalView.Percent <= balancedView.Percent {
		t.Fatalf("minimal percent = %d, balanced percent = %d; switching up must not raise the percentage",
			minimalView.Percent, balancedView.Percent)
	}
	if balancedView.Done != minimalView.Done {
		t.Fatalf("done changed on a depth switch: minimal=%d balanced=%d", minimalView.Done, balancedView.Done)
	}
}

func TestApplyMeasuredSizesUsesLoadedBytes(t *testing.T) {
	models := applyMeasuredSizes([]setupModel{{Name: "gemma", SizeBytes: 1}}, `{"roles":[{"role":"main","model":"gemma","loaded_bytes":123456}]}`)
	if len(models) != 1 || models[0].SizeBytes != 123456 {
		t.Fatalf("measured sizes = %+v, want the loaded size overlay", models)
	}
	names := measuredModelNames(`{"roles":[{"role":"main","model":"gemma"}]}`)
	if !names["gemma"] {
		t.Fatalf("measured names = %v, want gemma", names)
	}
}

func TestRecommendedResourcesIsAdaptive(t *testing.T) {
	res := recommendedResources(Hardware{
		TotalMemoryBytes: 16 << 30, HostMemoryBytes: 32 << 30, CPUs: 10,
	})
	if res.MemoryMB != 8192 || res.CPUsAllocated != 4 || res.TmpfsMB != 2048 || res.PidsLimit != 128 {
		t.Errorf("adaptive resources = %+v", res)
	}
	if res.OutsideRuntimeGiB != 16 {
		t.Errorf("memory outside runtime = %.0f GiB, want 16", res.OutsideRuntimeGiB)
	}
}

func TestCurrentResourcesShowsPersistedLimits(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.JobPod.MemoryMB = 3072
	cfg.JobPod.CPUs = 2
	cfg.JobPod.TmpfsMB = 768
	cfg.JobPod.PidsLimit = 320
	cfg.Inference.HostReserveMB = 6144
	res := currentResources(Hardware{TotalMemoryBytes: 16 << 30, HostMemoryBytes: 32 << 30, CPUs: 10}, cfg)
	if res.MemoryMB != 3072 || res.CPUsAllocated != 2 || res.TmpfsMB != 768 || res.PidsLimit != 320 || res.HostReserveMB != 6144 {
		t.Errorf("current resources = %+v, want the persisted values", res)
	}
}

func TestBudgetForSeparatesHostInferenceAndRuntimeSandbox(t *testing.T) {
	roles := []setupRole{{Role: "main", Model: "configured"}}
	models := []setupModel{{Name: "configured", SizeBytes: 6 << 30, Measured: true}}
	res := resourceView{MemoryMB: 4096, ReserveMB: 2048, HostReserveMB: 4096,
		EffectiveHostReserveMB: 4096, ReserveBelowVM: true, ReserveClamped: true}
	b := budgetFor(roles, models, Hardware{TotalMemoryBytes: 8 << 30, HostMemoryBytes: 32 << 30}, res)
	if b.HostGiB != 32 || b.RuntimeGiB != 8 || b.VmGiB != 8 {
		t.Errorf("memory shape = %+v, want 32 host / 8 runtime / 8 VM", b)
	}
	if b.InferenceGiB != 28 || b.ModelGiB != 6 || !b.ModelFits {
		t.Errorf("inference budget = %+v, want 6 GiB model in 28 GiB", b)
	}
	if !b.ReserveBelowVM {
		t.Errorf("4 GiB reserve should be flagged below the 8 GiB VM: %+v", b)
	}
	if b.PodGiB != 4 || b.RuntimeMaxGiB != 6 || !b.RuntimeFits || b.RuntimeHeadroom != 2 {
		t.Errorf("runtime budget = %+v, want 4 GiB pod in 6 GiB max", b)
	}
	if b.Overcommit {
		t.Errorf("total host pressure should fit: %+v", b)
	}
	// A model that fits the weights budget can still overcommit the host once
	// the reserve is counted.
	models[0].SizeBytes = 30 << 30
	b = budgetFor(roles, models, Hardware{TotalMemoryBytes: 8 << 30, HostMemoryBytes: 32 << 30}, res)
	if !b.Overcommit || b.OverBy <= 0 {
		t.Errorf("30 GiB model + 4 GiB reserve should be overcommitted: %+v", b)
	}
}

func TestQualDetailsGroupCases(t *testing.T) {
	state := newQualState()
	state.Results["generator|m|g1"] = true
	state.Results["judge|m|j1"] = false
	state.Details["generator|m|g1"] = qualCaseRecord{
		Kind: "generator", CaseID: "g1", Capability: "code", Prompt: "q", Want: "w", Answer: "w", Correct: true,
	}
	state.Details["judge|m|j1"] = qualCaseRecord{
		Kind: "judge", CaseID: "j1", Capability: "general", Prompt: "q", Want: "A", Answer: "B", Correct: false,
	}
	got := qualDetailsFromState(state, []string{"m", "missing"})
	if len(got) != 1 || got[0].Model != "m" || got[0].Passed != 1 || got[0].Total != 2 {
		t.Fatalf("details = %+v, want one model with 1/2", got)
	}
	if got[0].Cases[0].Prompt == "" || got[0].Cases[0].Want == "" || got[0].Cases[0].Answer == "" {
		t.Errorf("case evidence is incomplete: %+v", got[0].Cases[0])
	}
}

func TestRosterViewUsesMeasuredPool(t *testing.T) {
	p := composition.New(nil)
	for _, m := range []struct {
		name  string
		size  int64
		ctx   int
		score float64
	}{
		{"small:3b", 3 << 30, 65536, .78},
		{"medium:9b", 6 << 30, 32768, .80},
		{"large:27b", 17 << 30, 12288, .83},
		{"security:12b", 7 << 30, 32768, .81},
	} {
		p.Upsert(composition.ModelInfo{Name: m.name, SizeBytes: m.size, FileBytes: m.size, ContextTarget: m.ctx})
		for _, cap := range []string{"general", "code", "judgment", "summarize", "security", "agentry"} {
			p.SetMeasured(m.name, cap, int(m.score*100), 100)
		}
	}
	u := &UI{deps: Deps{Pool: p}}
	roster := u.rosterView(0)
	if len(roster.Picks) != 5 {
		t.Fatalf("picks = %+v, want all five functions", roster.Picks)
	}
	if len(roster.Leaders) == 0 {
		t.Fatal("no capability leaders")
	}
	roles := map[string]bool{}
	for _, pick := range roster.Picks {
		roles[pick.Role] = true
		if pick.Model == "" || pick.Reason == "" {
			t.Errorf("incomplete pick: %+v", pick)
		}
	}
	for _, role := range []string{"main", "tall", "wide", "security", "alternative"} {
		if !roles[role] {
			t.Errorf("roster missing %s", role)
		}
	}
}

func TestBudgetForUsesVRAMOnDiscreteGPU(t *testing.T) {
	roles := []setupRole{{Role: "main", Model: "big"}}
	models := []setupModel{{Name: "big", SizeBytes: 19 << 30, Measured: true}}
	res := resourceView{MemoryMB: 4096, ReserveMB: 2048, HostReserveMB: 9216,
		EffectiveHostReserveMB: 9216, MinHostReserveMB: 8192, RecommendedReserveMB: 9216}
	hw := Hardware{
		TotalMemoryBytes: 8 << 30, HostMemoryBytes: 32 << 30, CPUs: 10,
		Accelerator: &doctor.GPUInfo{Name: "RTX 4090", MemoryMiB: 11 * 1024, Kind: "cuda", Backend: "CUDA", Devices: 1},
	}
	b := budgetFor(roles, models, hw, res)
	if b.GPUBudgetGiB <= 0 || b.VRAMGiB != 11 {
		t.Fatalf("gpu budget = %+v, want 11 GiB VRAM with a reserve", b)
	}
	if b.ModelFits || b.SpillGiB <= 0 {
		t.Errorf("19 GiB model on 11 GiB VRAM = fits %v spill %.1f, want spill", b.ModelFits, b.SpillGiB)
	}
	if b.FitLimitGiB != b.GPUBudgetGiB || b.FitLimitGiB >= b.InferenceGiB {
		t.Errorf("fit limit = %.1f, want the GPU budget below the host budget %.1f", b.FitLimitGiB, b.InferenceGiB)
	}
}

func TestBudgetForUnifiedGPUUsesHostBudget(t *testing.T) {
	roles := []setupRole{{Role: "main", Model: "small"}}
	models := []setupModel{{Name: "small", SizeBytes: 6 << 30}}
	res := resourceView{MemoryMB: 4096, ReserveMB: 2048, HostReserveMB: 4096,
		EffectiveHostReserveMB: 4096, MinHostReserveMB: 2048, RecommendedReserveMB: 4096}
	hw := Hardware{
		TotalMemoryBytes: 32 << 30, HostMemoryBytes: 32 << 30,
		Accelerator: &doctor.GPUInfo{Name: "Apple M3 Max", MemoryMiB: 32 * 1024, Kind: "metal", Backend: "Metal", Unified: true, Devices: 1},
	}
	b := budgetFor(roles, models, hw, res)
	if !b.AccelUnified || b.GPUBudgetGiB != 0 {
		t.Errorf("unified accelerator = %+v, want no separate VRAM budget", b)
	}
	if b.FitLimitGiB != b.InferenceGiB || !b.ModelFits {
		t.Errorf("fit limit = %.1f host budget %.1f fits=%v, want host budget", b.FitLimitGiB, b.InferenceGiB, b.ModelFits)
	}
}

func TestInferenceFitBytesClampsToVRAM(t *testing.T) {
	hw := Hardware{
		HostMemoryBytes:  32 << 30,
		TotalMemoryBytes: 32 << 30,
		Accelerator:      &doctor.GPUInfo{Name: "GPU", MemoryMiB: 11 * 1024, Kind: "cuda", Backend: "CUDA", Devices: 1},
	}
	got := inferenceFitBytes(hw, 4<<30)
	if got >= 28<<30 || got <= 0 {
		t.Errorf("inferenceFitBytes = %d, want the discrete VRAM clamp", got)
	}
}

func TestContextTargetForSizeIsWideThenTall(t *testing.T) {
	for _, tc := range []struct {
		sizeGiB float64
		want    int
	}{
		{3, 65536},
		{5, 49152},
		{8, 32768},
		{10, 24576},
		{14, 16384},
		{18, 12288},
		{21, 8192},
	} {
		got := contextTargetForSize(int64(tc.sizeGiB*(1<<30)), 0)
		if got != tc.want {
			t.Errorf("contextTargetForSize(%.0f GiB) = %d, want %d", tc.sizeGiB, got, tc.want)
		}
	}
	// Capacity caps the working window; it never raises it.
	if got := contextTargetForSize(10<<30, 12288); got != 12288 {
		t.Errorf("capacity-capped target = %d, want 12288", got)
	}
	// A model with a small capacity is not given a larger working window.
	if got := contextTargetForSize(3<<30, 4096); got != 4096 {
		t.Errorf("small-capacity target = %d, want 4096", got)
	}
}

func TestMaxContextForUsesKVAndNativeCap(t *testing.T) {
	// 6 GiB weights, 128 KiB/token, 28 GiB budget, 8k native cap.
	if got := composition.MaxContextFor(6<<30, 128<<10, 8192, 28<<30); got != 8192 {
		t.Errorf("native-capped max = %d, want 8192", got)
	}
	// Without a native cap, the KV budget sets the limit.
	got := composition.MaxContextFor(6<<30, 128<<10, 0, 28<<30)
	if got <= 0 || got >= 28<<30 {
		t.Errorf("budget max = %d, want a positive bounded value", got)
	}
	// Weights plus safety exceeding the budget leaves no useful window.
	if got := composition.MaxContextFor(28<<30, 128<<10, 0, 28<<30); got != 0 {
		t.Errorf("over-budget max = %d, want 0", got)
	}
}

func TestQualificationPlanIsComprehensive(t *testing.T) {
	l := onboard.DefaultLadder()
	if len(l.Generators) < 10 || len(l.Judges) < 12 {
		t.Fatalf("default ladder = %d generators / %d judges, want at least 10/12", len(l.Generators), len(l.Judges))
	}
	if got, want := poolQualificationPlan([]string{"a", "b"}, l), 2*(len(l.Generators)+len(l.Judges)); got != want {
		t.Errorf("qualification plan = %d, want %d", got, want)
	}
}

func TestOnboardingEnvironmentCheckIsFinal(t *testing.T) {
	if onboardingIndex("environment") != len(onboardingSteps)-2 {
		t.Fatalf("environment index = %d, want the penultimate step", onboardingIndex("environment"))
	}
	if onboardingIndex("environment") < onboardingIndex("preferences") {
		t.Fatal("environment checks should run after preferences, as the final readiness summary")
	}
}

func TestOnboardingSkipIsDurable(t *testing.T) {
	u := newEmptyUI(t)
	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp := postForm(t, ts.URL+"/ui/welcome", url.Values{
		"step": {"welcome"}, "action": {"skip"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("skip status = %d", resp.StatusCode)
	}
	st := u.loadOnboarding(context.Background())
	if !st.Skipped {
		t.Fatal("skip was not persisted")
	}
	if u.needsOnboarding(context.Background()) {
		t.Fatal("a skipped onboarding still demands attention")
	}
}

func TestWelcomeRunsDoctorChecks(t *testing.T) {
	u := newEmptyUI(t)
	u.deps.Doctor = func(_ context.Context) doctor.Report {
		return doctor.Report{Checks: []doctor.Check{
			{Name: "podman", Status: doctor.StatusOK, Detail: "rootless 5.8"},
			{Name: "memory", Status: doctor.StatusWarn, Detail: "16 GiB", Remediation: "pull a smaller model"},
		}}
	}
	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	_, body := get(t, ts.URL+"/ui/welcome?step=environment")
	for _, want := range []string{"rootless 5.8", "pull a smaller model", "Run checks again", "No separate CLI command"} {
		if !strings.Contains(body, want) {
			t.Errorf("environment check missing %q", want)
		}
	}
}

func TestWelcomeResourcesStep(t *testing.T) {
	u := newEmptyUI(t)
	u.deps.Hardware = func(_ context.Context) Hardware {
		return Hardware{TotalMemoryBytes: 32 << 30, FreeDiskBytes: 100 << 30, CPUs: 10}
	}
	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	_, body := get(t, ts.URL+"/ui/welcome?step=resources")
	for _, want := range []string{"Resources.", "GiB runtime RAM", "CPU cores", "Sandbox limits", "Apply and restart Core", "memory_mb"} {
		if !strings.Contains(body, want) {
			t.Errorf("resources step missing %q", want)
		}
	}
	if !strings.Contains(body, "32") || !strings.Contains(body, "10") {
		t.Errorf("resources step did not show detected hardware:\n%s", body)
	}
}

func TestWelcomeAppliesResourceLimitsAndRestarts(t *testing.T) {
	u := newEmptyUI(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted := make(chan struct{}, 1)
	u.deps.ConfigPath = path
	u.deps.Hardware = func(context.Context) Hardware {
		return Hardware{TotalMemoryBytes: 8 << 30, HostMemoryBytes: 32 << 30, CPUs: 10}
	}
	u.deps.Restart = func() { restarted <- struct{}{} }
	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp := postForm(t, ts.URL+"/ui/welcome", url.Values{
		"step": {"resources"}, "action": {"apply_resources"},
		"memory_mb": {"4096"}, "cpus": {"2.5"}, "tmpfs_mb": {"1024"}, "pids_limit": {"256"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apply status = %d, want the restart page", resp.StatusCode)
	}
	select {
	case <-restarted:
	case <-time.After(3 * time.Second):
		t.Fatal("Core was not asked to restart after applying resources")
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.JobPod.MemoryMB != 4096 || cfg.JobPod.CPUs != 2.5 || cfg.JobPod.TmpfsMB != 1024 || cfg.JobPod.PidsLimit != 256 {
		t.Fatalf("job pod limits = %+v", cfg.JobPod)
	}
}

func TestWelcomeRejectsImpossibleResourceLimits(t *testing.T) {
	u := newEmptyUI(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	u.deps.ConfigPath = path
	u.deps.Hardware = func(context.Context) Hardware { return Hardware{CPUs: 4} }
	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp := postForm(t, ts.URL+"/ui/welcome", url.Values{
		"step": {"resources"}, "action": {"apply_resources"},
		"memory_mb": {"10"}, "cpus": {"2"}, "tmpfs_mb": {"1024"}, "pids_limit": {"256"},
	})
	if resp.StatusCode == http.StatusOK {
		t.Fatal("impossible memory limit was accepted")
	}
}

func TestWelcomeModelsSelfSelectAndApply(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{
			{"name": "small:3b", "size": 2 << 30, "details": map[string]string{"family": "granite"}},
			{"name": "mid:8b", "size": 5 << 30, "details": map[string]string{"family": "qwen"}},
			{"name": "large:14b", "size": 9 << 30, "details": map[string]string{"family": "gemma"}},
			{"name": "alt:12b", "size": 8 << 30, "details": map[string]string{"family": "muse"}},
		}})
	}))
	t.Cleanup(fake.Close)
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Inference.OllamaURL = fake.URL

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 2\npersonas:\n  main:\n    model: old-main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := store.Migrate(s.DB(), migrations.FS, ""); err != nil {
		t.Fatal(err)
	}
	u := New(Deps{Store: s, Pool: composition.New(s), Config: cfg, ConfigPath: path, Runtime: "Core Pod", Version: "test",
		Hardware: func(_ context.Context) Hardware {
			return Hardware{TotalMemoryBytes: 32 << 30, CPUs: 10}
		}})
	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	_, body := get(t, ts.URL+"/ui/welcome?step=models")
	for _, want := range []string{"Build the model pool", "Installed models", "large:14b", "Remove", "Inference budget", "Runtime sandbox", "The pool", "Fit limit"} {
		if !strings.Contains(body, want) {
			t.Errorf("models step missing %q", want)
		}
	}
	if strings.Contains(body, "What the roles mean") || strings.Contains(body, "Use this assignment") {
		t.Error("models step still renders the retired persona assignment UI")
	}
	if got := u.deps.Pool.Count(); got != 4 {
		t.Fatalf("pool count = %d, want 4 (the installed catalog)", got)
	}

	resp := postForm(t, ts.URL+"/ui/welcome", url.Values{
		"step": {"models"}, "action": {"pool_remove"}, "model": {"large:14b"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("pool remove status = %d", resp.StatusCode)
	}
	if got := u.deps.Pool.Count(); got != 3 {
		t.Fatalf("pool count after remove = %d, want 3", got)
	}
	if u.deps.Pool.Has("large:14b") {
		t.Fatal("removed model is still in the pool")
	}

	resp = postForm(t, ts.URL+"/ui/welcome", url.Values{
		"step": {"models"}, "action": {"pool_add"}, "model": {"large:14b"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("pool add status = %d", resp.StatusCode)
	}
	if !u.deps.Pool.Has("large:14b") {
		t.Fatal("re-added model did not return to the pool")
	}
}

func TestWelcomeApplyResources(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 2\njob_pod:\n  image: test-image\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	u := New(Deps{
		Store: nil, ConfigPath: path,
		Hardware: func(context.Context) Hardware {
			return Hardware{TotalMemoryBytes: 16 << 30, HostMemoryBytes: 32 << 30, CPUs: 10}
		},
	})
	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	// Operator values are honored; the Core restart is covered separately.
	resp := postForm(t, ts.URL+"/ui/welcome", url.Values{
		"step": {"resources"}, "action": {"apply_resources"},
		"memory_mb": {"4096"}, "cpus": {"2"}, "tmpfs_mb": {"1024"}, "pids_limit": {"256"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("apply resources status = %d", resp.StatusCode)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("applied config does not load: %v", err)
	}
	if got.JobPod.MemoryMB != 4096 || got.JobPod.CPUs != 2 || got.JobPod.TmpfsMB != 1024 || got.JobPod.PidsLimit != 256 {
		t.Errorf("resources = %+v", got.JobPod)
	}
}

// TestSoftenDefaultModelWarning pins the first-run copy: an unassigned
// install points at the Models step instead of listing five large pulls.
func TestSoftenDefaultModelWarning(t *testing.T) {
	checks := []healthCheck{{
		Name:   "models",
		Status: "warn",
		Detail: "configured model(s) not installed: qwen2.5:7b, mistral-nemo:12b",
		Fix:    "ollama pull qwen2.5:7b && ollama pull mistral-nemo:12b",
	}}
	got := softenDefaultModelWarning(checks, []setupRole{{Role: "main", Model: "qwen2.5:7b", Installed: false}})
	if strings.Contains(got[0].Fix, "ollama pull") {
		t.Errorf("fresh install still suggests default pulls: %q", got[0].Fix)
	}
	if !strings.Contains(got[0].Fix, "Models step") {
		t.Errorf("fresh install does not point at the Models step: %q", got[0].Fix)
	}

	// Once any persona matches an installed model, the doctor's real warning
	// stays intact.
	assigned := []healthCheck{{Name: "models", Status: "ok", Detail: "all configured models present"}}
	got = softenDefaultModelWarning(assigned, []setupRole{{Role: "main", Model: "bonsai-2:9b", Installed: true}})
	if got[0].Detail != "all configured models present" {
		t.Errorf("assigned install changed the check: %+v", got[0])
	}
}
