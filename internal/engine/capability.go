package engine

import "github.com/tcs76321/athanor/internal/project"

// capabilityFor resolves the task's primary output kind. It prefers the
// task's inferred capability, then the project's primary capability, then
// document (the fail-safe). The legacy project/archetype fields remain only
// as fallbacks for rows and tests written before ADR-0093.
func capabilityFor(p project.Project, t project.Task) string {
	if project.ValidCapability(t.Capability) {
		return t.Capability
	}
	if c := p.PrimaryCapability(); project.ValidCapability(c) {
		return c
	}
	return project.CapabilityDocument
}
