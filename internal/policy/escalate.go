package policy

// Escalate applies the fixed escalation ladder to a base plan for a retry
// (ADR-0074 §3). It is the shipped repertoire; a learned selector (a later
// step) chooses *around* it, but this ladder is always present and is the
// baseline the selector is measured against.
//
// The rungs, in order, are applied cumulatively — attempt 2 reaches the first,
// attempt 3 the second, and so on:
//
//	attempt 2  raise the phase wall-time budget (1.5×, up to 2.5×)
//	attempt 3  raise candidates to the hard ceiling
//	attempt 4  raise the reflection budget to its ceiling
//	attempt 5  swap the generator/synthesizer to the `alternative` persona
//	attempt 6  raise the generation temperature a little
//	attempt 7  add a retry directive to the prompt
//
// The **first attempt is never escalated**, so a first-pass benchmark is
// unaffected. Every rung only raises *toward* a configured bound (the plan
// never exceeds `Limits`; budgets only grow), leaving the task's §7.3 budget as
// the true ceiling. It is pure and total.
func Escalate(plan Plan, attempt int, in Inputs) Plan {
	if attempt <= 1 {
		return plan
	}
	if s := budgetScaleFor(attempt); s > plan.BudgetScale {
		plan.BudgetScale = s
	}
	rungs := []func(Plan, Inputs) Plan{moreCandidates, moreReflection, swapModel, bumpTemperature, addDirective}
	// Rung i applies from attempt i+3 (attempt 3 = rung 0).
	for i := 0; i < len(rungs) && i <= attempt-3; i++ {
		plan = rungs[i](plan, in)
	}
	// The reflection budget may have changed, so the eligible operation set
	// does too (eligibleOperations drops reflect when its budget is zero).
	plan.Operations = eligibleOperations(plan, in.Features.OperationSamples)
	return plan
}

// budgetScaleFor is the wall-time budget multiplier for an attempt: 1.5× at
// attempt 2, +0.5× per attempt, capped at 2.5×.
func budgetScaleFor(attempt int) float64 {
	if attempt <= 1 {
		return 0
	}
	s := 1 + 0.5*float64(attempt-1)
	if s > 2.5 {
		s = 2.5
	}
	return s
}

// retryTemperatureDelta is the generation temperature raise the ladder applies
// at attempt 6. It is a small nudge, not a reset.
const retryTemperatureDelta = 0.2

// retryDirective is the prompt rung: a bounded instruction telling the model
// this is a retry and to change approach.
const retryDirective = "This is a retry: earlier attempts failed the acceptance checks. " +
	"Re-read the criteria and take a materially different approach rather than rephrasing the previous one."

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

// swapModel routes generation and synthesis to the `alternative` persona so a
// retry draws from a different model when one is configured. It is a no-op when
// `alternative` is the same model as `main`.
func swapModel(p Plan, _ Inputs) Plan {
	if p.ModelRouting == nil {
		p.ModelRouting = map[string]string{}
	}
	p.ModelRouting[PhaseDiverging] = RoleAlternative
	p.ModelRouting[PhaseSynthesizing] = RoleAlternative
	return p
}

// bumpTemperature raises the generation temperature by a small delta. It never
// lowers it.
func bumpTemperature(p Plan, _ Inputs) Plan {
	if p.TemperatureDelta < retryTemperatureDelta {
		p.TemperatureDelta = retryTemperatureDelta
	}
	return p
}

// addDirective sets the retry prompt directive. It never shortens it.
func addDirective(p Plan, _ Inputs) Plan {
	if p.Directive == "" {
		p.Directive = retryDirective
	}
	return p
}
