package onboard

import "github.com/tcs76321/athanor/internal/capacity"

// AuditRole is one measured persona fit. LoadedBytes is the actual size Ollama
// reports in /api/ps at the role's context target (weights + KV cache), which
// is the only honest fit number; FileBytes is just the model blob on disk.
type AuditRole struct {
	Role          string `json:"role"`
	Model         string `json:"model"`
	ContextTarget int    `json:"context_target"`
	FileBytes     int64  `json:"file_bytes"`
	LoadedBytes   int64  `json:"loaded_bytes"`
	Fits          bool   `json:"fits"`
	Error         string `json:"error,omitempty"`
}

// Audit is the measured model plan written by `athanor models -audit` and read
// by the startup guard. Resident lists models still loaded at the end of the
// run; more than one means the one-resident-model policy is not holding.
type Audit struct {
	GeneratedAt  string           `json:"generated_at"`
	HostBytes    int64            `json:"host_bytes"`
	ReserveBytes int64            `json:"reserve_bytes"`
	BudgetBytes  int64            `json:"budget_bytes"`
	Resident     []string         `json:"resident,omitempty"`
	Roles        []AuditRole      `json:"roles"`
	Capacity     *capacity.Report `json:"capacity,omitempty"`
}

// FailingRoles returns the measured roles that do not fit the host budget or
// failed to load. It is empty when every role fits.
func (a Audit) FailingRoles() []AuditRole {
	var out []AuditRole
	for _, r := range a.Roles {
		if !r.Fits {
			out = append(out, r)
		}
	}
	return out
}
