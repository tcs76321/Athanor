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

	// Size tiers are a first signal for the everyday roles: wide is the
	// smallest capable model, main the best medium model, tall the best large
	// model. Quality can override the tier when one is clearly better.
	asc := append([]Model(nil), fitting...)
	sort.SliceStable(asc, func(i, j int) bool {
		if asc[i].SizeBytes != asc[j].SizeBytes {
			return asc[i].SizeBytes < asc[j].SizeBytes
		}
		return asc[i].Name < asc[j].Name
	})
	k := len(asc) / 3
	if k < 1 {
		k = 1
	}
	smallTier := asc[:k]
	bigTier := asc[len(asc)-k:]
	mediumTier := []Model{}
	if 2*k < len(asc) {
		mediumTier = asc[k : len(asc)-k]
	}
	bestIn := func(role string, group []Model) *Model {
		if len(group) == 0 {
			return nil
		}
		var chosen *Model
		best := -1.0
		for i := range group {
			s, ok := scores[role+"|"+group[i].Name]
			if !ok {
				s = -1
			}
			if chosen == nil || s > best || (s == best && group[i].SizeBytes > chosen.SizeBytes) {
				chosen = &group[i]
				best = s
			}
		}
		return chosen
	}
	scoreOf := func(role string, m *Model) float64 {
		if m == nil {
			return 0
		}
		if s, ok := scores[role+"|"+m.Name]; ok {
			return s
		}
		return 0
	}
	bestOverall := func(role string) *Model {
		if r := ranked(role); len(r) > 0 {
			return &r[0]
		}
		return nil
	}

	// tall: the best big model unless a medium model is clearly better; a
	// large model that loses by >0.1 is not worth the extra memory/swap.
	tall := bestIn("tall", bigTier)
	if tall == nil {
		tall = bestOverall("tall")
	}
	if alt := bestIn("tall", mediumTier); alt != nil && scoreOf("tall", alt) > scoreOf("tall", tall)+0.1 {
		tall = alt
	}
	if tall == nil {
		tall = &smallest
	}
	tallModel := *tall
	byRole["tall"] = Assignment{Model: tallModel.Name, Family: family(tallModel), Reason: reason("tall", tallModel.Name, "best large model for hard reasoning")}

	// main: the best medium model unless a big model is clearly better.
	main := bestIn("main", mediumTier)
	if main == nil {
		main = bestOverall("main")
	}
	if alt := bestIn("main", bigTier); alt != nil && scoreOf("main", alt) > scoreOf("main", main)+0.1 {
		main = alt
	}
	if main == nil {
		main = tall
	}
	mainModel := *main
	byRole["main"] = Assignment{Model: mainModel.Name, Family: family(mainModel), Reason: reason("main", mainModel.Name, "best medium model for normal execution")}

	// wide: the smallest capable model for fast ingestion.
	wide := bestIn("wide", smallTier)
	if wide == nil {
		wide = &smallest
	}
	byRole["wide"] = Assignment{Model: wide.Name, Family: family(*wide), Reason: reason("wide", wide.Name, "smallest capable model for fast ingestion")}

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
		judge = pickDifferent(ranked("security"), map[string]bool{family(tallModel): true, family(mainModel): true})
		judgeReason = "no cross-family judge available; using the largest different model"
	}
	if judge == nil {
		judge = &tallModel
		judgeReason = "only one model family installed; cross-family judgment is unavailable"
	}
	byRole["security"] = Assignment{Model: judge.Name, Family: family(*judge), Reason: judgeReason}

	// alternative: a different family from tall/main/security when possible.
	// If every family is already used, sharing the main model's family is
	// preferable to sharing the judge's family (the judge's cross-family
	// guarantee is the stronger one).
	altRanked := ranked("alternative")
	alt := pickDifferent(altRanked, map[string]bool{
		family(tallModel): true, family(mainModel): true, byRole["security"].Family: true,
	})
	altReason := "different family from main/tall/judge for an orthogonal candidate"
	if alt == nil {
		alt = pickSameFamily(altRanked, family(mainModel))
		altReason = "shares the main model's family; no unused family remains for a separate alternative"
	}
	if alt == nil {
		alt = pickDifferent(altRanked, map[string]bool{family(tallModel): true})
		altReason = "different family from the tall model for an orthogonal candidate"
	}
	if alt == nil {
		alt = &tallModel
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
