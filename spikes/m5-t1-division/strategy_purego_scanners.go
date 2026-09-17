package division

import "bytes"

// pyScanLine advances the Python string/bracket state across one line.
// Returns the new triple-quote state, bracket depth, and whether the
// line ends with an explicit backslash continuation.
func pyScanLine(line []byte, inTriple byte, depth int) (byte, int, bool) {
	i := 0
	for i < len(line) {
		c := line[i]
		switch {
		case inTriple != 0:
			if c == '\\' {
				i += 2
				continue
			}
			if c == inTriple && i+2 < len(line) && line[i+1] == inTriple && line[i+2] == inTriple {
				inTriple = 0
				i += 3
				continue
			}
			i++
		case c == '#':
			i = len(line) // comment to end of line
		case c == '"' || c == '\'':
			if i+2 < len(line) && line[i+1] == c && line[i+2] == c {
				inTriple = c
				i += 3
				continue
			}
			i++ // single-quoted string: skip to closing quote, honoring escapes
			for i < len(line) {
				if line[i] == '\\' {
					i += 2
					continue
				}
				if line[i] == c {
					i++
					break
				}
				i++
			}
		case c == '(' || c == '[' || c == '{':
			depth++
			i++
		case c == ')' || c == ']' || c == '}':
			if depth > 0 {
				depth--
			}
			i++
		default:
			i++
		}
	}
	trimmed := bytes.TrimSpace(line)
	cont := len(trimmed) > 0 && trimmed[len(trimmed)-1] == '\\'
	return inTriple, depth, cont
}

// divideBraceDepth chunks JavaScript at brace-depth-zero boundaries,
// tracking single/double/template strings and comments. A boundary is
// placed at the next content line start after the closing brace, so a
// chunk begins where a declaration begins (matching AST strategies).
// If the file ends unbalanced, the scan is untrusted and P5 kicks in
// (fallback).
func divideBraceDepth(filePath string, src []byte) []Chunk {
	var bounds []int
	depth := 0
	inStr := byte(0) // '"', '\'', '`'
	inLineComment := false
	inBlockComment := false
	escaped := false
	pending := false
	candidate := -1

	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case inLineComment:
			if c == '\n' {
				inLineComment = false
				if pending {
					candidate = i + 1
				}
			}
		case inBlockComment:
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				inBlockComment = false
				i++
			}
		case inStr != 0:
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == inStr {
				inStr = 0
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			inLineComment = true
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			inBlockComment = true
			i++
		case c == '"' || c == '\'' || c == '`':
			inStr = c
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				pending = true
				candidate = i + 1 // if the next char is a newline this becomes the line start
			}
		case c == ';':
			// A top-level statement terminator (const/let/var/expression)
			// also ends a unit; the next content line starts a new chunk.
			if depth == 0 && !pending {
				pending = true
				candidate = i + 1
			}
		case c == '\n':
			if pending {
				candidate = i + 1 // keep sliding to the next line start
			}
		default:
			if pending && c != ' ' && c != '\t' && c != '\r' {
				bounds = append(bounds, candidate)
				pending = false
			}
		}
	}
	if depth != 0 || inStr != 0 || inBlockComment {
		return fallbackChunks(filePath, "javascript", src)
	}
	if pending && candidate > 0 && candidate < len(src) {
		bounds = append(bounds, candidate)
	}
	if len(bounds) == 0 {
		return fallbackChunks(filePath, "javascript", src)
	}
	return buildChunks(filePath, "javascript", src, KindStructural, bounds)
}
