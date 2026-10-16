// Package onboard is the model-onboarding inventory (M9-T2; ADR-0049): it maps
// the configured personas to the models actually installed on the backend and
// reports, per role, whether the configured model is present and which installed
// models are viable alternatives.
//
// It is deliberately plumbing, not qualification. It does not measure a model or
// claim one is "best" — that is M9-T2b's bounded qualification run (generator
// pass@k with real tests; judge agreement against the anchor set). This package
// only narrows the field to what fits, so the operator or the qualification run
// can choose.
package onboard

import "sort"

// Model is one installed backend model.
type Model struct {
	Name      string
	SizeBytes int64
	Family    string
}

// Role is one configured persona's model expectation.
type Role struct {
	Role          string // wide | tall | main | security | alternative
	Model         string // the model name in config
	ContextTarget int
}

// PersonaReport is the inventory for one role. Installed reports the configured
// model; Alternatives are the installed models that fit in memory, largest
// first (a rough "most capable that fits" proxy).
type PersonaReport struct {
	Role          string
	Configured    string
	Installed     bool
	ContextTarget int
	Alternatives  []Model
}

// Report is the onboarding inventory.
type Report struct {
	AvailableBytes int64
	ReserveBytes   int64
	Personas       []PersonaReport
}

// Fit reports whether a model fits in memory given a reserve. Unknown memory
// (availableBytes <= 0) never claims a model is infeasible.
func Fit(m Model, availableBytes, reserveBytes int64) bool {
	if availableBytes <= 0 {
		return true
	}
	return m.SizeBytes <= availableBytes-reserveBytes
}

// Inventory maps configured roles to the installed catalog. A model counts as
// installed when its name matches exactly or its base name (tag stripped)
// matches — the same tolerance the doctor's presence check uses. Alternatives
// exclude the configured model and any model that does not fit.
func Inventory(roles []Role, models []Model, availableBytes, reserveBytes int64) Report {
	rep := Report{AvailableBytes: availableBytes, ReserveBytes: reserveBytes}
	exact := map[string]bool{}
	base := map[string]bool{}
	for _, m := range models {
		exact[m.Name] = true
		base[baseName(m.Name)] = true
	}
	for _, r := range roles {
		pr := PersonaReport{Role: r.Role, Configured: r.Model, ContextTarget: r.ContextTarget}
		pr.Installed = exact[r.Model] || base[baseName(r.Model)]
		configuredBase := baseName(r.Model)
		for _, m := range models {
			if m.Name == r.Model || baseName(m.Name) == configuredBase {
				continue
			}
			if Fit(m, availableBytes, reserveBytes) {
				pr.Alternatives = append(pr.Alternatives, m)
			}
		}
		sort.SliceStable(pr.Alternatives, func(i, j int) bool {
			if pr.Alternatives[i].SizeBytes != pr.Alternatives[j].SizeBytes {
				return pr.Alternatives[i].SizeBytes > pr.Alternatives[j].SizeBytes
			}
			return pr.Alternatives[i].Name < pr.Alternatives[j].Name
		})
		rep.Personas = append(rep.Personas, pr)
	}
	return rep
}

// baseName strips an Ollama tag (":" or "@") so "qwen2.5:7b" and "qwen2.5:14b"
// share a base name. A name with no tag is returned unchanged.
func baseName(name string) string {
	for i := 0; i < len(name); i++ {
		if name[i] == ':' || name[i] == '@' {
			return name[:i]
		}
	}
	return name
}
