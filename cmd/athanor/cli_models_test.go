package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/onboard"
)

// TestOllamaCatalog parses /api/tags into models with size + family, falling
// back to the `model` field and skipping blank entries.
func TestOllamaCatalog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"models":[
			{"name":"ornith-1.5:9b","size":7000000000,"details":{"family":"qwen35"}},
			{"model":"granite4.2:3b","size":2000000000,"details":{"family":"granite"}},
			{"name":"","model":"","size":1}
		]}`))
	}))
	defer srv.Close()

	got, err := ollamaCatalog(context.Background(), newOSProbes(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("models = %d, want 2 (blank skipped): %+v", len(got), got)
	}
	if got[0].Name != "ornith-1.5:9b" || got[0].SizeBytes != 7000000000 || got[0].Family != "qwen35" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Name != "granite4.2:3b" {
		t.Errorf("name fallback to .model failed: %+v", got[1])
	}
}

// TestModelRolesOverlaysConfigFamily pins that the operator-declared family
// wins over Ollama's own hint.
func TestModelRolesOverlaysConfigFamily(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Personas.Main.Model = "ornith-1.5:9b"
	cfg.Personas.Main.Family = "qwen35"
	models := []onboard.Model{{Name: "ornith-1.5:9b", SizeBytes: 7 << 30, Family: "ollama-hint"}}

	roles, families := modelRoles(cfg, models)
	if families["ornith-1.5:9b"] != "qwen35" {
		t.Errorf("config family did not override the hint: %v", families["ornith-1.5:9b"])
	}
	found := false
	for _, r := range roles {
		if r.Role == "main" {
			found = true
		}
	}
	if !found {
		t.Error("main role missing from modelRoles")
	}
}
