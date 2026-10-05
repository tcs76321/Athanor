// Package dag is the pure task-graph model behind the M6 autonomous DAG
// decomposition (ARCHITECTURE §7.1, ROADMAP M6-T1; ADR-0032).
//
// It parses the tall persona's structured decomposition output into a
// Graph and validates it deterministically: unique keys, resolved
// parent/dependency references, acyclicity, at least one root, depth and
// node-count bounds, leaf acceptance-criteria coverage, and budget
// feasibility. It performs no I/O and makes no LLM call; the judgment is
// pure so it can be unit- and property-tested and so no model is ever in
// the validation path.
package dag

import (
	"encoding/json"
	"fmt"
	"time"
)

// Budget is the §7.3 per-task budget. A zero counter means "unset". The
// wall-time value marshals as a Go duration string ("30m") so a persisted
// budget_json row is human-readable and the same shape is accepted from
// the model.
type Budget struct {
	MaxJobs     int           `json:"max_jobs"`
	MaxLLMCalls int           `json:"max_llm_calls"`
	MaxTokens   int           `json:"max_tokens"`
	MaxWallTime time.Duration `json:"-"`
}

// MarshalJSON implements json.Marshaler, rendering max_wall_time as a
// duration string rather than an integer nanosecond count.
func (b Budget) MarshalJSON() ([]byte, error) {
	// The named local type drops the method set, so this does not recurse.
	type budget Budget
	type wire struct {
		budget
		MaxWallTime string `json:"max_wall_time,omitempty"`
	}
	w := wire{budget: budget(b)}
	if b.MaxWallTime > 0 {
		w.MaxWallTime = b.MaxWallTime.String()
	}
	return json.Marshal(w)
}

// UnmarshalJSON implements json.Unmarshaler. max_wall_time accepts a Go
// duration string; a missing or empty value means "unset".
func (b *Budget) UnmarshalJSON(data []byte) error {
	var w struct {
		MaxJobs     int    `json:"max_jobs"`
		MaxLLMCalls int    `json:"max_llm_calls"`
		MaxTokens   int    `json:"max_tokens"`
		MaxWallTime string `json:"max_wall_time"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	b.MaxJobs = w.MaxJobs
	b.MaxLLMCalls = w.MaxLLMCalls
	b.MaxTokens = w.MaxTokens
	b.MaxWallTime = 0
	if w.MaxWallTime != "" {
		d, err := time.ParseDuration(w.MaxWallTime)
		if err != nil {
			return fmt.Errorf("max_wall_time %q: %w", w.MaxWallTime, err)
		}
		b.MaxWallTime = d
	}
	return nil
}

// Node is one task in the decomposed graph. Key is the model's local
// identifier; ParentKey groups a task under a phase (empty means top
// level); DependsOn lists the keys of tasks that must finish first.
type Node struct {
	Key         string
	ParentKey   string
	Title       string
	Description string
	DependsOn   []string
	Criteria    []string
	Budget      Budget
}

// Graph is a parsed decomposition.
type Graph struct {
	Nodes []Node
}

// Limits bounds an acceptable graph. A non-positive field disables that
// bound (useful in tests); production passes the configured values.
type Limits struct {
	MaxTasks     int
	MaxDepth     int
	MaxTotalJobs int
}
