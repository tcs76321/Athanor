package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/tcs76321/athanor/internal/capacity"
	"github.com/tcs76321/athanor/internal/onboard"
)

// capacityView is the read-only runtime summary. It is derived from the model
// audit when one exists; a missing audit leaves the section out rather than
// guessing.
type capacityView struct {
	Solo     string
	Pods     string
	Light    string
	Coverage string
	Reasons  []capacity.Reason
}

func (u *UI) capacityView() *capacityView {
	raw := []byte(u.deps.ModelAudit)
	if len(raw) == 0 && u.deps.ConfigPath != "" {
		b, err := os.ReadFile(filepath.Join(filepath.Dir(u.deps.ConfigPath), "model-fit.json"))
		if err == nil {
			raw = b
		}
	}
	if len(raw) == 0 {
		return nil
	}
	var audit onboard.Audit
	if err := json.Unmarshal(raw, &audit); err != nil || audit.Capacity == nil {
		return nil
	}
	rep := audit.Capacity
	return &capacityView{
		Solo:     slotText(rep.Solo),
		Pods:     swarmText(rep.SwarmPods),
		Light:    swarmText(rep.SwarmLight),
		Coverage: coverageText(rep.Coverage),
		Reasons:  rep.Reasons,
	}
}

func slotText(s *capacity.Slot) string {
	if s == nil {
		return "none"
	}
	return s.Model + " @ " + itoa(s.Context)
}

func swarmText(s *capacity.Swarm) string {
	if s == nil {
		return "none"
	}
	names := make([]string, 0, len(s.Groups))
	for _, g := range s.Groups {
		names = append(names, g.Model)
	}
	return itoa(s.Slots) + " × " + itoa(s.Context) + " (" + strings.Join(names, ", ") + ")"
}

func coverageText(c capacity.Coverage) string {
	var caps []string
	for _, name := range []string{"code", "data", "document", "text", "media"} {
		if c.Capabilities[name] {
			caps = append(caps, name)
		}
	}
	out := "capabilities: " + strings.Join(caps, ", ")
	if c.SingleModel {
		out += "; single model (role fallback)"
	}
	if !c.Embedding {
		out += "; FTS-only memory"
	}
	if !c.CrossFamilyJudge {
		out += "; same-family judgment"
	}
	if !c.CodeExecution {
		out += "; code execution unavailable"
	}
	if len(c.MissingRoles) > 0 {
		out += "; missing roles: " + strings.Join(c.MissingRoles, ", ")
	}
	return out
}
