package llm

import "encoding/json"

// ThinkMode is Ollama's `think` control. Boolean-thinking models accept
// true/false; level-based models accept "low"|"medium"|"high" (Ollama
// /api/show exposes the supported values). A bool cannot express "how much",
// which is why the old *bool was replaced (ADR-0090 S0).
//
// MarshalJSON emits the string when Level is set and the boolean otherwise, so
// the wire shape matches whichever control the model declares.
type ThinkMode struct {
	Enabled bool
	// Level is one of "low", "medium", "high". Empty means boolean mode.
	Level string
}

// MarshalJSON implements the Ollama wire shape.
func (m ThinkMode) MarshalJSON() ([]byte, error) {
	if m.Level != "" {
		return json.Marshal(m.Level)
	}
	return json.Marshal(m.Enabled)
}

// UnmarshalJSON accepts either shape, for tests and future server responses.
func (m *ThinkMode) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		m.Enabled = s != ""
		m.Level = s
		return nil
	}
	var v bool
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	m.Enabled = v
	m.Level = ""
	return nil
}

// Off is the bounded default: no hidden reasoning, the behavior every judgment
// phase keeps regardless of configuration.
func Off() *ThinkMode { return &ThinkMode{Enabled: false} }

// At returns a level mode. An empty level degrades to boolean true.
func At(level string) *ThinkMode {
	if level == "" {
		return &ThinkMode{Enabled: true}
	}
	return &ThinkMode{Enabled: true, Level: level}
}

// ReasoningTokens estimates the hidden-reasoning token count from the
// returned thinking trace. Ollama does not report a separate count (eval_count
// includes reasoning), so this is a documented approximation at ~4 chars per
// token. It is recorded for selection, never used as a hard budget.
func ReasoningTokens(thinking string) int {
	if len(thinking) == 0 {
		return 0
	}
	return (len(thinking) + 3) / 4
}
