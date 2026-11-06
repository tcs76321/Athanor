package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tcs76321/athanor/internal/config"
)

func TestModelCatalogIsCachedAndInvalidated(t *testing.T) {
	var tags, show int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			atomic.AddInt32(&tags, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{{
					"name": "alpha", "size": 1024, "details": map[string]string{"family": "a"},
				}},
			})
		case "/api/show":
			atomic.AddInt32(&show, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{"model_info": map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Inference.OllamaURL = srv.URL
	u := New(Deps{Config: cfg})

	models, err := u.modelCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Name != "alpha" {
		t.Fatalf("models = %#v, want alpha", models)
	}
	if got := atomic.LoadInt32(&tags); got != 1 {
		t.Errorf("/api/tags calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&show); got != 1 {
		t.Errorf("/api/show calls = %d, want 1", got)
	}

	if _, err := u.modelCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&tags); got != 1 {
		t.Errorf("cached /api/tags calls = %d, want 1", got)
	}

	u.catalogAt = time.Now().Add(-time.Hour)
	if _, err := u.modelCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&tags); got != 2 {
		t.Errorf("expired cache /api/tags calls = %d, want 2", got)
	}
}
