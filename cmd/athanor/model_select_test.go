package main

import (
	"testing"

	"github.com/tcs76321/athanor/internal/composition"
	"github.com/tcs76321/athanor/internal/config"
)

func TestPickWorkModelPrefersThePoolOverAStaleLegacyPersona(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Personas.Tall.Model = "qwen2.5-coder:32b" // not installed
	pool := composition.New(nil)
	pool.Upsert(composition.ModelInfo{Name: "bonsai-2:9b", ContextTarget: 32768, Family: "qwen35"})

	model, contextTarget, err := pickWorkModel(pool, cfg, "discovery", composition.StanceSynthesize, 16384)
	if err != nil {
		t.Fatal(err)
	}
	if model != "bonsai-2:9b" {
		t.Fatalf("model = %q, want the installed pool model", model)
	}
	if contextTarget < 16384 {
		t.Fatalf("context target = %d, want at least 16384", contextTarget)
	}
}

func TestPickWorkModelFallsBackToLegacyWhenNoPoolIsWired(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Personas.Tall.Model = "legacy-tall"
	cfg.Personas.Main.Model = "legacy-main"

	model, _, err := pickWorkModel(nil, cfg, "planning", composition.StancePlan, 8192)
	if err != nil {
		t.Fatal(err)
	}
	if model != "legacy-tall" {
		t.Fatalf("model = %q, want the legacy tall fallback", model)
	}
}

func TestPickWorkModelReportsAnEmptyPool(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Personas.Tall.Model = ""
	cfg.Personas.Main.Model = ""
	if _, _, err := pickWorkModel(composition.New(nil), cfg, "discovery", composition.StanceSynthesize, 16384); err == nil {
		t.Fatal("empty pool and empty legacy personas succeeded; want an error")
	}
}
