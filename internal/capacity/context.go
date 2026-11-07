package capacity

import (
	"errors"
	"fmt"
)

// Context planning is the per-job half of the capacity plan. It answers one
// question: what num_ctx does this phase of this job need, given its demand,
// the model's measured maximum, the pinned prompt sections, the working set,
// and the output reserve?
//
// It never lowers a floor. If the model cannot meet the floor, the caller must
// choose another model or pause with a recommendation.

// ErrContextUnavailable reports a job that cannot fit its floor or pinned
// content in the selected model. It is a typed error so the engine can pause
// rather than silently truncate.
var ErrContextUnavailable = errors.New("capacity: context unavailable")

// DemandInput is the deterministic demand signal available before the planner
// runs. Hint is the planner's CONTEXT: marker, when present.
type DemandInput struct {
	Capability       string
	HasRepository    bool
	DescriptionRunes int
	CriteriaRunes    int
	PriorMemory      bool
	SourceCount      int
	Hint             Demand
}

// InferDemand derives the demand tier. A hint may raise demand, never lower it.
func InferDemand(in DemandInput) Demand {
	det := DemandShort
	switch {
	case in.HasRepository && in.Capability == "code":
		det = DemandRepo
	case in.PriorMemory:
		det = DemandMemory
	case in.DescriptionRunes+in.CriteriaRunes > 1200,
		in.SourceCount > 0 && (in.Capability == "document" || in.Capability == "text"):
		det = DemandLong
	}
	if rank(in.Hint) > rank(det) {
		return in.Hint
	}
	return det
}

func rank(d Demand) int {
	switch d {
	case DemandShort:
		return 1
	case DemandMemory, DemandLong:
		return 2
	case DemandRepo:
		return 3
	default:
		return 0
	}
}

// ContextInput is one phase's planning request.
type ContextInput struct {
	Phase            string
	Capability       string
	Demand           Demand
	PinnedTokens     int
	WorkingSetTokens int
	OutputTokens     int
	SafetyPercent    int
	Model            ModelFit
	Buckets          []int
	Floors           Floors
}

// ContextPlan is the audited decision.
type ContextPlan struct {
	Phase      string
	Capability string
	Demand     Demand
	Floor      int
	Need       int
	Safety     int
	Target     int
	Bucket     int
	ModelMax   int
	Model      string
	Binding    string
}

