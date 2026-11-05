package ui

import (
	"context"
	"fmt"
	"io"
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

func TestSearchHTMXReturnsFragment(t *testing.T) {
	fx := setup(t)
	req, err := http.NewRequest(http.MethodGet, fx.ts.URL+"/ui/search?q=demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(body, "<html") || strings.Contains(body, "<main") {
		t.Fatalf("HTMX search returned a full page:\n%s", body)
	}
	if !strings.Contains(body, "demo") || !strings.Contains(body, "Project") {
		t.Fatalf("HTMX search fragment missing the result:\n%s", body)
	}
}

type fakeControl struct{ frozen bool }

func (f *fakeControl) Frozen() bool { return f.frozen }
func (f *fakeControl) Freeze(_ context.Context) error {
	f.frozen = true
	return nil
}
func (f *fakeControl) Unfreeze(_ context.Context, reason string) error {
	if reason == "" {
		return fmt.Errorf("reason required")
	}
	f.frozen = false
	return nil
}

func TestSettingsKillSwitch(t *testing.T) {
	fx := setup(t)
	fc := &fakeControl{}
	fx.ui.deps.Control = fc

	_, body := get(t, fx.ts.URL+"/ui/settings")
	if !strings.Contains(body, "Freeze Athanor") || !strings.Contains(body, "Not frozen") {
		t.Fatalf("settings missing the kill-switch control (%.200q)", body)
	}
	resp := postForm(t, fx.ts.URL+"/ui/freeze", url.Values{"action": {"freeze"}})
	if resp.StatusCode != http.StatusSeeOther || !fc.frozen {
		t.Fatalf("freeze status=%d frozen=%v", resp.StatusCode, fc.frozen)
	}
	_, body = get(t, fx.ts.URL+"/ui/settings")
	if !strings.Contains(body, "Frozen") || !strings.Contains(body, "Unfreeze") {
		t.Fatalf("frozen settings missing unfreeze (%.200q)", body)
	}
	resp = postForm(t, fx.ts.URL+"/ui/freeze", url.Values{"action": {"unfreeze"}, "reason": {""}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty-reason unfreeze status = %d, want 400", resp.StatusCode)
	}
	resp = postForm(t, fx.ts.URL+"/ui/freeze", url.Values{"action": {"unfreeze"}, "reason": {"checked"}})
	if resp.StatusCode != http.StatusSeeOther || fc.frozen {
		t.Fatalf("unfreeze status=%d frozen=%v", resp.StatusCode, fc.frozen)
	}
}

type fakeExporter struct{ called bool }

func (f *fakeExporter) ExportOne(_ context.Context, id string) (string, bool, error) {
	f.called = true
	return "/tmp/export/" + id, true, nil
}

type fakeResetter struct{ called string }

func (f *fakeResetter) Reset(_ context.Context, taskID string) ([]string, error) {
	f.called = taskID
	return []string{taskID}, nil
}

func TestTaskReset(t *testing.T) {
	fx := setup(t)
	fr := &fakeResetter{}
	fx.ui.deps.TaskResetter = fr
	resp := postForm(t, fx.ts.URL+"/ui/tasks/"+fx.task.ID+"/reset", url.Values{})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("reset status = %d", resp.StatusCode)
	}
	if fr.called != fx.task.ID {
		t.Fatalf("reset called for %q, want %q", fr.called, fx.task.ID)
	}
}

func TestArtifactExport(t *testing.T) {
	fx := setup(t)
	fe := &fakeExporter{}
	fx.ui.SetExporter(fe)
	resp := postForm(t, fx.ts.URL+"/ui/artifacts/"+fx.artifact.ID+"/export", url.Values{})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("export status = %d", resp.StatusCode)
	}
	if !fe.called {
		t.Fatal("exporter was not called")
	}
}

func TestRestartRequiredBanner(t *testing.T) {
	fx := setup(t)
	fx.ui.markRestartRequired(context.Background(), "persona assignment changed")
	_, body := get(t, fx.ts.URL+"/ui")
	if !strings.Contains(body, "restart required") || !strings.Contains(body, "persona assignment changed") {
		t.Fatalf("restart banner missing:\n%s", body)
	}
	if !strings.Contains(body, "restart the athanor serve process") {
		t.Errorf("restart hint missing for the host runtime")
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
