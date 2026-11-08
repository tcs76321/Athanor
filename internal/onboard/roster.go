package onboard

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Evidence is a bounded success estimate for one capability. Successes and
// Trials are the grounded qualification or job outcomes behind it.
type Evidence struct {
	Successes int `json:"successes"`
	Trials    int `json:"trials"`
}

// Value is the Laplace-smoothed success rate.
func (e Evidence) Value() float64 {
	if e.Trials <= 0 {
		return 0
	}
	return float64(e.Successes+1) / float64(e.Trials+2)
}

// Lower is the 95% Wilson lower bound. Selection weights it so a model with a
// few lucky cases does not outrank a better-sampled one.
func (e Evidence) Lower() float64 {
	if e.Trials <= 0 {
		return 0
	}
	return wilsonLower(e.Successes, e.Trials)
}

// RosterModel is the evidence the roster policy needs for one model.
type RosterModel struct {
	Name            string
	Family          string
	SizeBytes       int64
	ContextTarget   int
	TokensPerSecond float64
	// Overall is the displayed overall score. When zero, the mean of the
	// measured capabilities is used.
	Overall      float64
	Capabilities map[string]Evidence
}

// RosterAlternative is one runner-up for a functional role.
type RosterAlternative struct {
	Model   string
	Quality float64
	Reason  string
}

// RosterPick is one functional assignment and the evidence behind it.
type RosterPick struct {
	Role            string
	Model           string
	Family          string
	SizeBytes       int64
	ContextTarget   int
	TokensPerSecond float64
	Quality         float64
	Confidence      float64
	Samples         int
	Reason          string
	Alternatives    []RosterAlternative
}

// RosterLeader is the best and runner-up for one capability.
type RosterLeader struct {
	Capability      string
	Model           string
	Family          string
	Score           float64
	Confidence      float64
	Samples         int
	ContextTarget   int
	TokensPerSecond float64
	RunnerUp        string
	RunnerUpScore   float64
	Gap             float64
	GapLabel        string
}

// RosterExperiment is a model worth trying next to reduce uncertainty or test a
// complementary family.
type RosterExperiment struct {
	Model      string
	Family     string
	Quality    float64
	Confidence float64
	Samples    int
	Reason     string
}

// Roster is the functional starting cast plus the capability leaders and
// exploration candidates derived from the same evidence.
type Roster struct {
	Picks       []RosterPick
	Leaders     []RosterLeader
	Experiments []RosterExperiment
}

type rosterMetrics struct {
	model      RosterModel
	quality    float64
	confidence float64
	samples    int
	context    float64
	speed      float64
	cost       float64
	caps       map[string]float64
}

