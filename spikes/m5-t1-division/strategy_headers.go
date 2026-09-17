package division

import "regexp"

// HeaderStrategy (candidate C) splits on naive structural-header regexes:
// markdown headings, top-level def/class/function lines. It is
// deliberately language-generic and dependency-free — and deliberately
// naive: it cannot see strings, comments, or nesting, so it will place
// false boundaries inside docstrings and code fences. The metrics run
// quantifies exactly how often that happens (boundary agreement with the
// AST strategies' ground truth).
type HeaderStrategy struct{}

func (HeaderStrategy) Name() string { return "header-regex" }

var headerPatterns = map[string]*regexp.Regexp{
	"markdown":   regexp.MustCompile(`(?m)^#{1,6} \S`),
	"python":     regexp.MustCompile(`(?m)^(def |class |async def |@)`),
	"go":         regexp.MustCompile(`(?m)^func `),
	"javascript": regexp.MustCompile(`(?m)^(function |class |const |let |var |async function )`),
}

func (HeaderStrategy) Divide(filePath, lang string, src []byte) []Chunk {
	re, ok := headerPatterns[lang]
	if !ok {
		return fallbackChunks(filePath, lang, src)
	}
	var bounds []int
	for _, loc := range re.FindAllIndex(src, -1) {
		bounds = append(bounds, loc[0])
	}
	if len(bounds) == 0 {
		return fallbackChunks(filePath, lang, src)
	}
	return buildChunks(filePath, lang, src, KindHeader, bounds)
}
