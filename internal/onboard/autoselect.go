package onboard

import "sort"

// Assignment is Athanor's automatic persona selection for one role.
type Assignment struct {
	Role          string
	Model         string
	Family        string
	ContextTarget int
	Reason        string
}

// AutoAssign chooses one installed model per persona. It is deterministic and
// conservative: largest-first is a capability proxy, smallest-first serves
// ingestion, and the judge/alternative roles prefer a different family so the
// system does not grade its own lineage. It never claims a cross-family judge
// when only one family is installed.
func AutoAssign(roles []Role, models []Model, availableBytes, reserveBytes int64) []Assignment {
	if len(models) == 0 {
		return nil
	}
	if len(roles) == 0 {
		roles = []Role{
			{Role: "wide"}, {Role: "tall"}, {Role: "main"},
			{Role: "security"}, {Role: "alternative"},
		}
	}
	// Prefer models that fit; if none fit (unknown/limited memory), still
	// select from the catalog rather than leaving the operator stuck.
	var fitting []Model
	for _, m := range models {
		if Fit(m, availableBytes, reserveBytes) {
			fitting = append(fitting, m)
		}
	}
	if len(fitting) == 0 {
		fitting = append([]Model(nil), models...)
	}
	sort.SliceStable(fitting, func(i, j int) bool {
		if fitting[i].SizeBytes != fitting[j].SizeBytes {
			return fitting[i].SizeBytes > fitting[j].SizeBytes
		}
		return fitting[i].Name < fitting[j].Name
	})
	smallest := fitting[len(fitting)-1]

	byRole := map[string]Assignment{}
	family := func(m Model) string { return m.Family }

	// tall: the largest model.
	tall := fitting[0]
	byRole["tall"] = Assignment{Model: tall.Name, Family: family(tall), Reason: "largest installed model for hard reasoning"}
	// main: the next distinct model, else tall.
	main := tall
	if len(fitting) > 1 {
		main = fitting[1]
	}
	byRole["main"] = Assignment{Model: main.Name, Family: family(main), Reason: "largest remaining model for normal execution"}
	// wide: the smallest model for fast ingestion.
	byRole["wide"] = Assignment{Model: smallest.Name, Family: family(smallest), Reason: "smallest installed model for fast ingestion"}

	// security: a cross-family judge over the generator roles. Reserve this
	// before alternative so the only unused family goes to the judge.
	generatorFamilies := map[string]bool{}
	for _, role := range []string{"wide", "tall", "main"} {
		if a, ok := byRole[role]; ok && a.Family != "" {
			generatorFamilies[a.Family] = true
		}
	}
	judge := pickDifferent(fitting, generatorFamilies)
	judgeReason := "largest model from a family outside the generator roles (cross-family judgment)"
	if judge == nil {
		judge = pickDifferent(fitting, map[string]bool{family(tall): true, family(main): true})
		judgeReason = "no cross-family judge available; using the largest different model"
	}
	if judge == nil {
		judge = &tall
		judgeReason = "only one model family installed; cross-family judgment is unavailable"
	}
	byRole["security"] = Assignment{Model: judge.Name, Family: family(*judge), Reason: judgeReason}

	// alternative: a different family from tall/main/security when possible.
	// If every family is already used, sharing the main model's family is
	// preferable to sharing the judge's family (the judge's cross-family
	// guarantee is the stronger one).
	alt := pickDifferent(fitting, map[string]bool{
		family(tall): true, family(main): true, byRole["security"].Family: true,
	})
	altReason := "different family from main/tall/judge for an orthogonal candidate"
	if alt == nil {
		alt = pickSameFamily(fitting, family(main))
		altReason = "shares the main model's family; no unused family remains for a separate alternative"
	}
	if alt == nil {
		alt = pickDifferent(fitting, map[string]bool{family(tall): true})
		altReason = "different family from the tall model for an orthogonal candidate"
	}
	if alt == nil {
		alt = &tall
		altReason = "no second family installed; alternative shares the tall model"
	}
	byRole["alternative"] = Assignment{Model: alt.Name, Family: family(*alt), Reason: altReason}

	out := make([]Assignment, 0, len(roles))
	for _, role := range roles {
		a, ok := byRole[role.Role]
		if !ok {
			continue
		}
		a.Role = role.Role
		a.ContextTarget = role.ContextTarget
		out = append(out, a)
	}
	return out
}

// pickDifferent returns the largest model whose family is not excluded. An
// empty family is treated as unknown and is eligible only as a last resort.
func pickDifferent(models []Model, exclude map[string]bool) *Model {
	var fallback *Model
	for i := range models {
		m := &models[i]
		if m.Family == "" {
			if fallback == nil {
				fallback = m
			}
			continue
		}
		if !exclude[m.Family] {
			return m
		}
	}
	return fallback
}

// pickSameFamily returns the largest model in the given family, if any.
func pickSameFamily(models []Model, fam string) *Model {
	for i := range models {
		if models[i].Family == fam {
			return &models[i]
		}
	}
	return nil
}