// BuildRoster chooses the five functional roles from measured evidence. It is
// deterministic: the same evidence and fit budget always produce the same
// roster. maxBytes is the model fit limit; when zero it falls back to the
// largest model in the pool.
func BuildRoster(models []RosterModel, maxBytes int64) Roster {
	if len(models) == 0 {
		return Roster{}
	}
	var largest int64
	for _, m := range models {
		if m.SizeBytes > largest {
			largest = m.SizeBytes
		}
	}
	if maxBytes <= 0 || maxBytes < largest {
		maxBytes = largest
	}
	metrics := make([]rosterMetrics, 0, len(models))
	for _, m := range models {
		metrics = append(metrics, measureModel(m, maxBytes))
	}
	if len(metrics) == 0 {
		return Roster{}
	}
	roster := Roster{}
	used := map[string]bool{}
	usedFamilies := map[string]bool{}

	pick := func(role string, chosen rosterMetrics, reason string, utility func(rosterMetrics) float64, candidates []rosterMetrics) {
		alts := topAlternatives(chosen, candidates, utility, 2)
		used[chosen.model.Name] = true
		if chosen.model.Family != "" {
			usedFamilies[chosen.model.Family] = true
		}
		out := RosterPick{
			Role: role, Model: chosen.model.Name, Family: chosen.model.Family,
			SizeBytes: chosen.model.SizeBytes, ContextTarget: chosen.model.ContextTarget,
			TokensPerSecond: chosen.model.TokensPerSecond,
			Quality:         chosen.quality, Confidence: chosen.confidence, Samples: chosen.samples,
			Reason: reason, Alternatives: alts,
		}
		roster.Picks = append(roster.Picks, out)
	}

	// 1. Architect (tall): the best measured quality first, then trusted
	// depth and context. A one-point quality band keeps a specialist with a
	// missing capability from outranking the overall leader.
	hard := func(m rosterMetrics) float64 {
		return capCoverageAvg(m, "planning", "calibration", "repair", "judgment", "code")
	}
	tallUtility := func(m rosterMetrics) float64 {
		return 0.50*m.quality + 0.20*hard(m) + 0.15*m.context + 0.10*m.confidence + 0.05*m.speed
	}
	bestTall := 0.0
	for _, m := range metrics {
		if m.quality > bestTall {
			bestTall = m.quality
		}
	}
	tallCandidates := make([]rosterMetrics, 0, len(metrics))
	for _, m := range metrics {
		if withinDisplayPoints(m.quality, bestTall, 1) {
			tallCandidates = append(tallCandidates, m)
		}
	}
	if len(tallCandidates) == 0 {
		tallCandidates = metrics
	}
	tall := chooseBest(tallCandidates, tallUtility)
	pick("tall", tall, fmt.Sprintf("%.0f%% measured quality, %d samples, %d ctx; strongest trusted depth on this machine.",
		tall.quality*100, tall.samples, tall.model.ContextTarget), tallUtility, metrics)

	var main rosterMetrics
	if len(metrics) > 1 {
		rest := without(metrics, tall.model.Name)
		// A worker floor scaled to the architect keeps a tiny scout from
		// becoming the everyday worker on a large-model machine.
		floor := int64(float64(tall.model.SizeBytes) * 0.35)
		worker := filterSize(rest, floor, 0)
		if len(worker) == 0 {
			worker = rest
		}
		// Stay within two measured points of the best remaining model. The
		// displayed score is rounded, so a model reading one point lower can
		// be within this band; a meaningful resident-cost saving then wins.
		bestQuality := 0.0
		for _, m := range worker {
			if m.quality > bestQuality {
				bestQuality = m.quality
			}
		}
		band := make([]rosterMetrics, 0, len(worker))
		for _, m := range worker {
			if withinDisplayPoints(m.quality, bestQuality, 1) {
				band = append(band, m)
			}
		}
		if len(band) > 0 {
			worker = band
		}
		mainUtility := func(m rosterMetrics) float64 {
			return 0.40*m.quality + 0.20*m.speed + 0.10*m.context + 0.20*m.cost + 0.10*m.confidence
		}
		main = chooseBest(worker, mainUtility)
		pick("main", main, fmt.Sprintf("%.0f%% measured quality at %.1f GiB; best worker value after the architect, with resident cost counted.",
			main.quality*100, float64(main.model.SizeBytes)/(1<<30)), mainUtility, rest)
	}

	// 2. Scout (wide): meaningfully smaller than the worker, fast and cheap.
	if len(metrics) > 2 {
		rest := without(metrics, tall.model.Name, main.model.Name)
		wideUtility := func(m rosterMetrics) float64 {
			return 0.18*m.quality + 0.28*m.speed + 0.20*m.cost + 0.10*m.context + 0.19*m.confidence + 0.05*independence(m, usedFamilies)
		}
		small := filterSize(rest, 0, int64(float64(main.model.SizeBytes)*0.65))
		if len(small) == 0 {
			small = filterSize(rest, 0, int64(float64(main.model.SizeBytes)*0.85))
		}
		if len(small) == 0 {
			small = rest
		}
		scout := chooseBestQualityFloor(small, wideUtility, main.quality-0.05)
		pick("wide", scout, fmt.Sprintf("%.0f%% measured quality at %.1f GiB (%.0f%% of the worker's size); fast, cheap and still capable.",
			scout.quality*100, float64(scout.model.SizeBytes)/(1<<30),
			100*float64(scout.model.SizeBytes)/float64(maxInt64(main.model.SizeBytes, 1))), wideUtility, rest)
	}

	// 3. Screener (security): cross-family security and judgment.
	rest := without(metrics, usedList(used)...)
	securityCandidates := rest
	if cross := filterUnusedFamily(rest, usedFamilies); len(cross) > 0 {
		securityCandidates = cross
	}
	if len(securityCandidates) > 0 {
		securityUtility := func(m rosterMetrics) float64 {
			return 0.30*capOr(m, "security") + 0.20*capOr(m, "judgment") + 0.10*capOr(m, "instruction") +
				0.20*m.confidence + 0.20*independence(m, usedFamilies)
		}
		screener := chooseBest(securityCandidates, securityUtility)
		reason := fmt.Sprintf("%.0f%% security, %.0f%% judgment, %d samples; %s.",
			capOr(screener, "security")*100, capOr(screener, "judgment")*100, screener.samples,
			independenceReason(screener, usedFamilies))
		pick("security", screener, reason, securityUtility, rest)
	}

	// 4. Second opinion (alternative): a different family and a useful
	// uncertainty reduction.
	rest = without(metrics, usedList(used)...)
	altCandidates := rest
	if cross := filterUnusedFamily(rest, usedFamilies); len(cross) > 0 {
		altCandidates = cross
	}
	if len(altCandidates) > 0 {
		altUtility := func(m rosterMetrics) float64 {
			return 0.30*independence(m, usedFamilies) + 0.25*(1-m.confidence) + 0.25*m.quality +
				0.10*m.context + 0.10*m.speed
		}
		alt := chooseBestQualityFloor(altCandidates, altUtility, main.quality-0.04)
		reason := fmt.Sprintf("%.0f%% measured quality, %d samples; %s. Automatic exploration will use it on low-risk work.",
			alt.quality*100, alt.samples, independenceReason(alt, usedFamilies))
		pick("alternative", alt, reason, altUtility, rest)
	}

	// Fill any missing function so all five roles are represented, even on a
	// one- or two-model pool. Sharing is explicit, not silent.
	have := map[string]bool{}
	for _, p := range roster.Picks {
		have[p.Role] = true
	}
	base := tall
	if main.model.Name != "" {
		base = main
	}
	for _, role := range []string{"main", "wide", "security", "alternative"} {
		if have[role] {
			continue
		}
		reason := "only one model is installed; this function shares the best available model."
		if role == "wide" && main.model.Name != "" {
			reason = "no meaningfully smaller model is installed; the scout shares the worker."
		} else if (role == "security" || role == "alternative") && len(metrics) > 1 {
			reason = "no independent family is available; this function shares a measured model."
		}
		roster.Picks = append(roster.Picks, RosterPick{
			Role: role, Model: base.model.Name, Family: base.model.Family,
			SizeBytes: base.model.SizeBytes, ContextTarget: base.model.ContextTarget,
			TokensPerSecond: base.model.TokensPerSecond, Quality: base.quality,
			Confidence: base.confidence, Samples: base.samples, Reason: reason,
		})
	}
	order := map[string]int{"main": 0, "tall": 1, "wide": 2, "security": 3, "alternative": 4}
	sort.SliceStable(roster.Picks, func(i, j int) bool {
		return order[roster.Picks[i].Role] < order[roster.Picks[j].Role]
	})

	roster.Leaders = buildLeaders(metrics)
	roster.Experiments = buildExperiments(metrics, used, main, roster.Leaders)
	return roster
}

