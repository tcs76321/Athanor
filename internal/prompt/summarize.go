package prompt

import (
	"fmt"

	"github.com/tcs76321/athanor/internal/llm"
)

// SummarizeChunkSystemV1 is the §11.1 "Internal Runtime" prompt that produces
// a Dormant Index entry (§10.1). It is Core-owned and versioned: the constant's
// name carries the version, so a change is a new symbol rather than an edit and
// the EventLog keeps recording which version produced a summary. Users cannot
// override it (§11.1: user prompts never override Core-owned runtime prompts).
const SummarizeChunkSystemV1 = `You write a single-line table-of-contents entry for one chunk of a file so an agent can later decide whether to load it.
Output exactly one line of at most 120 characters. No preamble, no markdown, no trailing period.
Name what the chunk contains — its declaration, symbol, or topic — not that it is a chunk.`

// SummarizeChunkMessages builds the chat messages for one chunk summary. The
// header carries the metadata the Dormant Index publishes (path, language,
// line range) so the summary can refer to it without repeating it.
//
// The function is pure: identical inputs produce byte-identical messages, which
// is what makes the Dormant Index reproducible at temperature 0.0 (ADR-0021 §7).
func SummarizeChunkMessages(filePath, lang string, lineStart, lineEnd int, content string) []llm.Message {
	header := fmt.Sprintf("FILE: %s\nLANG: %s\nLINES: %d-%d\n", filePath, lang, lineStart, lineEnd)
	return []llm.Message{
		{Role: "system", Content: SummarizeChunkSystemV1},
		{Role: "user", Content: header + "---\n" + content},
	}
}
