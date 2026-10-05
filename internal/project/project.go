// Package project persists projects, goals, and their tasks
// (ARCHITECTURE §5–§7). One goal maps to one task today — the autonomous
// DAG decomposition is M6 — and each submitted goal becomes a runnable task
// whose job the engine executes.
package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tcs76321/athanor/internal/store"
)

// Archetypes are the §6.2 project archetypes; the set is closed per the
// projects table CHECK.
const (
	ArchetypeText     = "text"
	ArchetypeCode     = "code"
	ArchetypeDocument = "document"
	ArchetypeData     = "data"
	ArchetypeMedia    = "media"
)

// ValidArchetype reports whether a is one of the §6.2 archetypes.
func ValidArchetype(a string) bool {
	switch a {
	case ArchetypeText, ArchetypeCode, ArchetypeDocument, ArchetypeData, ArchetypeMedia:
		return true
	default:
		return false
	}
}

// ErrNotFound reports a missing project or task.
var ErrNotFound = errors.New("project not found")

// Goal text length bounds come from the goals table CHECK (§5).
const (
	goalMinLen = 20
	goalMaxLen = 500
)

// Project is one persisted project row (§6.1).
type Project struct {
	ID        string
	Name      string
	Archetype string
	Goal      string
	Status    string
	// RepositoryPath is the §6.1 repository root the M5-T8 indexer walks
	// (ADR-0028). Empty means no repository is configured; indexing is then
	// a no-op until an operator sets one.
	RepositoryPath string
	// Execution is the per-project §6.2 execution override (F3-T4,
	// ADR-0031). The zero value means "use the built-in archetype
	// defaults"; see TestCommand.
	Execution Execution
	CreatedAt time.Time
}

// Execution holds the per-project §6.2 execution commands. It is persisted
// as JSON in projects.execution_json (migration 0016). The zero value means
// "use the built-in defaults for the archetype".
type Execution struct {
	TestCommand  string   `json:"test_command,omitempty"`
	BuildCommand string   `json:"build_command,omitempty"`
	Linters      []string `json:"linters,omitempty"`
}

// DefaultCodeTestCommand is the built-in test command for a `code` project
// with no override (§6.2). It is a constant so the engine, the docs, and the
// tests agree; changing it is a behavior change for every code project.
const DefaultCodeTestCommand = "pytest -q"

// TestCommand resolves the command the evaluation phase runs in a Job Pod
// for this project (F3-T4). An explicit execution.test_command wins; a
// `code` project with no override gets DefaultCodeTestCommand; other
// archetypes get "" (no test command).
func (p Project) TestCommand() string {
	if p.Execution.TestCommand != "" {
		return p.Execution.TestCommand
	}
	if p.Archetype == ArchetypeCode {
		return DefaultCodeTestCommand
	}
	return ""
}

// Task is one persisted task row (§7.3, M1 subset).
type Task struct {
	ID          string
	ProjectID   string
	GoalID      string
	Title       string
	Description string
	Status      string
	Criteria    []string
	// AllowedTools is the optional per-task tool allowlist override
	// (ROADMAP M2-T4, ARCHITECTURE §25). When non-empty it replaces
	// config.job_pod.default_tools for this task's jobs. When empty
	// the per-task override is "use the daemon default". Persisted
	// as a JSON array string in tasks.allowed_tools_json
	// (migration 0005).
	AllowedTools []string
}

// Repo persists projects, goals, and tasks.
type Repo struct {
	store *store.Store
}

// NewRepo returns a repository backed by s.
func NewRepo(s *store.Store) *Repo {
	return &Repo{store: s}
}

func validateGoalText(goal string) error {
	if len(goal) < goalMinLen || len(goal) > goalMaxLen {
		return fmt.Errorf("goal text must be %d–%d characters, got %d", goalMinLen, goalMaxLen, len(goal))
	}
	return nil
}

// nullIfEmpty maps an empty string to SQL NULL for optional columns.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func marshalCriteria(criteria []string) (string, error) {
	if criteria == nil {
		criteria = []string{}
	}
	raw, err := json.Marshal(criteria)
	if err != nil {
		return "", fmt.Errorf("marshalling criteria: %w", err)
	}
	return string(raw), nil
}
