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

	"github.com/tcs76321/athanor/internal/dag"
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

// ErrNoRepository reports an operation that needs a project repository_path
// when the project has none (M6-T5 push; ADR-0036).
var ErrNoRepository = errors.New("project has no repository")

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
	// FixturePath is the immutable starter tree a code task reads (ADR-0065):
	// staged into the Job Pod under the candidate and shown to the model.
	// Distinct from RepositoryPath, which is the evolving git-as-undo output
	// target. Empty means no fixture. Read only through airlock containment.
	FixturePath string `json:"fixture_path,omitempty"`
	// DataSchema is the JSON Schema a `data` candidate must validate against
	// (M9-T3). Operator-supplied (never from the model); empty means the LLM
	// judge decides, as before.
	DataSchema string `json:"data_schema,omitempty"`
	// Checks is the operator-declared, machine-checkable constraint set (M9
	// verifier strengthening, ADR-0070). It is the deterministic counterpart
	// to the free-text AcceptanceCriteria: a `text`/`document` candidate is
	// gated by it exactly as a `code` candidate is gated by its test command
	// or a `data` candidate by its schema. nil declares no checks.
	Checks *ExecutionChecks `json:"checks,omitempty"`
}

// ExecutionChecks is the operator-declared, machine-checkable constraint set
// (M9, ADR-0070). It mirrors the deterministic subset of eval/bench's
// `checks:` block — previously the benchmark's offline scorer only — so the
// engine can gate a text/document candidate at runtime instead of scoring it
// after the fact. It rides projects.execution_json (ADR-0031); no migration.
//
// The zero value declares no checks and leaves acceptance to the other
// verifiers and the LLM judge. A declared check is authoritative: the
// declared-checks verifier is HARD, so a failure decides rejection.
type ExecutionChecks struct {
	// RequiredSections names markdown headings (case-insensitive, trimmed)
	// that must each appear. Mirrors eval/bench required_sections.
	RequiredSections []string `json:"required_sections,omitempty"`
	// StrictSectionOrder names headings that must appear in this relative
	// order (a subsequence of the document's headings). Mirrors
	// strict_section_order.
	StrictSectionOrder []string `json:"strict_section_order,omitempty"`
	// ExactParagraphs requires exactly this many prose paragraphs (0 = unset).
	ExactParagraphs int `json:"exact_paragraphs,omitempty"`
	// MinWords / MaxWords bound the word count (0 = unset).
	MinWords int `json:"min_words,omitempty"`
	MaxWords int `json:"max_words,omitempty"`
	// ForbiddenPhrases must not appear (case-insensitive substring).
	ForbiddenPhrases []string `json:"forbidden_phrases,omitempty"`
	// MustContain phrases must each appear (case-insensitive substring).
	MustContain []string `json:"must_contain,omitempty"`
	// NoHeadings forbids any markdown heading.
	NoHeadings bool `json:"no_headings,omitempty"`
	// NoPlaceholders forbids TODO/FIXME/lorem-ipsum markers.
	NoPlaceholders bool `json:"no_placeholders,omitempty"`
	// MinItems requires at least Min bullet items under a section whose
	// heading contains Noun (or, failing that, in the document body). It
	// generalizes eval/bench's count_alternatives_min / count_risks_min.
	MinItems []ItemCount `json:"min_items,omitempty"`
}

// ItemCount is one named minimum-cardinality check, e.g.
// {Noun: "risks", Min: 2} for eval/bench's count_risks_min: 2.
type ItemCount struct {
	Noun string `json:"noun"`
	Min  int    `json:"min"`
}

// IsZero reports whether no check is declared.
func (c ExecutionChecks) IsZero() bool {
	return len(c.RequiredSections) == 0 && len(c.StrictSectionOrder) == 0 &&
		c.ExactParagraphs == 0 && c.MinWords == 0 && c.MaxWords == 0 &&
		len(c.ForbiddenPhrases) == 0 && len(c.MustContain) == 0 &&
		!c.NoHeadings && !c.NoPlaceholders && len(c.MinItems) == 0
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

// Task is one persisted task row (§7.3, M1 subset plus the M6 DAG fields).
type Task struct {
	ID          string
	ProjectID   string
	GoalID      string
	ParentID    string
	Title       string
	Description string
	Status      string
	// TaskType is the §7.3 task kind (one_time | recurring | triggered).
	// Recurring/triggered execution is backlog; every decomposed task is
	// one_time today (migration 0017).
	TaskType string
	Criteria []string
	// DependsOn lists the IDs of tasks that must finish before this one
	// (the §7.3 dependency edges). Empty for a root task.
	DependsOn []string
	// Budget is the §7.3 per-task budget, persisted as budget_json.
	Budget dag.Budget
	// Priority orders sibling tasks when several are ready at once.
	Priority int
	// AllowedTools is the optional per-task tool allowlist override
	// (ROADMAP M2-T4, ARCHITECTURE §25). When non-empty it replaces
	// config.job_pod.default_tools for this task's jobs. When empty
	// the per-task override is "use the daemon default". Persisted
	// as a JSON array string in tasks.allowed_tools_json
	// (migration 0005).
	AllowedTools []string
	// RetryBaseline is the number of jobs already recorded when an operator
	// last reset the task (known-issues H8, migration 0026). The scheduler
	// counts a task's §7.2 attempts as len(jobs) - RetryBaseline, so a reset
	// grants a fresh retry window rather than re-blocking immediately. Zero
	// for a task that has never been reset.
	RetryBaseline int
}

// TaskSpec is one task the caller asks the repository to persist as part
// of a decomposed graph. Key is a caller-local identifier used only to
// express ParentKey and DependsOn; the repository replaces it with a
// generated task ID.
type TaskSpec struct {
	Key         string
	ParentKey   string
	Title       string
	Description string
	DependsOn   []string
	Criteria    []string
	Budget      dag.Budget
	Priority    int
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
