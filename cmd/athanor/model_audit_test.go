package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/onboard"
)

func fakeOllamaAuditServer(t *testing.T, loadedBytes int64) *httptest.Server {
	t.Helper()
	loaded := map[string]int64{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{{
				"name": "fake:3b", "model": "fake:3b", "size": int64(2 << 30),
				"details": map[string]any{"family": "fake"},
			}},
		})
	})
	mux.HandleFunc("/api/generate", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model     string `json:"model"`
			KeepAlive any    `json:"keep_alive"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode generate: %v", err)
			return
		}
		if ka, ok := body.KeepAlive.(float64); ok && ka == 0 {
			delete(loaded, body.Model)
		} else {
			loaded[body.Model] = loadedBytes
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"done": true})
	})
	mux.HandleFunc("/api/ps", func(w http.ResponseWriter, _ *http.Request) {
		var models []map[string]any
		for name, size := range loaded {
			models = append(models, map[string]any{"name": name, "model": name, "size": size})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func auditTestConfig(t *testing.T, url string) *config.Config {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Inference.OllamaURL = url
	pc := config.PersonaConfig{Model: "fake:3b", Family: "fake", ContextTarget: 32768}
	cfg.Personas = config.Personas{Wide: pc, Tall: pc, Main: pc, Security: pc, Alternative: pc}
	return cfg
}

func TestRunModelAuditMeasuresLoadedSize(t *testing.T) {
	srv := fakeOllamaAuditServer(t, 5<<30)
	cfg := auditTestConfig(t, srv.URL)
	probes := &osProbes{client: srv.Client()}
	a, err := runModelAudit(context.Background(), cfg, probes, 32<<30, 8<<30)
	if err != nil {
		t.Fatal(err)
	}
	if a.BudgetBytes != 24<<30 {
		t.Errorf("budget = %d, want 24 GiB", a.BudgetBytes)
	}
	if len(a.Roles) != 5 {
		t.Fatalf("roles = %d, want 5", len(a.Roles))
	}
	for _, r := range a.Roles {
		if !r.Fits || r.LoadedBytes != 5<<30 || r.FileBytes != 2<<30 {
			t.Errorf("role = %+v, want a 5 GiB loaded / 2 GiB file fit", r)
		}
	}
	if len(a.Resident) != 0 {
		t.Errorf("resident after audit = %v, want none", a.Resident)
	}
}

func TestRunModelAuditMarksOversizeAsFailing(t *testing.T) {
	srv := fakeOllamaAuditServer(t, 30<<30)
	cfg := auditTestConfig(t, srv.URL)
	probes := &osProbes{client: srv.Client()}
	a, err := runModelAudit(context.Background(), cfg, probes, 32<<30, 8<<30)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.FailingRoles()) != 5 {
		t.Errorf("failing roles = %d, want 5", len(a.FailingRoles()))
	}
}

func TestCheckModelAuditFailsOnMeasuredNonFit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model-fit.json")
	a := onboard.Audit{
		GeneratedAt: "2026-11-04T00:00:00Z", HostBytes: 32 << 30,
		ReserveBytes: 8 << 30, BudgetBytes: 24 << 30,
		Roles: []onboard.AuditRole{{Role: "main", Model: "big:30b", LoadedBytes: 30 << 30, Fits: false}},
	}
	raw, _ := json.Marshal(a)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := checkModelAudit(path, nil, 32<<30); err == nil {
		t.Fatal("a measured non-fit was accepted")
	}
}

func TestCheckModelAuditRejectsStaleConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model-fit.json")
	a := onboard.Audit{
		GeneratedAt: "2026-11-04T00:00:00Z", HostBytes: 32 << 30,
		ReserveBytes: 8 << 30, BudgetBytes: 24 << 30,
		Roles: []onboard.AuditRole{{Role: "main", Model: "old:7b", ContextTarget: 32768, LoadedBytes: 8 << 30, Fits: true}},
	}
	raw, _ := json.Marshal(a)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Personas.Main = config.PersonaConfig{Model: "new:9b", ContextTarget: 32768}
	if _, err := checkModelAudit(path, cfg, 32<<30); err == nil {
		t.Fatal("an audit for a different configured model was accepted")
	}
}

func TestCheckModelAuditMissingIsAWarning(t *testing.T) {
	if _, err := checkModelAudit(filepath.Join(t.TempDir(), "missing.json"), nil, 32<<30); err != nil {
		t.Fatalf("missing audit should not fail startup: %v", err)
	}
}
