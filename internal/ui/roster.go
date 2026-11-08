package ui

import (
	"strings"

	"github.com/tcs76321/athanor/internal/composition"
	"github.com/tcs76321/athanor/internal/onboard"
)

// rosterView builds the functional starting roster, capability leaders, and
// exploration candidates from the measured pool. It uses the same evidence
// the assignment layer uses, so the UI cannot drift from the actual cast.
func (u *UI) rosterView(fitBytes int64) onboard.Roster {
	p := u.pool()
	if p == nil {
		return onboard.Roster{}
	}
	profiles := p.Profiles()
	models := make([]onboard.RosterModel, 0, len(profiles))
	for _, prof := range profiles {
		if prof.TooLarge || len(prof.Scores) == 0 {
			continue
		}
		overall, _, _ := overallScore(prof)
		capabilities := map[string]onboard.Evidence{}
		for key, score := range prof.Scores {
			if strings.HasPrefix(key, "stance:") {
				continue
			}
			capabilities[key] = onboard.Evidence{Successes: score.Successes, Trials: score.Trials}
		}
		models = append(models, onboard.RosterModel{
			Name:            prof.Name,
			Family:          onboard.InferFamily(prof.Family, prof.Name),
			SizeBytes:       profileSize(prof),
			ContextTarget:   prof.ContextTarget,
			TokensPerSecond: prof.TokensPerSecond,
			Overall:         overall,
			Capabilities:    capabilities,
		})
	}
	if len(models) == 0 {
		return onboard.Roster{}
	}
	if fitBytes <= 0 {
		fitBytes = p.FitLimitBytes
	}
	return onboard.BuildRoster(models, fitBytes)
}

func profileSize(p composition.Profile) int64 {
	if p.FileBytes > 0 {
		return p.FileBytes
	}
	if p.ResidentBytes > 0 {
		return p.ResidentBytes
	}
	return p.SizeBytes
}
