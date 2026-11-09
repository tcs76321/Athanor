package project

import (
	"strings"
	"unicode"
)

// On-ramp values. A project is either precise enough for a single plan
// (direct) or ambiguous enough to benefit from a direction portfolio
// (explore). The value is inferred once at create and may be overridden.
const (
	OnRampDirect  = "direct"
	OnRampExplore = "explore"
)

// ValidOnRamp reports whether r is a known on-ramp. The empty string means
// "infer it" and is accepted by the repository.
func ValidOnRamp(r string) bool {
	switch r {
	case "", OnRampDirect, OnRampExplore:
		return true
	default:
		return false
	}
}

// exploratoryMarkers describe goals that are asking for direction rather than
// execution. They are deliberately few and concrete; an override always wins.
var exploratoryMarkers = []string{
	"brainstorm", "decide", "explore", "figure out", "find a", "find an",
	"ideas", "make money", "options", "portfolio", "research", "should i",
	"start a business", "what should", "which approach",
}

// InferOnRamp chooses the intake path from the goal, repository, and inferred
// capabilities. A repository-backed code project is direct: the work is
// concrete and a portfolio of directions is noise. Prose goals, and goals
// with explicit exploratory language, explore.
func InferOnRamp(goal, repositoryPath string, caps []string) string {
	lower := strings.ToLower(strings.TrimSpace(goal))
	for _, marker := range exploratoryMarkers {
		if strings.Contains(lower, marker) {
			return OnRampExplore
		}
	}
	primary := PrimaryCapability(caps)
	if strings.TrimSpace(repositoryPath) != "" && primary == CapabilityCode {
		return OnRampDirect
	}
	if strings.TrimSpace(repositoryPath) == "" &&
		(primary == CapabilityDocument || primary == CapabilityText) {
		return OnRampExplore
	}
	return OnRampDirect
}

// DeriveName builds a short human-readable project name from the goal. The
// caller resolves uniqueness; this function is deterministic.
func DeriveName(goal string) string {
	fields := strings.Fields(strings.TrimSpace(goal))
	if len(fields) > 6 {
		fields = fields[:6]
	}
	name := strings.TrimSpace(strings.Join(fields, " "))
	name = strings.Trim(name, ".,;:!?-\"'()[]{}")
	if name == "" {
		return "Project"
	}
	runes := []rune(name)
	runes[0] = unicode.ToUpper(runes[0])
	if len(runes) > 60 {
		runes = runes[:60]
	}
	return string(runes)
}
