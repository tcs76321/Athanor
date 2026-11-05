package ui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSettingsAndThemeRoundTrip(t *testing.T) {
	fx := setup(t)
	code, body := get(t, fx.ts.URL+"/ui/settings")
	if code != http.StatusOK {
		t.Fatalf("settings status = %d", code)
	}
	for _, want := range []string{"Appearance", "Monokai", "Ember Paper", `data-theme-preview="ember"`} {
		if !strings.Contains(body, want) {
			t.Errorf("settings missing %q", want)
		}
	}
	resp := postForm(t, fx.ts.URL+"/ui/theme", url.Values{"theme": {"ember"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("theme post status = %d, want 303", resp.StatusCode)
	}
	if got := fx.ui.Theme(); got != "ember" {
		t.Fatalf("theme = %q, want ember", got)
	}
	_, body = get(t, fx.ts.URL+"/ui")
	if !strings.Contains(body, `data-theme="ember"`) {
		t.Error("rendered shell did not apply the saved theme")
	}
	// Persisted in system_state, so a new UI instance sees it.
	if got := New(Deps{Store: fx.ui.deps.Store}).Theme(); got != "ember" {
		t.Errorf("persisted theme = %q, want ember", got)
	}
}

func TestInvalidThemeRejected(t *testing.T) {
	fx := setup(t)
	resp := postForm(t, fx.ts.URL+"/ui/theme", url.Values{"theme": {"neon"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid theme status = %d, want 400", resp.StatusCode)
	}
	if got := fx.ui.Theme(); got != "monokai" {
		t.Fatalf("theme changed to %q after an invalid request", got)
	}
}

func TestAssetsAreEmbeddedAndCached(t *testing.T) {
	fx := setup(t)
	code, body := get(t, fx.ts.URL+"/ui/assets/app.css")
	if code != http.StatusOK {
		t.Fatalf("css status = %d", code)
	}
	if !strings.Contains(body, "--accent") || !strings.Contains(body, `[data-theme="ember"]`) {
		t.Error("css asset is missing the design tokens")
	}
	if code, _ := get(t, fx.ts.URL+"/ui/assets/does-not-exist.css"); code != http.StatusNotFound {
		t.Errorf("missing asset status = %d, want 404", code)
	}
	// A traversal attempt must not escape the embedded root.
	if code, _ := get(t, fx.ts.URL+"/ui/assets/../ui.go"); code != http.StatusNotFound {
		t.Errorf("traversal status = %d, want 404", code)
	}
}

func TestSitemapExposesEverySurface(t *testing.T) {
	fx := setup(t)
	_, body := get(t, fx.ts.URL+"/ui/sitemap")
	for _, want := range []string{"Sitemap", "Memory", "Events", "Settings", "Review queue"} {
		if !strings.Contains(body, want) {
			t.Errorf("sitemap missing %q", want)
		}
	}
}

func TestSearchFindsProjectsAndReturnsJSON(t *testing.T) {
	fx := setup(t)
	_, body := get(t, fx.ts.URL+"/ui/search?q=demo")
	if !strings.Contains(body, "demo") || !strings.Contains(body, "Project") {
		t.Errorf("search page missing the demo project:\n%s", body)
	}
	code, body := get(t, fx.ts.URL+"/ui/search?format=json&q=demo")
	if code != http.StatusOK {
		t.Fatalf("json search status = %d", code)
	}
	if !strings.Contains(body, `"results"`) || !strings.Contains(body, `"url":"/ui/projects/`) {
		t.Errorf("json search missing a project result: %s", body)
	}
}

func TestCSPAndNoInlineScripts(t *testing.T) {
	fx := setup(t)
	resp, err := http.Get(fx.ts.URL + "/ui")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q, want a strict self-only policy", csp)
	}
}
