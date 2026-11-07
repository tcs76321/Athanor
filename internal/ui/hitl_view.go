package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// hitlField is one readable row of a HITL payload. The dashboard renders these
// instead of the raw JSON blob: an approval is a decision, and the operator
// needs the requested effect, not escaped braces.
type hitlField struct {
	Key   string
	Value string
}

// hitlTypeLabel is the human name for a request type. Unknown types fall back
// to the raw value so a future type is still legible.
func hitlTypeLabel(typ string) string {
	switch typ {
	case "git_push":
		return "Push a branch to a remote"
	case "task_escalation":
		return "A task needs a decision"
	case "strategy_insight":
		return "Promote a strategy insight"
	case "network_access":
		return "Allow a new source host"
	case "clarification":
		return "Answer a job's question"
	default:
		if typ == "" {
			return "Approval request"
		}
		return typ
	}
}

// hitlFieldOrder puts the operationally important keys first, then sorts any
// remaining keys alphabetically. The output is deterministic so the same
// request always renders the same way.
var hitlFieldOrder = []string{
	"title", "reason", "branch", "remote", "commit", "host", "url",
	"instruction", "project", "task_id", "goal_id", "feature", "value", "statement",
}

// hitlFields parses a payload into display rows. Invalid or empty JSON yields
// no rows (the request type and severity still render); nested values are
// compacted to JSON so nothing is silently dropped.
func hitlFields(payload string) []hitlField {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return []hitlField{{Key: "payload", Value: payload}}
	}
	seen := make(map[string]bool, len(raw))
	out := make([]hitlField, 0, len(raw))
	appendField := func(k string, v any) {
		if v == nil {
			return
		}
		text := hitlValue(v)
		if text == "" {
			return
		}
		out = append(out, hitlField{Key: k, Value: text})
		seen[k] = true
	}
	for _, k := range hitlFieldOrder {
		if v, ok := raw[k]; ok {
			appendField(k, v)
		}
	}
	var rest []string
	for k := range raw {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		appendField(k, raw[k])
	}
	return out
}

func hitlValue(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case bool:
		if x {
			return "yes"
		}
		return "no"
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprintf("%v", x)
		}
		return string(b)
	}
}

// expiryLabel renders an approval's remaining lifetime. An empty label means
// the request does not expire; "expired" is explicit so silence is never
// ambiguous.
func expiryLabel(exp *time.Time, now time.Time) string {
	if exp == nil || exp.IsZero() {
		return ""
	}
	d := exp.Sub(now)
	if d <= 0 {
		return "expired"
	}
	switch {
	case d < time.Minute:
		return "expires in under a minute"
	case d < time.Hour:
		return fmt.Sprintf("expires in %dm", int(d.Minutes()))
	case d < 48*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) - h*60
		if m == 0 {
			return fmt.Sprintf("expires in %dh", h)
		}
		return fmt.Sprintf("expires in %dh %dm", h, m)
	default:
		return fmt.Sprintf("expires in %d days", int(d.Hours()/24))
	}
}

// hitlSummary is the one-line notification form: label, severity, and expiry.
func hitlSummary(typ, severity string, exp *time.Time, now time.Time) string {
	parts := []string{hitlTypeLabel(typ)}
	if severity != "" {
		parts = append(parts, severity)
	}
	if e := expiryLabel(exp, now); e != "" {
		parts = append(parts, e)
	}
	return strings.Join(parts, " · ")
}
