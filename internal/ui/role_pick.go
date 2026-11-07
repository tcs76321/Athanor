package ui

import (
	"fmt"
	"sort"

	"github.com/tcs76321/athanor/internal/composition"
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

// rolePicks derives the small/medium/big baseline from the measured pool.
// It is a first signal, not a permanent assignment: the selector and job
// outcomes continue to adapt after qualification.
func (u *UI) rolePicks() []rolePick {
	p := u.pool()
	if p == nil {
		return nil
	}
	profiles := make([]composition.Profile, 0)
	for _, prof := range p.Profiles() {
		if prof.TooLarge || len(prof.Scores) == 0 {
			continue
		}
		profiles = append(profiles, prof)
	}
	if len(profiles) < 1 {
		return nil
	}
	sort.SliceStable(profiles, func(i, j int) bool {
		si, sj := profileSize(profiles[i]), profileSize(profiles[j])
		if si != sj {
			return si < sj
		}
		return profiles[i].Name < profiles[j].Name
	})
	tierFor := func(i int) string {
		n := len(profiles)
		if n == 1 {
			return "single"
		}
		if n == 2 {
			if i == 0 {
				return "small"
			}
			return "big"
		}
		switch {
		case i < (n+2)/3:
			return "small"
		case i >= 2*n/3:
			return "big"
		default:
			return "medium"
		}
	}
	best := func(role, tier string) *composition.Profile {
		var chosen *composition.Profile
		chosenScore := -1.0
		for i := range profiles {
			if tierFor(i) != tier {
				continue
			}
			s := roleScore(profiles[i], role)
			if s > chosenScore {
				chosen = &profiles[i]
				chosenScore = s
			}
		}
		return chosen
	}
	out := make([]rolePick, 0, 3)
	for _, role := range []string{"wide", "main", "tall"} {
		tier := "medium"
		switch role {
		case "wide":
			tier = "small"
		case "tall":
			tier = "big"
		}
		chosen := best(role, tier)
		if chosen == nil {
			// Degenerate pool: fall back to the tier nearest the role.
			for _, t := range []string{"medium", "small", "big", "single"} {
				if chosen = best(role, t); chosen != nil {
					tier = t
					break
				}
			}
		}
		if chosen == nil {
			continue
		}
		out = append(out, rolePick{
			Role: role, Model: chosen.Name, Tier: tier,
			Score: roleScore(*chosen, role), SizeGiB: float64(profileSize(*chosen)) / (1 << 30),
			Context: chosen.ContextTarget, Family: chosen.Family,
			Reason: fmt.Sprintf("best %s-tier model for %s on this machine", tier, role),
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
