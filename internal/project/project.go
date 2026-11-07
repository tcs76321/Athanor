// Package project persists projects, goals, and their tasks
// (ARCHITECTURE §5–§7). One goal maps to one task today — the autonomous
// DAG decomposition is M6 — and each submitted goal becomes a runnable task
// whose job the engine executes.
package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
	"unicode/utf8"

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

// Goal text length bounds come from the goals table CHECK (§5). Three
// characters is the floor: the project onboarding brief supplies depth, and a
// short, honest goal ("a daily news brief") must not be rejected.
const (
	goalMinLen = 3
	goalMaxLen = 500
)

// Project is one persisted project row (§6.1).
// Project priority values. At most one project may be critical; Repo.SetPriority
// demotes the previous critical project in the same transaction.
const (
	PriorityCritical = "critical"
	PriorityHigh     = "high"
	PriorityMedium   = "medium"
	PriorityLow      = "low"
)

// ValidPriority reports whether p is in the closed priority set.
func ValidPriority(p string) bool {
	switch p {
	case PriorityCritical, PriorityHigh, PriorityMedium, PriorityLow:
		return true
	default:
		return false
	}
}

// NormalizePriority maps an empty/unknown value to medium. Use ValidPriority
// when rejecting an operator value rather than silently changing it.
func NormalizePriority(p string) string {
	if ValidPriority(p) {
		return p
	}
	return PriorityMedium
}

type Project struct {
	ID   string
	Name string
	// Capabilities is the inferred set of work kinds this project can produce
	// (ADR-0093). It replaces the single operator-chosen archetype.
	Capabilities []string
	// Archetype is the deprecated legacy alias for the primary capability.
	// It is kept for one migration window so old rows/clients keep working;
	// no new product surface reads or writes it.
	Archetype string
	Goal      string
	Status    string
	// Priority is the operator-set scheduling priority: critical, high,
	// medium, or low. At most one project is critical at a time (migration
	// 0037's partial unique index). The zero value on an old row means
	// medium.
	Priority string
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
	// FixtureRoot bounds FixturePath when the fixture is not inside the
	// repository (the benchmark's corpus fixtures are the motivating case).
	// Empty means "use RepositoryPath as the root"; when both are empty the
	// fixture is refused rather than reading an arbitrary host path.
	FixtureRoot string `json:"fixture_root,omitempty"`
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

	// Data checks. The JSON-Schema subset validates shape; these pin the
	// array-level constraints a schema cannot express without minItems /
	// maxItems / uniqueItems support. They mirror eval/bench's
	// array_length, array_unique_by, array_sorted_by, and json_field_matches.
	// ArrayLength 0 means unset.
	ArrayLength      int               `json:"array_length,omitempty"`
	ArrayUniqueBy    string            `json:"array_unique_by,omitempty"`
	ArraySortedBy    string            `json:"array_sorted_by,omitempty"`
	JSONFieldMatches map[string]string `json:"json_field_matches,omitempty"`
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
		!c.NoHeadings && !c.NoPlaceholders && len(c.MinItems) == 0 &&
		c.ArrayLength == 0 && c.ArrayUniqueBy == "" && c.ArraySortedBy == "" &&
		len(c.JSONFieldMatches) == 0
}

