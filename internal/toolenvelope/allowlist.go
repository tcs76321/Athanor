// Package toolenvelope defines the per-job tool allowlist for Job Pod
// tool calls (ARCHITECTURE §25, ROADMAP M2-T4). The set of tools a
// Job Pod may invoke is fixed at job creation time and enforced
// server-side by the internal API before any code runs.
//
// M2-T4 ships a closed set of two tools — execute_code and run_tests —
// because those are the only ones the engine calls today. Additional
// tools from the §25 table (read_file, write_file, git_operation,
// fetch_url, etc.) arrive in M3+ when their consumers land. Adding a
// new tool to the closed set is a project decision, not an agent
// decision: it widens the §25 surface and must be paired with the
// matching internal API route, Gate G2 coverage, and a per-archetype
// policy review.
package toolenvelope

import (
	"errors"
	"sort"
)

// Tool is the closed set of job-pod-callable tools from ARCHITECTURE
// §25. The string values are the on-the-wire names that appear in
// the per-task override (tasks.allowed_tools_json) and in the
// config-level default (config.job_pod.default_tools).
type Tool string

// The closed set as of M4-T7. M2-T4 shipped the first
// two (`execute_code`, `run_tests`); M3-T2 commit 2.3
// added `lint` for the per-task linter; M3-T5 adds
// `git_operation` so the engine can record accepted
// artifacts to a project-local git repo (the actual
// engine call site is M3-T7 work; this commit just
// widens the closed set and the matching API route);
// M4-T7 (ADR-0019 §5) adds `fetch_url` and `search_web`
// — Core-executed gateway tools. They are per-task
// override only: `job_pod.default_tools` does not include
// them, so network access is an explicit task decision.
//
// New entries require updating the test in
// allowlist_test.go and a Gate G2 extension that
// asserts the matching internal API route is
// registered. The `git_operation` tool's Gate G2
// coverage is tracked separately.
const (
	ToolExecuteCode  Tool = "execute_code"
	ToolRunTests     Tool = "run_tests"
	ToolLint         Tool = "lint"
	ToolGitOperation Tool = "git_operation"
	ToolFetchURL     Tool = "fetch_url"
	ToolSearchWeb    Tool = "search_web"
)

// ErrUnknownTool is returned by Parse for any name outside the
// closed set. The error wraps the offending name so the caller can
// surface a specific, actionable message in the validation error
// chain.
var ErrUnknownTool = errors.New("toolenvelope: unknown tool")

// ErrToolDisallowed is the typed sentinel the internal API and
// the engine use to communicate "the request was well-formed and
// authenticated, but the per-job envelope does not include the
// requested tool". The internal API returns 403 with this error
// text; the engine matches it with errors.Is and treats it as a
// soft-fail (continue to comparing). M3-T2 will turn soft-fails
// into HITL escalations.
//
// Defined here (in the toolenvelope package) so the engine and
// the internal API agree on a single sentinel without an import
// cycle: the engine imports toolenvelope; the internal API
// imports toolenvelope; the runner package (which sits between
// them) is the one that returns the sentinel.
var ErrToolDisallowed = errors.New("toolenvelope: tool not in job envelope")

// Envelope is the per-job tool allowlist. It is a value type built
// from a fixed set at construction time; once built, it is read-only.
// The zero value is an empty envelope (no tools allowed) — a valid
// configuration for jobs that should never call the pod.
type Envelope struct {
	tools map[Tool]struct{}
}

// Parse parses a []string (typically from YAML or JSON) into an
// Envelope. Empty / nil input is allowed and yields an empty
// envelope. Any name outside the closed set is rejected with an
// error that names the offender.
//
// Duplicates are silently de-duplicated — the source list comes from
// human-edited YAML, not adversarial input, and a duplicate tool
// does not change the meaning of the allowlist.
func Parse(names []string) (Envelope, error) {
	env := Envelope{tools: map[Tool]struct{}{}}
	for _, n := range names {
		t := Tool(n)
		if !isKnown(t) {
			return Envelope{}, errors.Join(ErrUnknownTool, errors.New("tool: "+n))
		}
		env.tools[t] = struct{}{}
	}
	return env, nil
}

// Allows reports whether t is in the envelope. The check is
// constant-time with respect to the number of tools in the envelope
// (Go map lookup is amortized O(1); a side-channel via timing is
// not a concern for a 2-element map checked by an internal daemon).
func (e Envelope) Allows(t Tool) bool {
	if e.tools == nil {
		return false
	}
	_, ok := e.tools[t]
	return ok
}

// Tools returns the sorted, deterministic list of tools in the
// envelope. Used for EventLog events and for test assertions that
// need a stable representation. The order is the natural string
// order of the Tool constants.
func (e Envelope) Tools() []Tool {
	out := make([]Tool, 0, len(e.tools))
	for t := range e.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// IsEmpty reports whether the envelope has no tools. Equivalent to
// len(e.Tools()) == 0 but cheaper (no allocation).
func (e Envelope) IsEmpty() bool { return len(e.tools) == 0 }

// isKnown reports whether t is in the closed set. Kept private so
// only the package's Parse function can construct a non-empty
// envelope.
func isKnown(t Tool) bool {
	switch t {
	case ToolExecuteCode, ToolRunTests, ToolLint, ToolGitOperation, ToolFetchURL, ToolSearchWeb:
		return true
	default:
		return false
	}
}