func measureModel(m RosterModel, maxBytes int64) rosterMetrics {
	caps := map[string]float64{}
	trials := 0
	lowerWeighted := 0.0
	for key, e := range m.Capabilities {
		if e.Trials <= 0 {
			continue
		}
		caps[key] = e.Value()
		trials += e.Trials
		lowerWeighted += e.Lower() * float64(e.Trials)
	}
	quality := 0.5
	if len(caps) > 0 {
		total := 0.0
		for _, v := range caps {
			total += v
		}
		quality = total / float64(len(caps))
	}
	if m.Overall > 0 {
		quality = m.Overall
	}
	// Trust is the sample-weighted average of the per-capability Wilson lower
	// bounds, capped by the displayed quality. It reflects the weaker,
	// sparse capabilities without letting a 3-case capability dominate.
	confidence := 0.0
	if trials > 0 {
		confidence = math.Min(quality, lowerWeighted/float64(trials))
	}
	context := 0.0
	if m.ContextTarget > 0 {
		context = math.Min(1, float64(m.ContextTarget)/32768)
	}
	speed := 0.5
	if m.TokensPerSecond > 0 {
		speed = math.Min(1, m.TokensPerSecond/60)
	}
	cost := 0.5
	if maxBytes > 0 && m.SizeBytes > 0 {
		cost = 1 - math.Min(1, float64(m.SizeBytes)/float64(maxBytes))
	}
	return rosterMetrics{
		model: m, quality: quality, confidence: confidence,
		samples: trials, context: context, speed: speed, cost: cost, caps: caps,
	}
}

