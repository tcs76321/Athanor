package verify

import (
	"strings"

	"github.com/tcs76321/athanor/internal/project"
)

// docsVerifier is the F5 `require_documentation_for_code` gate. It applies
// only when the operator requires documentation (RequireDocs); otherwise it
// is absent, so the pre-F5 advisory path is unchanged.
//
// The check is a *presence* check, not a quality judgment: a candidate passes
// when it contains at least one recognized documentation construct —
//
//   - a docstring: a line whose trimmed content begins with `"""` or `”'`
//     and whose body (across lines, up to the closing quote) has at least
//     minDocstringChars non-space characters (Python module/function
//     docstrings);
//   - a documentation comment block: a run of comment lines (`#`, `//`,
//     `///`, `/*`, `*`, `--`, `;`, `%`) either at the top of the file (a
//     module header, at least two lines) or immediately above a declaration
//     keyword (`def`, `class`, `func`, `function`, `type`, `struct`,
//     `interface`, `public`, `private`, `export`, `impl`, `fn`, `sub`,
//     `@`, …);
//   - a markdown documentation section: a line whose trimmed content starts
//     with `## `.
//
// The recognized set is deliberately small and language-agnostic. It can
// under-detect an exotic house style, so the flag can be disabled without a
// code change. See ADR-0058.
type docsVerifier struct{}

func (docsVerifier) Archetypes() []string { return []string{project.ArchetypeCode} }
func (docsVerifier) Name() string         { return "docs" }

func (docsVerifier) Verify(in Input) Verdict {
	if !in.RequireDocs {
		return Verdict{Verifier: "docs", Applied: false, Hard: true}
	}
	if hasDocumentation(in.Content) {
		return Verdict{Verifier: "docs", Applied: true, Hard: true, Pass: true, Score: 1,
			Reasons: []string{"documentation construct present"}}
	}
	return Verdict{Verifier: "docs", Applied: true, Hard: true, Pass: false, Score: 0,
		Reasons: []string{"no documentation construct found (docstring, doc-comment block, or docs section)"}}
}

// minDocstringChars is the smallest meaningful docstring body. It rejects an
// empty `""""""` or a placeholder while accepting a real one-line summary.
const minDocstringChars = 8

// hasDocumentation reports whether content carries a recognized documentation
// construct (see docsVerifier). It is pure and deterministic.
func hasDocumentation(content string) bool {
	lines := strings.Split(content, "\n")
	return hasDocstring(lines) || hasDocCommentBlock(lines)
}

// hasDocstring detects a triple-quoted block that begins a line (a Python
// module or function docstring) and carries real text.
func hasDocstring(lines []string) bool {
	for i, line := range lines {
		t := strings.TrimSpace(line)
		for _, q := range []string{`"""`, `'''`} {
			if !strings.HasPrefix(t, q) {
				continue
			}
			body := t[len(q):]
			closed := false
			if j := strings.Index(body, q); j >= 0 {
				body = body[:j]
				closed = true
			} else {
				var b strings.Builder
				b.WriteString(body)
				b.WriteByte('\n')
				for k := i + 1; k < len(lines); k++ {
					if j := strings.Index(lines[k], q); j >= 0 {
						b.WriteString(lines[k][:j])
						closed = true
						break
					}
					b.WriteString(lines[k])
					b.WriteByte('\n')
				}
				body = b.String()
			}
			if closed && countNonSpace(body) >= minDocstringChars {
				return true
			}
		}
	}
	return false
}

// commentPrefixes are the line-comment leads the docs gate recognizes.
var commentPrefixes = []string{"#", "//", "///", "/*", "*", "--", ";", "%"}

// declarationPrefixes are the declaration keywords a doc comment may precede.
var declarationPrefixes = []string{
	"def ", "class ", "async def ", "func ", "function ", "type ", "struct ",
	"interface ", "public ", "private ", "protected ", "export ", "const ",
	"var ", "impl ", "fn ", "sub ", "package ", "@",
}

func isCommentLine(s string) bool {
	s = strings.TrimSpace(s)
	for _, p := range commentPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func isDeclarationLine(s string) bool {
	s = strings.TrimSpace(s)
	for _, p := range declarationPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// minDocCommentChars is the smallest meaningful standalone documentation
// comment block (a module header). A block immediately above a declaration
// needs only real text on one line.
const minDocCommentChars = 8

// hasDocCommentBlock detects a documentation comment block: a run of comment
// lines that is either a module header at the top of the file (≥2 lines with
// real text) or sits immediately above a declaration (at least one line with
// real text). A markdown `## ` heading also counts.
func hasDocCommentBlock(lines []string) bool {
	inBlock := false
	blockStart, blockLen, textLen := -1, 0, 0
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "## ") {
			return true
		}
		if isCommentLine(line) {
			if !inBlock {
				inBlock, blockStart, blockLen, textLen = true, i, 0, 0
			}
			blockLen++
			textLen += commentTextLen(line)
			continue
		}
		if !inBlock {
			continue
		}
		if strings.TrimSpace(line) == "" {
			// A blank inside/after a block does not end it; a following
			// declaration still counts.
			continue
		}
		if isDeclarationLine(line) {
			return textLen >= 1
		}
		if blockStart == 0 && blockLen >= 2 {
			return textLen >= minDocCommentChars
		}
		inBlock = false
	}
	// A file that is entirely a comment header.
	return inBlock && blockStart == 0 && blockLen >= 2 && textLen >= minDocCommentChars
}

// commentTextLen returns the number of non-space characters in line after its
// longest matching comment prefix (0 when the line is not a comment).
func commentTextLen(line string) int {
	t := strings.TrimSpace(line)
	best := -1
	for _, p := range commentPrefixes {
		if strings.HasPrefix(t, p) && len(p) > best {
			best = len(p)
		}
	}
	if best < 0 {
		return 0
	}
	return countNonSpace(t[best:])
}

// countNonSpace returns the number of non-whitespace bytes in s.
func countNonSpace(s string) int {
	n := 0
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			n++
		}
	}
	return n
}
