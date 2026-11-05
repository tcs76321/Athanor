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
	if loc := resp.Header.Get("Location"); loc != "/ui/welcome?step=environment" {
		t.Fatalf("next location = %q", loc)
	}
	if got := u.loadOnboarding(context.Background()).Step; got != "environment" {
		t.Fatalf("persisted step = %q, want environment", got)
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
	for _, want := range []string{"Resources.", "GiB host RAM", "CPU cores", "Job Pod memory (MB)", "Save resource limits"} {
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
	u := New(Deps{Store: nil, ConfigPath: path})
	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp := postForm(t, ts.URL+"/ui/welcome", url.Values{
		"step": {"resources"}, "action": {"apply_resources"},
		"memory_mb": {"4096"}, "cpus": {"2"}, "tmpfs_mb": {"2048"}, "pids_limit": {"256"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("apply resources status = %d", resp.StatusCode)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("applied config does not load: %v", err)
	}
	if got.JobPod.MemoryMB != 4096 || got.JobPod.CPUs != 2 || got.JobPod.TmpfsMB != 2048 || got.JobPod.PidsLimit != 256 {
		t.Errorf("resources = %+v", got.JobPod)
	}
}