func wilsonLower(successes, trials int) float64 {
	if trials <= 0 {
		return 0
	}
	n := float64(trials)
	p := float64(successes) / n
	const z = 1.96
	denom := 1 + z*z/n
	centre := p + z*z/(2*n)
	margin := z * math.Sqrt(p*(1-p)/n+z*z/(4*n*n))
	v := (centre - margin) / denom
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func withinDisplayPoints(value, best float64, points int) bool {
	d := int(math.Round(value*100)) - int(math.Round(best*100))
	if d < 0 {
		d = -d
	}
	return d <= points
}

func capCoverageAvg(m rosterMetrics, names ...string) float64 {
	total := 0.0
	for _, name := range names {
		if v, ok := m.caps[name]; ok {
			total += v
		} else {
			// Missing coverage counts as the model's overall quality, not as
			// an absent requirement.
			total += m.quality
		}
	}
	if len(names) == 0 {
		return m.quality
	}
	return total / float64(len(names))
}

func capOr(m rosterMetrics, name string) float64 {
	if v, ok := m.caps[name]; ok {
		return v
	}
	return m.quality
}

func independence(m rosterMetrics, used map[string]bool) float64 {
	if m.model.Family == "" {
		return 0.5
	}
	if used[m.model.Family] {
		return 0
	}
	return 1
}

func independenceReason(m rosterMetrics, used map[string]bool) string {
	if m.model.Family == "" {
		return "family unknown"
	}
	if used[m.model.Family] {
		return "shares a family with an existing role; no unused family remains"
	}
	return "different family from the existing roles"
}

func chooseBest(models []rosterMetrics, utility func(rosterMetrics) float64) rosterMetrics {
	best := models[0]
	bestScore := utility(best)
	for _, m := range models[1:] {
		s := utility(m)
		if s > bestScore+1e-9 || (math.Abs(s-bestScore) <= 1e-9 && betterTie(m, best)) {
			best, bestScore = m, s
		}
	}
	return best
}

func chooseBestQualityFloor(models []rosterMetrics, utility func(rosterMetrics) float64, floor float64) rosterMetrics {
	filtered := make([]rosterMetrics, 0, len(models))
	for _, m := range models {
		if m.quality >= floor {
			filtered = append(filtered, m)
		}
	}
	if len(filtered) == 0 {
		filtered = models
	}
	return chooseBest(filtered, utility)
}

func betterTie(a, b rosterMetrics) bool {
	if math.Abs(a.quality-b.quality) > 1e-9 {
		return a.quality > b.quality
	}
	if a.model.ContextTarget != b.model.ContextTarget {
		return a.model.ContextTarget > b.model.ContextTarget
	}
	if a.model.SizeBytes != b.model.SizeBytes {
		return a.model.SizeBytes < b.model.SizeBytes
	}
	return a.model.Name < b.model.Name
}

func without(models []rosterMetrics, names ...string) []rosterMetrics {
	skip := map[string]bool{}
	for _, name := range names {
		skip[name] = true
	}
	out := make([]rosterMetrics, 0, len(models))
	for _, m := range models {
		if !skip[m.model.Name] {
			out = append(out, m)
		}
	}
	return out
}

func filterSize(models []rosterMetrics, min, max int64) []rosterMetrics {
	out := make([]rosterMetrics, 0, len(models))
	for _, m := range models {
		if min > 0 && m.model.SizeBytes < min {
			continue
		}
		if max > 0 && m.model.SizeBytes > max {
			continue
		}
		out = append(out, m)
	}
	return out
}

func filterUnusedFamily(models []rosterMetrics, used map[string]bool) []rosterMetrics {
	out := make([]rosterMetrics, 0, len(models))
	for _, m := range models {
		if m.model.Family != "" && !used[m.model.Family] {
			out = append(out, m)
		}
	}
	return out
}

func usedList(used map[string]bool) []string {
	out := make([]string, 0, len(used))
	for name := range used {
		out = append(out, name)
	}
	return out
}

func topAlternatives(chosen rosterMetrics, candidates []rosterMetrics, utility func(rosterMetrics) float64, n int) []RosterAlternative {
	type scored struct {
		m rosterMetrics
		s float64
	}
	ranked := make([]scored, 0, len(candidates))
	for _, m := range candidates {
		if m.model.Name == chosen.model.Name {
			continue
		}
		ranked = append(ranked, scored{m: m, s: utility(m)})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if math.Abs(ranked[i].s-ranked[j].s) > 1e-9 {
			return ranked[i].s > ranked[j].s
		}
		return betterTie(ranked[i].m, ranked[j].m)
	})
	if len(ranked) > n {
		ranked = ranked[:n]
	}
	out := make([]RosterAlternative, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, RosterAlternative{
			Model:   r.m.model.Name,
			Quality: r.m.quality,
			Reason:  fmt.Sprintf("%.0f%% measured quality", r.m.quality*100),
		})
	}
	return out
}

