package main

import (
	"errors"
	"testing"

	"github.com/tcs76321/athanor/internal/capacity"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/engine"
)

func adaptiveConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.ContextEngine.ContextPolicy = "adaptive"
	return cfg
}

func TestNewContextPlannerDisabledByDefault(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	if p := newContextPlanner(cfg); p != nil {
		t.Errorf("planner = %T, want nil for context_policy=archetype", p)
	}
}

func TestContextPlannerJudgeIgnoresRepoFloor(t *testing.T) {
	p := newContextPlanner(adaptiveConfig(t))
	dec, err := p.PlanContext(engine.ContextRequest{
		Phase: "evaluating", Capability: "code", Demand: "repo",
		Model: "m", ModelMax: 32768, Pinned: 2000, Output: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dec.Floor != 8192 {
		t.Errorf("judge floor = %d, want 8192 (repo demand must not apply)", dec.Floor)
	}
	if dec.Target != 8192 {
		t.Errorf("judge target = %d, want 8192", dec.Target)
	}
}

func TestContextPlannerRepoCodeGetsDeepWindow(t *testing.T) {
	p := newContextPlanner(adaptiveConfig(t))
	dec, err := p.PlanContext(engine.ContextRequest{
		Phase: "diverging", Capability: "code", Demand: "repo",
		Model: "m", ModelMax: 32768, Pinned: 2000, Working: 6000, Output: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dec.Floor != 32768 || dec.Target != 32768 {
		t.Errorf("plan = %+v, want floor/target 32768", dec)
	}
}

func TestContextPlannerShortText(t *testing.T) {
	p := newContextPlanner(adaptiveConfig(t))
	dec, err := p.PlanContext(engine.ContextRequest{
		Phase: "diverging", Capability: "text", Demand: "short",
		Model: "m", ModelMax: 32768, Pinned: 1500, Output: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dec.Target > 8192 || dec.Floor > 8192 {
		t.Errorf("plan = %+v, want a small text window", dec)
	}
}

func TestContextPlannerRefusesTinyModel(t *testing.T) {
	p := newContextPlanner(adaptiveConfig(t))
	_, err := p.PlanContext(engine.ContextRequest{
		Phase: "diverging", Capability: "code", Demand: "repo",
		Model: "tiny", ModelMax: 4096, Pinned: 1000, Output: 2048,
	})
	if !errors.Is(err, capacity.ErrContextUnavailable) {
		t.Fatalf("err = %v, want ErrContextUnavailable", err)
	}
}
