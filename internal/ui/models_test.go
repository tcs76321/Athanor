package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/composition"
	"github.com/tcs76321/athanor/internal/config"
)

func TestModelsPageManagesPoolAfterOnboarding(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{
			{"name": "alpha:3b", "size": 2 << 30, "details": map[string]string{"family": "a"}},
			{"name": "beta:9b", "size": 6 << 30, "details": map[string]string{"family": "b"}},
			{"name": "gamma:12b", "size": 8 << 30, "details": map[string]string{"family": "c"}},
			{"name": "delta:4b", "size": 3 << 30, "details": map[string]string{"family": "d"}},
		}})
	}))
	t.Cleanup(fake.Close)

	u := newEmptyUI(t)
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Inference.OllamaURL = fake.URL
	u.deps.Config = cfg
	u.deps.Pool = composition.New(u.deps.Store)
	u.deps.Hardware = func(context.Context) Hardware {
		return Hardware{TotalMemoryBytes: 8 << 30, HostMemoryBytes: 32 << 30, CPUs: 8}
	}
	if err := u.saveOnboarding(context.Background(), onboardingState{Completed: true, Step: "ready"}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	u.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	status, body := get(t, ts.URL+"/ui/models")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	for _, want := range []string{"What Athanor may choose from", "Pool", "alpha:3b", "Remove", "Run minimal core", "Run core + extra"} {
		if !strings.Contains(body, want) {
			t.Errorf("models page missing %q", want)
		}
	}
	if got := u.deps.Pool.Count(); got != 4 {
		t.Fatalf("pool count = %d, want 4", got)
	}

	resp := postForm(t, ts.URL+"/ui/models/pool", url.Values{
		"action": {"pool_remove"}, "model": {"beta:9b"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("remove status = %d", resp.StatusCode)
	}
	if u.deps.Pool.Has("beta:9b") {
		t.Fatal("removed model is still in the pool")
	}

	resp = postForm(t, ts.URL+"/ui/models/pool", url.Values{
		"action": {"pool_add"}, "model": {"beta:9b"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("add status = %d", resp.StatusCode)
	}
	if !u.deps.Pool.Has("beta:9b") {
		t.Fatal("re-added model did not return")
	}
}
