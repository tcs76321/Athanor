// Package llm implements the model persona system (ARCHITECTURE §12) and
// the Ollama client M1 builds on.
//
// It owns three concerns:
//
//   - Persona registry: the five fixed functional roles (§12.1) resolved
//     from configuration.
//   - Temperature resolution: §13.1 precedence (phase pin/range >
//     persona default) computed as a pure function.
//   - Context feasibility: the (persona, phase) floor rule from §12.6 /
//     ADR-0002 — recommend or escalate, never silently reduce.
//
// The client speaks Ollama's native HTTP API (/api/chat). It executes no
// tools, spawns no processes, and touches no files: Gate G1 requires the
// M1 agent to be provably contained to LLM + storage.
package llm

import (
	"fmt"

	"github.com/tcs76321/athanor/internal/config"
)

// The five fixed persona roles (§12.1). The set is closed: new roles are
// an ARCHITECTURE change, not a config change.
const (
	RoleWide        = "wide"
	RoleTall        = "tall"
	RoleMain        = "main"
	RoleSecurity    = "security"
	RoleAlternative = "alternative"
)

// Persona is one resolved model assignment.
type Persona struct {
	Role  string
	Model string
	// Family is the model lineage (F4-T3): the judge must differ from the
	// generator so their errors are decorrelated.
	Family        string
	ContextTarget int
	Temperature   float64
}

// Registry holds the five personas resolved from configuration.
type Registry struct {
	personas map[string]Persona
}

// NewRegistry builds the registry from configuration. It fails loudly if
// any role is unassigned — a partial registry would surface later as a
// confusing per-call failure instead of a boot-time config error.
func NewRegistry(cfg config.Personas) (*Registry, error) {
	pairs := map[string]config.PersonaConfig{
		RoleWide:        cfg.Wide,
		RoleTall:        cfg.Tall,
		RoleMain:        cfg.Main,
		RoleSecurity:    cfg.Security,
		RoleAlternative: cfg.Alternative,
	}
	r := &Registry{personas: make(map[string]Persona, len(pairs))}
	for role, pc := range pairs {
		if pc.Model == "" {
			return nil, fmt.Errorf("persona %q has no model assigned (personas.%s.model)", role, role)
		}
		r.personas[role] = Persona{
			Role:          role,
			Model:         pc.Model,
			Family:        pc.Family,
			ContextTarget: pc.ContextTarget,
			Temperature:   pc.Temp(),
		}
	}
	return r, nil
}

// Persona returns the assignment for a role.
func (r *Registry) Persona(role string) (Persona, bool) {
	p, ok := r.personas[role]
	return p, ok
}

// All returns every persona keyed by role.
func (r *Registry) All() map[string]Persona {
	out := make(map[string]Persona, len(r.personas))
	for k, v := range r.personas {
		out[k] = v
	}
	return out
}
