package division

import (
	"bytes"
	"go/parser"
	"go/token"
)

// PureGoStrategy (candidate B) uses zero new dependencies: go/parser for
// Go (a real AST), and hand-written structural scanners for Python
// (indentation + string/continuation awareness) and JavaScript
// (brace-depth with string/comment awareness). The honest hypothesis
// under test: pure-Go parsing is excellent for Go and degrades to
// heuristics everywhere else.
type PureGoStrategy struct{}

func (PureGoStrategy) Name() string { return "pure-go" }

func (PureGoStrategy) Divide(filePath, lang string, src []byte) []Chunk {
	switch lang {
	case "go":
		return divideGoAST(filePath, src)
	case "python":
		return dividePythonIndent(filePath, src)
	case "javascript":
		return divideBraceDepth(filePath, src)
	default:
		return fallbackChunks(filePath, lang, src)
	}
}

// divideGoAST chunks at top-level declaration offsets from go/parser.
// Doc comments attach to the following decl automatically: they sit
// between the previous decl's end and this decl's start, so tiling
// includes them in the decl's chunk.
func divideGoAST(filePath string, src []byte) []Chunk {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, parser.ParseComments)
	if err != nil {
		// P5: partial ASTs are not trusted; degrade to fallback.
		return fallbackChunks(filePath, "go", src)
	}
	var bounds []int
	for _, d := range f.Decls {
		off := fset.Position(d.Pos()).Offset
		if off > 0 {
			bounds = append(bounds, off)
		}
	}
	return buildChunks(filePath, "go", src, KindAST, bounds)
}

// dividePythonIndent chunks Python at top-level def/class/decorator
// lines, tracking triple-quoted strings, line continuations, and bracket
// depth so boundaries never land inside a string, a continued line, or a
// multi-line call. This is the "indentation-aware structural scanner"
// that naive regex (strategy C) is measured against.
func dividePythonIndent(filePath string, src []byte) []Chunk {
	var bounds []int
	lineStart := 0
	depth := 0        // () [] {} depth
	contNext := false // previous line ended with a backslash
	inTriple := byte(0)
	prevBlank := true // previous line was blank (or start of file)

	for lineStart < len(src) {
		nl := bytes.IndexByte(src[lineStart:], '\n')
		lineEnd := len(src)
		if nl >= 0 {
			lineEnd = lineStart + nl
		}
		line := src[lineStart:lineEnd]
		trimmed := bytes.TrimSpace(line)

		if inTriple == 0 && depth == 0 && !contNext &&
			len(trimmed) > 0 &&
			// A boundary goes at every top-level statement start: declared
			// functions/classes/decorators, but also module-level
			// statements like `if __name__ == "__main__":` (any column-0
			// statement that begins after a blank line). Bracket-depth and
			// continuation tracking keep us out of multi-line statements;
			// the triple-quote state keeps us out of strings.
			(pyIsDeclStart(trimmed) || (prevBlank && !pyIsNonUnitLine(trimmed))) {
			bounds = append(bounds, lineStart)
		}

		inTriple, depth, contNext = pyScanLine(line, inTriple, depth)
		prevBlank = len(trimmed) == 0
		lineStart = lineEnd + 1
	}

	if len(bounds) == 0 {
		return fallbackChunks(filePath, "python", src)
	}
	return buildChunks(filePath, "python", src, KindStructural, bounds)
}

func pyIsDeclStart(trimmed []byte) bool {
	for _, p := range []string{"def ", "def(", "class ", "async def ", "@"} {
		if bytes.HasPrefix(trimmed, []byte(p)) {
			return true
		}
	}
	return false
}

// pyIsNonUnitLine reports column-0 lines that never start a chunk of
// their own: comments and import statements fold into the following
// chunk, mirroring the AST strategies' skip sets.
func pyIsNonUnitLine(trimmed []byte) bool {
	return bytes.HasPrefix(trimmed, []byte("#")) ||
		bytes.HasPrefix(trimmed, []byte("import ")) ||
		bytes.HasPrefix(trimmed, []byte("from "))
}
