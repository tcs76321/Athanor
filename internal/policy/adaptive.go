package policy

// Adaptive is F4-T2's compute policy. Easy or familiar work gets a single
// candidate and no reflection; hard or novel work gets the configured ceiling.
// It never raises compute past the ceiling (Default already clamps), so turning
// it on can only *reduce* spend.
//
// Signals, in priority order:
//  1. the planner's explicit Difficulty hint ("easy" / "hard");
//  2. prior outcome history for the task class (familiar = easy);
//  3. a criteria-count heuristic (a task with one or no criterion is small).
//
// It is pure and total: same inputs, same plan.
type Adaptive struct{}

var _ Policy = Adaptive{}

// Decide implements Policy.
func (Adaptive) Decide(in Inputs) Plan {
	plan := Default{}.Decide(in)
	if isEasy(in.Features) {
		plan.Candidates = 1
		plan.MaxReflectionLoops = 0
		// Reflection cannot run without budget, so it leaves the eligible
		// set (M8-T8). The selection seam is recomputed after the ceiling
		// changes.
		plan.Operations = eligibleOperations(plan, in.Features.OperationSamples)
	}
	return plan
}

// isEasy reports whether a task class is easy enough to skip divergence and
// reflection. The planner's hint is authoritative; absent one, a well-known
// class (>= 5 samples at >= 90% accepted) or a task with <= 1 criterion is easy.
func isEasy(f Features) bool {
	switch f.Difficulty {
	case "easy":
		return true
	case "hard":
		return false
	}
	if f.RecentSamples >= 5 && f.RecentAcceptRate >= 0.9 {
		return true
	}
	return f.CriteriaCount <= 1
}
