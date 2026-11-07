package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/tcs76321/athanor/internal/capacity"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/engine"
	"github.com/tcs76321/athanor/internal/onboard"
)

// capacityContextPlanner adapts ADR-0124's pure planner to the engine's
// ContextPlanner seam. It is only active when context_policy is adaptive; the
// shipped default (archetype) leaves the legacy fixed-floor path untouched.
type capacityContextPlanner struct {
	ce      config.ContextEngine
	floors  capacity.Floors
	buckets []int
}

func newContextPlanner(cfg *config.Config) engine.ContextPlanner {
	if cfg == nil || cfg.ContextEngine.ContextPolicy != "adaptive" {
		return nil
	}
	demand := make(map[capacity.Demand]int, len(cfg.ContextEngine.DemandFloors))
	for k, v := range cfg.ContextEngine.DemandFloors {
		demand[capacity.Demand(k)] = v
	}
	return &capacityContextPlanner{
		ce: cfg.ContextEngine,
		floors: capacity.Floors{
			Planning:   cfg.ContextEngine.PlanningFloor,
			Judge:      cfg.ContextEngine.JudgeFloor,
			Demand:     demand,
			Capability: cfg.ContextEngine.CapabilityFloors,
		},
		buckets: append([]int(nil), cfg.ContextEngine.ContextBuckets...),
	}
}

// ContextFloor implements engine.FloorPlanner so model selection sees the
// adaptive floor before a target is computed.
func (p *capacityContextPlanner) ContextFloor(phase, capability, demand string) (int, bool) {
	f := capacity.FloorFor(phase, capability, capacity.Demand(demand), p.floors)
	if f <= 0 {
		return 0, false
	}
	return f, true
}

func (p *capacityContextPlanner) PlanContext(req engine.ContextRequest) (engine.ContextDecision, error) {
	demand := capacity.Demand(req.Demand)
	if demand == "" {
		demand = capacity.DemandShort
	}
	output := p.ce.OutputReserve[req.Phase]
	if output == 0 {
		output = req.Output
	}
	if req.ModelMax <= 0 {
		return engine.ContextDecision{}, fmt.Errorf("%w: model %q has no measured window", capacity.ErrContextUnavailable, req.Model)
	}
	plan, err := capacity.PlanContext(capacity.ContextInput{
		Phase: req.Phase, Capability: req.Capability, Demand: demand,
		PinnedTokens: req.Pinned, WorkingSetTokens: req.Working, OutputTokens: output,
		SafetyPercent: p.ce.SafetyMarginPercent,
		Model:         capacity.ModelFit{Name: req.Model, MaxContext: req.ModelMax},
		Buckets:       p.buckets,
		Floors:        p.floors,
	})
	if err != nil {
		return engine.ContextDecision{}, err
	}
	return engine.ContextDecision{
		Target: plan.Target, Bucket: plan.Bucket, Floor: plan.Floor,
		Demand: string(plan.Demand), Binding: plan.Binding,
	}, nil
}

// loadLaneAdmission builds lane admission from the measured audit, when one is
// available. No audit (or an old audit without a capacity report) leaves the
// aggregate cap in place.
func loadLaneAdmission(configPath string) engine.LaneAdmission {
	var raw []byte
	if env := os.Getenv("ATHANOR_MODEL_AUDIT"); env != "" {
		raw = []byte(env)
	} else {
		b, err := os.ReadFile(modelAuditPath(configPath))
		if err != nil {
			return nil
		}
		raw = b
	}
	var a onboard.Audit
	if err := json.Unmarshal(raw, &a); err != nil || a.Capacity == nil {
		return nil
	}
	return capacity.NewAdmission(*a.Capacity)
}
