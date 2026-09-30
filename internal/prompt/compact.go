package prompt

import (
	"fmt"

	"github.com/tcs76321/athanor/internal/llm"
)

// Compaction message construction (ARCHITECTURE §10.3; ROADMAP M5-T6; ADR-0025).
//
// §10.3 is the only place the MCE may lose information, so its instructions are
// Core-owned, versioned Internal-Runtime prompts (§11.1): the constant's name
// carries the version, a change is a new symbol rather than an edit, and the
// matching *Version string is part of the CompactStore's content-address key
// (ADR-0025 §1), so a template change is a new cache row.
//
// The package stays free of internal/mce (ADR-0023 §1): the builders take plain
// strings, and the cmd/athanor adapter — which may import both — selects the
// builder by mce.CompactionKind.

// CompactDeterministicSystemV1 is the §11.1 Internal Runtime prompt for
// deterministic compaction (`log`/`test_output` + `episodic`, §10.3): extract
// exact error codes, stack traces, and return values; discard verbose noise.
const CompactDeterministicSystemV1 = `You compact a technical log or test-output record into durable memory.
Extract, verbatim wherever possible: error codes, exception types, stack-trace frames, command lines, exit codes, return values, file paths, and exact timings.
Discard verbose noise, repeated progress lines, and boilerplate.
Output one fact per line. No preamble, no markdown, no commentary.
Never invent a value that is not present in the input; if a field is absent, omit it.`

// CompactSemanticSystemV1 is the §11.1 Internal Runtime prompt for semantic
// compaction (`conversation`/`brainstorming` + `archival`, `documentation` +
// `episodic`/`archival`, §10.3): extract explicit decisions, constraints,
// derived rules, key facts, and API signatures; discard raw chat.
const CompactSemanticSystemV1 = `You compact a conversation or document into durable memory.
Extract: explicit decisions, constraints, derived rules, key facts, API signatures, and named entities.
Discard raw chat, pleasantries, and restatement.
Output one fact per line. No preamble, no markdown, no commentary.
Never invent a fact that is not present in the input; if something is uncertain, omit it.`

// Template versions. Each is part of the content-address key (ADR-0025 §1):
// bump it when its system prompt changes, so the previous outputs stay
// addressable and the new template gets its own cache row.
const (
	CompactDeterministicVersion = "compact-deterministic-v1"
	CompactSemanticVersion      = "compact-semantic-v1"
)

// CompactDeterministicMessages builds the chat messages for one deterministic
// compaction. profileType/profileState are the §10.2 axes; sourceLabel names
// the origin (a job ID, a path, or a hash). Pure: identical input produces
// byte-identical messages, which is the request half of the determinism
// contract (ADR-0025 §1).
func CompactDeterministicMessages(profileType, profileState, sourceLabel, content string) []llm.Message {
	return compactMessages(CompactDeterministicSystemV1, profileType, profileState, sourceLabel, content)
}

// CompactSemanticMessages builds the chat messages for one semantic
// compaction. Same purity contract as CompactDeterministicMessages.
func CompactSemanticMessages(profileType, profileState, sourceLabel, content string) []llm.Message {
	return compactMessages(CompactSemanticSystemV1, profileType, profileState, sourceLabel, content)
}

// compactMessages builds the shared two-message shape. The user header carries
// the §10.2 profile and the source label so the model can frame its output
// without repeating metadata — the same shape as SummarizeChunkMessages.
func compactMessages(system, profileType, profileState, sourceLabel, content string) []llm.Message {
	header := fmt.Sprintf("PROFILE: %s/%s\n", profileType, profileState)
	if sourceLabel != "" {
		header += fmt.Sprintf("SOURCE: %s\n", sourceLabel)
	}
	return []llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: header + "---\n" + content},
	}
}