func buildLeaders(models []rosterMetrics) []RosterLeader {
	out := make([]RosterLeader, 0, len(qualificationCapabilityOrder))
	for _, capability := range qualificationCapabilityOrder {
		candidates := make([]rosterMetrics, 0, len(models))
		for _, m := range models {
			if _, ok := m.caps[capability]; ok {
				candidates = append(candidates, m)
			}
		}
		if len(candidates) == 0 {
			continue
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			li, lj := candidates[i].capLower(capability), candidates[j].capLower(capability)
			if math.Abs(li-lj) > 1e-9 {
				return li > lj
			}
			vi, vj := candidates[i].caps[capability], candidates[j].caps[capability]
			if math.Abs(vi-vj) > 1e-9 {
				return vi > vj
			}
			return candidates[i].model.Name < candidates[j].model.Name
		})
		leader := candidates[0]
		row := RosterLeader{
			Capability: capability, Model: leader.model.Name, Family: leader.model.Family,
			Score: leader.caps[capability], Confidence: leader.capLower(capability),
			Samples: leader.capTrials(capability), ContextTarget: leader.model.ContextTarget,
			TokensPerSecond: leader.model.TokensPerSecond,
		}
		if len(candidates) > 1 {
			row.RunnerUp = candidates[1].model.Name
			row.RunnerUpScore = candidates[1].caps[capability]
			if gap := row.Score - row.RunnerUpScore; gap > 0 {
				row.Gap = gap
				switch {
				case gap <= 0.0005:
					row.GapLabel = "tie"
				case gap*100 < 0.5:
					row.GapLabel = "<1 pt"
				default:
					row.GapLabel = fmt.Sprintf("%.0f pts", gap*100)
				}
			} else {
				row.GapLabel = "tie"
			}
		}
		out = append(out, row)
	}
	return out
}

func (m rosterMetrics) capLower(capability string) float64 {
	if m.model.Capabilities == nil {
		return 0
	}
	e, ok := m.model.Capabilities[capability]
	if !ok {
		return 0
	}
	return e.Lower()
}

func (m rosterMetrics) capTrials(capability string) int {
	if m.model.Capabilities == nil {
		return 0
	}
	return m.model.Capabilities[capability].Trials
}

func buildExperiments(models []rosterMetrics, used map[string]bool, main rosterMetrics, leaders []RosterLeader) []RosterExperiment {
	top2 := map[string]int{}
	capsFor := map[string][]string{}
	add := func(model, capability string) {
		if model == "" {
			return
		}
		top2[model]++
		capsFor[model] = append(capsFor[model], capability)
	}
	for _, leader := range leaders {
		add(leader.Model, leader.Capability)
		add(leader.RunnerUp, leader.Capability)
	}
	candidates := make([]rosterMetrics, 0, len(models))
	for _, m := range models {
		if used[m.model.Name] {
			continue
		}
		if main.samples > 0 && m.quality < main.quality-0.05 {
			continue
		}
		candidates = append(candidates, m)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		ci, cj := top2[candidates[i].model.Name], top2[candidates[j].model.Name]
		if ci != cj {
			return ci > cj
		}
		ui := 1 - candidates[i].confidence
		uj := 1 - candidates[j].confidence
		if math.Abs(ui-uj) > 1e-9 {
			return ui > uj
		}
		if math.Abs(candidates[i].quality-candidates[j].quality) > 1e-9 {
			return candidates[i].quality > candidates[j].quality
		}
		return candidates[i].model.Name < candidates[j].model.Name
	})
	if len(candidates) > 2 {
		candidates = candidates[:2]
	}
	out := make([]RosterExperiment, 0, len(candidates))
	for _, m := range candidates {
		family := "different family"
		if m.model.Family == "" {
			family = "family unknown"
		}
		reason := fmt.Sprintf("high uncertainty: %d samples, confidence %.0f%%; %s.",
			m.samples, m.confidence*100, family)
		if caps := capsFor[m.model.Name]; len(caps) > 0 {
			if len(caps) > 4 {
				reason = fmt.Sprintf("top-2 in %s and %d more; %s.",
					strings.Join(caps[:3], ", "), len(caps)-3, family)
			} else {
				reason = fmt.Sprintf("top-2 in %s; %s.", strings.Join(caps, ", "), family)
			}
		}
		out = append(out, RosterExperiment{
			Model: m.model.Name, Family: m.model.Family, Quality: m.quality,
			Confidence: m.confidence, Samples: m.samples, Reason: reason,
		})
	}
	return out
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
