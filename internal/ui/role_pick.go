package ui

import (
	"github.com/tcs76321/athanor/internal/composition"
	"github.com/tcs76321/athanor/internal/onboard"
)

// rolePick is the qualification's baseline cast: the best small, medium, and
// big model on this machine for the three everyday roles.
type rolePick struct {
	Role    string
	Model   string
	Tier    string
	Score   float64
	SizeGiB float64
	Reason  string
	Context int
	Family  string
}

var roleCapabilities = map[string][]string{
	"wide": {"general", "document", "text"},
	"main": {"code", "general", "data"},
	"tall": {"code", "judgment", "general"},
}

// rolePicks derives the baseline cast from the measured pool using the same
// policy the assignment layer applies: main is the smallest model in the
// highest measured main tier, wide is genuinely smaller than main, and tall is
// the strongest tall model even when it is much larger.
func (u *UI) rolePicks() []rolePick {
	p := u.pool()
	if p == nil {
		return nil
	}
	profiles := make([]composition.Profile, 0)
	models := make([]onboard.Model, 0)
	byName := map[string]composition.Profile{}
	for _, prof := range p.Profiles() {
		if prof.TooLarge || len(prof.Scores) == 0 {
			continue
		}
		profiles = append(profiles, prof)
		byName[prof.Name] = prof
		models = append(models, onboard.Model{
			Name: prof.Name, Family: prof.Family, SizeBytes: profileSize(prof),
		})
	}
	if len(profiles) < 1 {
		return nil
	}
	scores := map[string]float64{}
	for _, prof := range profiles {
		overall, _, _ := overallScore(prof)
		for _, role := range []string{"wide", "main", "tall", "security", "alternative"} {
			// main follows the displayed overall ranking; the other roles use
			// their capability-specific score.
			s := roleScore(prof, role)
			if role == "main" {
				s = overall
			}
			scores[role+"|"+prof.Name] = s
		}
	}
	roles := []onboard.Role{
		{Role: "wide"}, {Role: "tall"}, {Role: "main"},
		{Role: "security"}, {Role: "alternative"},
	}
	assignments := onboard.AutoAssignWithScores(roles, models, scores, p.FitLimitBytes, 0)
	byRole := map[string]onboard.Assignment{}
	for _, a := range assignments {
		byRole[a.Role] = a
	}
	out := make([]rolePick, 0, 3)
	for _, role := range []string{"wide", "main", "tall"} {
		a, ok := byRole[role]
		if !ok {
			continue
		}
		prof, ok := byName[a.Model]
		if !ok {
			continue
		}
		tier := "medium"
		switch role {
		case "wide":
			tier = "small"
		case "tall":
			tier = "big"
		}
		out = append(out, rolePick{
			Role: role, Model: a.Model, Tier: tier,
			Score: roleScore(prof, role), SizeGiB: float64(profileSize(prof)) / (1 << 30),
			Context: prof.ContextTarget, Family: prof.Family, Reason: a.Reason,
		})
	}
	return out
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

func roleScore(p composition.Profile, role string) float64 {
	caps := roleCapabilities[role]
	total, n := 0.0, 0
	for _, c := range caps {
		if s, ok := p.Scores[c]; ok {
			total += s.Value()
			n++
		}
	}
	if n == 0 {
		// Fall back to overall/general, then any measured capability.
		if s, ok := p.Scores["general"]; ok {
			return s.Value()
		}
		for _, s := range p.Scores {
			total += s.Value()
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return total / float64(n)
}
