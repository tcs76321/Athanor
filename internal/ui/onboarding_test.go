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
	return New(Deps{Store: s, Runtime: "Core Pod", Version: "test"})
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
		"Environment", "Models", "Qualification", "Preferences", "Ready",
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

func TestBudgetForUsesHostMemoryNotRuntimeMemory(t *testing.T) {
	b := budgetFor(
		[]setupModel{{SizeBytes: 20 << 30}},
		Hardware{TotalMemoryBytes: 16 << 30, HostMemoryBytes: 32 << 30},
		resourceView{MemoryMB: 8192, ReserveMB: 2048},
	)
	if b.TotalGiB != 32 {
		t.Errorf("budget total = %.1f GiB, want the 32 GiB host total", b.TotalGiB)
	}
	if b.Overcommit {
		t.Errorf("budget = %+v, want it to fit in host memory", b)
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
	for _, want := range []string{"Resources.", "GiB runtime RAM", "CPU cores", "Adaptive sandbox", "Apply adaptive limits"} {
		if !strings.Contains(body, want) {
			t.Errorf("resources step missing %q", want)
		}
	}
	if !strings.Contains(body, "32") || !strings.Contains(body, "10") {
		t.Errorf("resources step did not show detected hardware:\n%s", body)
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
	u := New(Deps{Store: s, Config: cfg, ConfigPath: path, Runtime: "Core Pod", Version: "test",
		Hardware: func(_ context.Context) Hardware {
			return Hardware{TotalMemoryBytes: 32 << 30, CPUs: 10}
		}})
	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	_, body := get(t, ts.URL+"/ui/welcome?step=models")
	for _, want := range []string{"What the roles mean", "Installed models", "Athanor's selection", "large:14b", "Use this assignment", "Memory budget"} {
		if !strings.Contains(body, want) {
			t.Errorf("models step missing %q", want)
		}
	}
	// The old persona mapping table must not be the primary content anymore.
	if strings.Contains(body, "<th>Role</th><th>Model</th><th>Family</th><th>Context</th>") {
		t.Error("models step still renders the old configured-mapping table")
	}

	resp := postForm(t, ts.URL+"/ui/welcome", url.Values{"step": {"models"}, "action": {"apply_personas"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("apply personas status = %d", resp.StatusCode)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("applied config does not load: %v", err)
	}
	if got.Personas.Tall.Model != "large:14b" {
		t.Errorf("tall = %q, want large:14b", got.Personas.Tall.Model)
	}
	if got.Personas.Security.Model == "" || got.Personas.Main.Model == "" {
		t.Errorf("assignment missing roles: %+v", got.Personas)
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

	// The form values are ignored: limits are adaptive, not operator fields.
	resp := postForm(t, ts.URL+"/ui/welcome", url.Values{
		"step": {"resources"}, "action": {"apply_resources"},
		"memory_mb": {"999"}, "cpus": {"99"}, "tmpfs_mb": {"99999"}, "pids_limit": {"99999"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("apply resources status = %d", resp.StatusCode)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("applied config does not load: %v", err)
	}
	if got.JobPod.MemoryMB != 8192 || got.JobPod.CPUs != 4 || got.JobPod.TmpfsMB != 2048 || got.JobPod.PidsLimit != 512 {
		t.Errorf("resources = %+v", got.JobPod)
	}
}
