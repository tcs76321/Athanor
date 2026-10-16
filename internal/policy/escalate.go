package policy

// Escalate applies the fixed compute ladder to a base plan for a retry
// (ADR-0074 §3). It is the shipped repertoire: the first attempt is never
// escalated, attempt 2 raises the candidate count toward the hard ceiling,
// attempt 3 also raises the reflection budget, and later rungs (budget, model,
// temperature, prompt) land in a follow-up. Each rung only raises toward a
// configured ceiling, so the plan never exceeds `Limits`; the task's §7.3
// budget remains the only unbounded lever.
//
// It is pure and total: the same plan, attempt, and inputs give the same
// result.
func Escalate(plan Plan, attempt int, in Inputs) Plan {
	if attempt <= 1 {
		return plan
	}
	rungs := []func(Plan, Inputs) Plan{moreCandidates, moreReflection}
	for i := 0; i < len(rungs) && i <= attempt-2; i++ {
		plan = rungs[i](plan, in)
	}
	// Reflection budget may have changed, so the eligible operation set does
	// too (eligibleOperations drops reflect when its budget is zero).
	plan.Operations = eligibleOperations(plan, in.Features.OperationSamples)
	return plan
}

// moreCandidates raises the candidate count to the hard-task ceiling when there
// is headroom. It never lowers candidates.
func moreCandidates(p Plan, in Inputs) Plan {
	if in.Limits.HardCandidatesCeiling > p.Candidates {
		p.Candidates = in.Limits.HardCandidatesCeiling
	}
	return p
}

// moreReflection raises the reflection budget to its ceiling when there is
// headroom. It never lowers it.
func moreReflection(p Plan, in Inputs) Plan {
	if in.Limits.ReflectionCeiling > p.MaxReflectionLoops {
		p.MaxReflectionLoops = in.Limits.ReflectionCeiling
	}
	return p
}
