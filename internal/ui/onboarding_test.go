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
		"Running qualification", "40%", "4 / 10", `data-auto-refresh="2000"`,
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
	if res.MemoryMB != 8192 || res.CPUsAllocated != 4 || res.TmpfsMB != 2048 || res.PidsLimit != 512 {
		t.Errorf("adaptive resources = %+v", res)
	}
	if res.OutsideRuntimeGiB != 16 {
		t.Errorf("memory outside runtime = %.0f GiB, want 16", res.OutsideRuntimeGiB)
	}
}

func TestBudgetForSeparatesHostInferenceAndRuntimeSandbox(t *testing.T) {
	roles := []setupRole{{Role: "main", Model: "configured"}}
	models := []setupModel{{Name: "configured", SizeBytes: 6 << 30, Measured: true}}
	res := resourceView{MemoryMB: 4096, ReserveMB: 2048}
	b := budgetFor(roles, models, Hardware{TotalMemoryBytes: 8 << 30, HostMemoryBytes: 32 << 30}, res)
	if b.HostGiB != 32 || b.RuntimeGiB != 8 || b.VmGiB != 8 {
		t.Errorf("memory shape = %+v, want 32 host / 8 runtime / 8 VM", b)
	}
	if b.InferenceGiB != 16 || b.ModelGiB != 6 || !b.ModelFits {
		t.Errorf("inference budget = %+v, want 6 GiB model in 16 GiB", b)
	}
	if b.PodGiB != 4 || b.RuntimeMaxGiB != 6 || !b.RuntimeFits || b.RuntimeHeadroom != 2 {
		t.Errorf("runtime budget = %+v, want 4 GiB pod in 6 GiB max", b)
	}
	if b.Overcommit {
		t.Errorf("total host pressure should fit: %+v", b)
	}
	// A model that fits the audit alone can still overcommit the host once the
	// Podman runtime is counted.
	models[0].SizeBytes = 20 << 30
	b = budgetFor(roles, models, Hardware{TotalMemoryBytes: 8 << 30, HostMemoryBytes: 32 << 30}, res)
	if !b.Overcommit || b.OverBy <= 0 {
		t.Errorf("20 GiB model + 8 GiB VM + 8 GiB reserve should be overcommitted: %+v", b)
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
	for _, want := range []string{"Build the model pool", "Installed models", "large:14b", "Remove", "Host inference budget", "Runtime sandbox", "The pool"} {
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