// PlanContext computes one phase's window. One bucket is pinned per model and
// occupancy window, so callers should compute the plan once at the start of a
// job/model residency and reuse the bucket across phases (only the output
// reserve changes).
func PlanContext(in ContextInput) (ContextPlan, error) {
	if in.Model.Name == "" || in.Model.MaxContext <= 0 {
		return ContextPlan{}, fmt.Errorf("%w: no model or measured max context", ErrContextUnavailable)
	}
	buckets := in.Buckets
	if len(buckets) == 0 {
		buckets = []int{4096, 8192, 16384, 32768}
	}
	safetyPct := in.SafetyPercent
	if safetyPct <= 0 {
		safetyPct = 15
	}
	floor := phaseFloor(in)
	if floor <= 0 {
		return ContextPlan{}, fmt.Errorf("%w: no floor for phase %q", ErrContextUnavailable, in.Phase)
	}
	if floor > in.Model.MaxContext {
		return ContextPlan{}, fmt.Errorf(
			"%w: floor %d exceeds the model's measured max %d for phase %q",
			ErrContextUnavailable, floor, in.Model.MaxContext, in.Phase)
	}
	subtotal := in.PinnedTokens + in.WorkingSetTokens + in.OutputTokens
	safety := subtotal * safetyPct / 100
	need := subtotal + safety
	if need > in.Model.MaxContext {
		return ContextPlan{}, fmt.Errorf(
			"%w: need %d tokens exceeds the model's measured max %d (pinned %d + working %d + output %d + safety %d)",
			ErrContextUnavailable, need, in.Model.MaxContext,
			in.PinnedTokens, in.WorkingSetTokens, in.OutputTokens, safety)
	}
	target := bucketUp(need, buckets)
	if target < floor {
		target = floor
	}
	// A bucket may exceed the model's measured max even when the exact need
	// does not. In that case the exact target (<= max) is the valid window.
	if target > in.Model.MaxContext {
		target = in.Model.MaxContext
	}
	if target < floor {
		return ContextPlan{}, fmt.Errorf(
			"%w: floor %d exceeds the model's measured max %d for phase %q",
			ErrContextUnavailable, floor, in.Model.MaxContext, in.Phase)
	}
	if target > in.Model.MaxContext {
		return ContextPlan{}, fmt.Errorf(
			"%w: target %d exceeds the model's measured max %d (pinned %d + working %d + output %d + safety %d)",
			ErrContextUnavailable, target, in.Model.MaxContext,
			in.PinnedTokens, in.WorkingSetTokens, in.OutputTokens, safety)
	}
	// Pinned content is never evicted; an output reserve that leaves no room
	// for it is not a plan.
	if in.PinnedTokens+in.OutputTokens > in.Model.MaxContext {
		return ContextPlan{}, fmt.Errorf(
			"%w: pinned %d + output %d exceeds the model's max %d",
			ErrContextUnavailable, in.PinnedTokens, in.OutputTokens, in.Model.MaxContext)
	}
	return ContextPlan{
		Phase: in.Phase, Capability: in.Capability, Demand: in.Demand,
		Floor: floor, Need: need, Safety: safety, Target: target,
		Bucket: target, ModelMax: in.Model.MaxContext, Model: in.Model.Name,
		Binding: fmt.Sprintf("%s floor, %s demand, %d pinned + %d working + %d output + %d safety",
			in.Phase, in.Demand, in.PinnedTokens, in.WorkingSetTokens, in.OutputTokens, safety),
	}, nil
}

// phaseFloor applies the phase-aware rule: planning and judgment do not inherit
// the repo/demand floor; generation and synthesis do.
func phaseFloor(in ContextInput) int {
	return FloorFor(in.Phase, in.Capability, in.Demand, in.Floors)
}

// FloorFor is the exported floor rule used by model selection before a target
// is known. It is model-independent: the caller then chooses a model whose
// measured max is at least this value.
func FloorFor(phase, capability string, demand Demand, floors Floors) int {
	capFloor := floors.Capability[capability]
	switch phase {
	case "planning":
		return maxInt(floors.Planning, capFloor)
	case "evaluating", "comparing", "reflecting":
		return maxInt(floors.Judge, capFloor)
	case "diverging", "synthesizing":
		return maxInt(floors.Demand[demand], capFloor)
	default:
		if d := floors.Demand[demand]; d > 0 {
			return maxInt(d, capFloor)
		}
		return capFloor
	}
}

// EffectiveWorkingSet bounds the working-set pack by the room left after the
// pinned tiers and the output reserve. Returning 0 means "skip the pack"; a
// chunk is never sliced to fit.
func EffectiveWorkingSet(target, pinned, output int) int {
	room := target - pinned - output
	if room <= 0 {
		return 0
	}
	// Keep at most 40% of the free room for full-fidelity bodies; the rest is
	// corrections, episodic memory, the Dormant Index, and generation headroom.
	return room * 40 / 100
}

// EffectiveRepositoryIndexLimit scales the Dormant Index row count with the
// window: small contexts get fewer summaries, large contexts keep the cap.
func EffectiveRepositoryIndexLimit(target, configured int) int {
	if configured <= 0 {
		return 0
	}
	byWindow := target / 2048
	if byWindow < 4 {
		byWindow = 4
	}
	if byWindow < configured {
		return byWindow
	}
	return configured
}

func bucketUp(n int, buckets []int) int {
	if n <= 0 {
		return 0
	}
	best := 0
	for _, b := range buckets {
		if b >= n {
			if best == 0 || b < best {
				best = b
			}
		}
	}
	if best == 0 {
		// The need exceeds every configured bucket; return the largest so the
		// caller's model-max check reports the real overflow.
		for _, b := range buckets {
			if b > best {
				best = b
			}
		}
	}
	return best
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
