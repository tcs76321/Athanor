package division

import (
	"bytes"
	"regexp"
)

// headerPatterns is the §10.1 "structural headers for text/markdown"
// splitter. Only markdown is listed: code languages are routed to
// tree-sitter by Divide, so their header regexes from the M5-T1 spike are
// deliberately not carried into the shipped hybrid (ADR-0020).
var headerPatterns = map[string]*regexp.Regexp{
	"markdown": regexp.MustCompile(`(?m)^#{1,6} \S`),
}

// headerChunks divides markdown headings or plain-text paragraphs. When the
// source carries no structural headers it degrades to fixed line blocks
// (P5), so a headerless document is still tiled without loss.
func (d *Divider) headerChunks(filePath, lang string, src []byte) []Chunk {
	var bounds []int
	if lang == "text" {
		bounds = textParagraphBounds(src)
	} else if re, ok := headerPatterns[lang]; ok {
		for _, loc := range re.FindAllIndex(src, -1) {
			bounds = append(bounds, loc[0])
		}
	}
	if len(bounds) == 0 {
		return d.fallbackChunks(filePath, lang, src)
	}
	return d.bounded(filePath, lang, src, KindHeader, bounds)
}

// textParagraphBounds returns the start offset of each paragraph: a
// non-blank line whose predecessor line was blank. Plain text has no
// structural headers, so paragraph (blank-line) boundaries are its
// structural division.
func textParagraphBounds(src []byte) []int {
	var bounds []int
	lineStart := 0
	prevBlank := true
	for i := 0; i <= len(src); i++ {
		if i == len(src) || src[i] == '\n' {
			blank := len(bytes.TrimSpace(src[lineStart:i])) == 0
			if !blank && prevBlank && lineStart > 0 {
				bounds = append(bounds, lineStart)
			}
			prevBlank = blank
			lineStart = i + 1
		}
	}
	return bounds
}
