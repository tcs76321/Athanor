// Package analysis is the ADR-0090 task-analysis value: the planning phase's
// decomposition of a task into units, each with a default-direct mode and a
// falsifier. Parsing is deliberately lenient — a malformed or absent block
// degrades to one direct unit, because the planner is a proposer, not an
// authority (the M9-T2b lesson).
package analysis

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Mode is a unit's contemplation prior.
type Mode string

const (
	// Direct means "do it without hidden reasoning". It is the default.
	Direct Mode = "direct"
	// Deliberate means "this part is uncertain or high-risk; allow reasoning".
	Deliberate Mode = "deliberate"
)

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool { return m == Direct || m == Deliberate }

// Unit is one independently addressable part of the task.
type Unit struct {
	ID        string `json:"id"`
	Goal      string `json:"goal"`
	Mode      Mode   `json:"mode"`
	Reason    string `json:"reason,omitempty"`
	Falsifier string `json:"falsifier,omitempty"`
}

// Analysis is the planning phase's structured output.
type Analysis struct {
	Units []Unit `json:"units"`
	// Parsed reports whether the model emitted a valid block. False means
	// Units is the one-unit fallback.
	Parsed bool `json:"parsed"`
}

// Default is the fail-safe: one direct unit covering the whole task.
func Default() Analysis {
	return Analysis{Units: []Unit{{ID: "u1", Mode: Direct}}, Parsed: false}
}

// Deliberate returns the units allowed hidden reasoning.
func (a Analysis) Deliberate() []Unit {
	var out []Unit
	for _, u := range a.Units {
		if u.Mode == Deliberate {
			out = append(out, u)
		}
	}
	return out
}

// maxUnits bounds a planner that tries to decompose everything.
const maxUnits = 8

// Parse extracts the fenced analysis block (or a bare JSON object). It returns
// Parsed=true only when at least one unit with a goal was decoded; otherwise
// it returns Default().
func Parse(content string) Analysis {
	for _, candidate := range candidates(content) {
		var a Analysis
		if err := json.Unmarshal([]byte(candidate), &a); err != nil {
			continue
		}
		if len(a.Units) == 0 {
			continue
		}
		units := normalize(a.Units)
		if len(units) == 0 {
			continue
		}
		return Analysis{Units: units, Parsed: true}
	}
	return Default()
}

func normalize(in []Unit) []Unit {
	out := make([]Unit, 0, len(in))
	for _, u := range in {
		u.Goal = strings.TrimSpace(u.Goal)
		if u.Goal == "" {
			continue
		}
		if !u.Mode.Valid() {
			u.Mode = Direct
		}
		u.ID = ""
		out = append(out, u)
		if len(out) == maxUnits {
			break
		}
	}
	for i := range out {
		out[i].ID = "u" + strconv.Itoa(i+1)
	}
	return out
}

// candidates returns JSON-looking substrings, most specific first: fenced
// blocks, then a bare object.
func candidates(content string) []string {
	var out []string
	lines := strings.Split(content, "\n")
	inFence := false
	var buf strings.Builder
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inFence {
				out = append(out, strings.TrimSpace(buf.String()))
				buf.Reset()
				inFence = false
				continue
			}
			lang := strings.TrimPrefix(trimmed, "```")
			if lang == "" || strings.EqualFold(lang, "json") || strings.EqualFold(lang, "analysis") {
				inFence = true
				continue
			}
		}
		if inFence {
			buf.WriteString(line)
			buf.WriteString("\n")
		}
	}
	if start := strings.Index(content, "{"); start >= 0 {
		if end := strings.LastIndex(content, "}"); end > start {
			out = append(out, content[start:end+1])
		}
	}
	return out
}
