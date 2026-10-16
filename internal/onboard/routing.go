package onboard

import (
	"fmt"
	"sort"
)

// Measurement is one model's measured result for a role (M9-T2b). Higher Score
// is better for both role classes: the deterministic pass rate for a generator
// role, the anchor agreement (Spearman) for the judge role.
type Measurement struct {
	Model   string  `json:"model"`
	Role    string  `json:"role"`
	Score   float64 `json:"score"`
	Samples int     `json:"samples,omitempty"`
}

// RoutingOptions parameterise Route.
type RoutingOptions struct {
	// JudgeRole is the role the cross-family rule applies to (typically
	// "security"). Empty disables the rule.
	JudgeRole string
	// GeneratorRoles are the roles the judge must differ from in family, so a
	// correlated judge (same lineage as a generator) is never recommended.
	GeneratorRoles []string
	// MinImprovement is how far a candidate must beat the configured model to
	// replace it. Probe noise is real, so a small positive floor is expected.
	MinImprovement float64
	// MinSamples is the minimum number of samples a candidate measurement must
	// have before it can *replace* the configured model. A single-pass pass@k is
	// noisy — the M9 soak passed 6/6 then 0/2 on the same tasks — so a low-sample
	// candidate is reported but never swaps the incumbent. Zero disables the guard.
	MinSamples int
}

// Recommendation is the routing proposal for one role. It is advisory: Route
// never mutates config (ADR-0049; the proposal is for HITL approval).
type Recommendation struct {
	Role        string `json:"role"`
	Configured  string `json:"configured"`
	Recommended string `json:"recommended"`
	Replace     bool   `json:"replace"`
	// Score is the recommended model's measured score (0 when unmeasured).
	Score  float64 `json:"score,omitempty"`
	Reason string  `json:"reason"`
}

// Route produces a routing proposal from per-role measurements. `families` maps
// a model name to its lineage and is consulted only for the judge cross-family
// rule; a judge candidate whose family is unknown or matches a generator family
// is excluded, so Route never recommends a judge it cannot prove is
// cross-family (the engine's conservative posture).
func Route(roles []Role, families map[string]string, ms []Measurement, opts RoutingOptions) []Recommendation {
	best := map[string]map[string]Measurement{}
	for _, m := range ms {
		if best[m.Role] == nil {
			best[m.Role] = map[string]Measurement{}
		}
		if cur, ok := best[m.Role][m.Model]; !ok || m.Score > cur.Score {
			best[m.Role][m.Model] = m
		}
	}

	genFamilies := map[string]bool{}
	for _, role := range roles {
		for _, gr := range opts.GeneratorRoles {
			if role.Role == gr {
				if f := families[role.Model]; f != "" {
					genFamilies[f] = true
				}
			}
		}
	}

	out := make([]Recommendation, 0, len(roles))
	for _, role := range roles {
		rec := Recommendation{Role: role.Role, Configured: role.Model, Recommended: role.Model}
		scores := best[role.Role]
		if len(scores) == 0 {
			rec.Reason = "no qualification data; keeping the configured model"
			out = append(out, rec)
			continue
		}

		type cand struct {
			model   string
			score   float64
			samples int
		}
		var cands []cand
		for model, meas := range scores {
			if opts.JudgeRole != "" && role.Role == opts.JudgeRole {
				// A judge whose family is unknown or matches a generator's is
				// not a *verifiable* cross-family judge; exclude it, matching the
				// engine's conservative runtime posture.
				fam := families[model]
				if fam == "" || genFamilies[fam] {
					continue
				}
			}
			cands = append(cands, cand{model, meas.Score, meas.Samples})
		}
		sort.Slice(cands, func(i, j int) bool {
			if cands[i].score != cands[j].score {
				return cands[i].score > cands[j].score
			}
			return cands[i].model < cands[j].model
		})
		if len(cands) == 0 {
			rec.Reason = "no cross-family candidate measured; keeping the configured model"
			out = append(out, rec)
			continue
		}

		top := cands[0]
		rec.Score = top.score
		configured, configuredMeasured := scores[role.Model]
		switch {
		case top.model == role.Model:
			rec.Reason = fmt.Sprintf("configured model is the best measured (%.2f)", top.score)
		case top.samples < opts.MinSamples:
			rec.Reason = fmt.Sprintf("%s scored %.2f on %d sample(s) (< %d required); keeping the configured model",
				top.model, top.score, top.samples, opts.MinSamples)
		case !configuredMeasured:
			rec.Recommended = top.model
			rec.Replace = true
			rec.Reason = fmt.Sprintf("%s scored %.2f; the configured %s is unmeasured", top.model, top.score, role.Model)
		case top.score >= configured.Score+opts.MinImprovement:
			rec.Recommended = top.model
			rec.Replace = true
			rec.Reason = fmt.Sprintf("%s scored %.2f vs configured %s %.2f (margin %.2f)",
				top.model, top.score, role.Model, configured.Score, opts.MinImprovement)
		default:
			rec.Reason = fmt.Sprintf("configured %s %.2f within %.2f of the best measured %s %.2f",
				role.Model, configured.Score, opts.MinImprovement, top.model, top.score)
		}
		out = append(out, rec)
	}
	return out
}