// Describe renders the declared checks as deterministic, prompt-visible
// requirements. The parser enforces them, so the model must see the exact
// limits rather than infer them from prose criteria; this closes the
// "declared check the model never saw" gap the document max_words regression
// exposed. The order is stable (struct order, sorted map keys).
func (c *ExecutionChecks) Describe() []string {
	if c == nil || c.IsZero() {
		return nil
	}
	var out []string
	if c.MinWords > 0 && c.MaxWords > 0 {
		out = append(out, fmt.Sprintf("word count must be between %d and %d", c.MinWords, c.MaxWords))
	} else if c.MaxWords > 0 {
		out = append(out, fmt.Sprintf("word count must be at most %d", c.MaxWords))
	} else if c.MinWords > 0 {
		out = append(out, fmt.Sprintf("word count must be at least %d", c.MinWords))
	}
	if c.ExactParagraphs > 0 {
		out = append(out, fmt.Sprintf("exactly %d prose paragraphs", c.ExactParagraphs))
	}
	for _, section := range c.RequiredSections {
		out = append(out, fmt.Sprintf("required section: %q", section))
	}
	if len(c.StrictSectionOrder) > 0 {
		out = append(out, "sections must appear in this order: "+quoteList(c.StrictSectionOrder))
	}
	for _, phrase := range c.MustContain {
		out = append(out, fmt.Sprintf("must contain: %q", phrase))
	}
	for _, phrase := range c.ForbiddenPhrases {
		out = append(out, fmt.Sprintf("must not contain: %q", phrase))
	}
	if c.NoHeadings {
		out = append(out, "no markdown headings")
	}
	if c.NoPlaceholders {
		out = append(out, "no TODO/FIXME/lorem-ipsum placeholders")
	}
	for _, item := range c.MinItems {
		if item.Min > 0 {
			out = append(out, fmt.Sprintf("at least %d %s item(s)", item.Min, item.Noun))
		}
	}
	if c.ArrayLength > 0 {
		out = append(out, fmt.Sprintf("exactly %d array item(s)", c.ArrayLength))
	}
	if c.ArrayUniqueBy != "" {
		out = append(out, fmt.Sprintf("array items must be unique by %q", c.ArrayUniqueBy))
	}
	if c.ArraySortedBy != "" {
		out = append(out, fmt.Sprintf("array items must be sorted ascending by %q", c.ArraySortedBy))
	}
	if len(c.JSONFieldMatches) > 0 {
		keys := make([]string, 0, len(c.JSONFieldMatches))
		for key := range c.JSONFieldMatches {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			out = append(out,
				fmt.Sprintf("field %q must match regex %q", key, c.JSONFieldMatches[key]))
		}
	}
	return out
}

func quoteList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = fmt.Sprintf("%q", item)
	}
	return joinStrings(quoted, " → ")
}

func joinStrings(items []string, sep string) string {
	if len(items) == 0 {
		return ""
	}
	out := items[0]
	for _, item := range items[1:] {
		out += sep + item
	}
	return out
}

// DefaultCodeTestCommand is retained as a compatibility constant for callers
// that still describe the old pytest default. TestCommand no longer invents
// it: the command is explicit or detected from the repository (ADR-0093).
const DefaultCodeTestCommand = "pytest -q"

// PrimaryCapability returns the project's canonical primary capability.
func (p Project) PrimaryCapability() string {
	if len(p.Capabilities) > 0 {
		return PrimaryCapability(p.Capabilities)
	}
	return PrimaryCapability(CapabilitiesFromArchetype(p.Archetype))
}

// TestCommand resolves the command the evaluation phase runs in a Job Pod
// (F3-T4, ADR-0093). An explicit execution.test_command wins; otherwise the
// repository manifest is detected. A missing command is empty: a code task
// without a detected runner degrades to the judge and is audited, rather than
// guessing pytest.
func (p Project) TestCommand() string {
	return ProjectTestCommand(p)
}

// ProjectTestCommand is the value form of TestCommand, used by the engine and
// tests.
func ProjectTestCommand(p Project) string {
	if p.Execution.TestCommand != "" {
		return p.Execution.TestCommand
	}
	return DetectTestCommand(p.RepositoryPath)
}

// Task is one persisted task row (§7.3, M1 subset plus the M6 DAG fields).
type Task struct {
	ID          string
	ProjectID   string
	GoalID      string
	ParentID    string
	Title       string
	Description string
	// Capability is the task's primary output kind (ADR-0093): code, data,
	// document, text, or media. It selects the final artifact kind and the
	// deterministic verifier set. Empty means "infer/legacy".
	Capability string
	Status     string
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
	// AllowedTools optionally overrides the capability default for this task.
	// It is validated against the closed §25 set by CreateDAG.
	AllowedTools []string
	// Capability is the task's primary output kind. Empty means infer it
	// from the description and the project's capability set (ADR-0093).
	Capability string
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
	n := utf8.RuneCountInString(goal)
	if n < goalMinLen || n > goalMaxLen {
		return fmt.Errorf("goal text must be %d–%d characters, got %d", goalMinLen, goalMaxLen, n)
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
