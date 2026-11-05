package onboard

import (
	"fmt"
	"sort"
)

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
	return AutoAssignWithScores(roles, models, nil, availableBytes, reserveBytes)
}

// AutoAssignWithScores is AutoAssign plus optional per-role qualification
// scores keyed "role|model". A measured role prefers the highest score; an
// unmeasured role falls back to size. Family constraints still apply.
func AutoAssignWithScores(roles []Role, models []Model, scores map[string]float64, availableBytes, reserveBytes int64) []Assignment {
	if len(models) == 0 {
		return nil
	}
	models = chatModels(models)
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

	// ranked returns role-specific order: measured models first by score, then
	// the unmeasured by size. An unmeasured role is exactly the old behavior.
	ranked := func(role string) []Model {
		out := append([]Model(nil), fitting...)
		if len(scores) == 0 {
			return out
		}
		measured := false
		for _, m := range out {
			if _, ok := scores[role+"|"+m.Name]; ok {
				measured = true
				break
			}
		}
		if !measured {
			return out
		}
		sort.SliceStable(out, func(i, j int) bool {
			si, iok := scores[role+"|"+out[i].Name]
			sj, jok := scores[role+"|"+out[j].Name]
			if iok != jok {
				return iok
			}
			if iok && si != sj {
				return si > sj
			}
			if out[i].SizeBytes != out[j].SizeBytes {
				return out[i].SizeBytes > out[j].SizeBytes
			}
			return out[i].Name < out[j].Name
		})
		return out
	}
	reason := func(role, model, fallback string) string {
		if s, ok := scores[role+"|"+model]; ok {
			return fmt.Sprintf("highest measured score for this role (%.2f)", s)
		}
		return fallback
	}

	byRole := map[string]Assignment{}
	family := func(m Model) string { return m.Family }

	// tall: the best measured (else largest) model.
	tallRanked := ranked("tall")
	tall := tallRanked[0]
	byRole["tall"] = Assignment{Model: tall.Name, Family: family(tall), Reason: reason("tall", tall.Name, "largest installed model for hard reasoning")}
	// main: the best model that fits. Sharing tall is preferred when no
	// role-specific measurement distinguishes them: it uses the strongest
	// generator and avoids a model swap between planning and synthesis.
	main := tall
	if rankedMain := ranked("main"); len(rankedMain) > 0 {
		main = rankedMain[0]
	}
	byRole["main"] = Assignment{Model: main.Name, Family: family(main), Reason: reason("main", main.Name, "best measured/largest model that fits for normal execution")}
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
	judge := pickDifferent(ranked("security"), generatorFamilies)
	judgeReason := "largest model from a family outside the generator roles (cross-family judgment)"
	if judge == nil {
		judge = pickDifferent(ranked("security"), map[string]bool{family(tall): true, family(main): true})
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
	altRanked := ranked("alternative")
	alt := pickDifferent(altRanked, map[string]bool{
		family(tall): true, family(main): true, byRole["security"].Family: true,
	})
	altReason := "different family from main/tall/judge for an orthogonal candidate"
	if alt == nil {
		alt = pickSameFamily(altRanked, family(main))
		altReason = "shares the main model's family; no unused family remains for a separate alternative"
	}
	if alt == nil {
		alt = pickDifferent(altRanked, map[string]bool{family(tall): true})
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
