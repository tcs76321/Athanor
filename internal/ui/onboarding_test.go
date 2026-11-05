package ui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

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
