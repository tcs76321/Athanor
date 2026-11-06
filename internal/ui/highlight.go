package ui

import (
	"html"
	"html/template"
	"path"
	"strconv"
	"strings"
)

// highlightCode renders one file as escaped, lightly highlighted source. It is
// deliberately a small tokenizer, not a full parser: comments, strings,
// numbers, and a per-language keyword set give the reader shape without adding
// a dependency. Every byte is escaped, so model output cannot inject markup.
//
// Line numbers live in a separate gutter (see artifactFile.Gutter), not in the
// <pre>, so the copy button copies source only.
func highlightCode(name, src string) template.HTML {
	lang := languageForPath(name)
	var b strings.Builder
	inBlockComment := false
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(highlightLine(line, lang, &inBlockComment))
	}
	return template.HTML(b.String()) // #nosec G203 -- every fragment is escaped below.
}

// lineNumbers builds the gutter text for a file. A trailing newline is not
// counted as an extra line.
func lineNumbers(src string) string {
	n := strings.Count(src, "\n") + 1
	if strings.HasSuffix(src, "\n") {
		n--
	}
	if n < 1 {
		n = 1
	}
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if i > 1 {
			b.WriteByte('\n')
		}
		b.WriteString(strconv.Itoa(i))
	}
	return b.String()
}

type langSpec struct {
	lineComment  string
	blockComment bool
	keywords     map[string]bool
}

func languageForPath(name string) *langSpec {
	switch strings.ToLower(path.Ext(name)) {
	case ".py":
		return pySpec
	case ".go":
		return goSpec
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx":
		return jsSpec
	case ".rs":
		return rsSpec
	case ".java", ".kt":
		return javaSpec
	case ".rb":
		return rbSpec
	case ".sh", ".bash", ".zsh":
		return shSpec
	case ".yml", ".yaml":
		return yamlSpec
	case ".json":
		return jsonSpec
	default:
		return plainSpec
	}
}

var (
	plainSpec = &langSpec{}
	pySpec    = &langSpec{lineComment: "#", keywords: words(
		"and", "as", "assert", "async", "await", "break", "class", "continue", "def", "del",
		"elif", "else", "except", "finally", "for", "from", "global", "if", "import", "in",
		"is", "lambda", "nonlocal", "not", "or", "pass", "raise", "return", "try", "while",
		"with", "yield", "True", "False", "None",
	)}
	goSpec = &langSpec{lineComment: "//", blockComment: true, keywords: words(
		"break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough",
		"for", "func", "go", "goto", "if", "import", "interface", "map", "package", "range",
		"return", "select", "struct", "switch", "type", "var", "true", "false", "nil",
	)}
	jsSpec = &langSpec{lineComment: "//", blockComment: true, keywords: words(
		"async", "await", "break", "case", "catch", "class", "const", "continue", "debugger",
		"default", "delete", "do", "else", "export", "extends", "false", "finally", "for",
		"function", "if", "import", "in", "instanceof", "let", "new", "null", "of", "return",
		"static", "super", "switch", "this", "throw", "true", "try", "typeof", "undefined",
		"var", "void", "while", "yield",
	)}
	rsSpec = &langSpec{lineComment: "//", blockComment: true, keywords: words(
		"as", "async", "await", "break", "const", "continue", "crate", "dyn", "else", "enum",
		"extern", "false", "fn", "for", "if", "impl", "in", "let", "loop", "match", "mod",
		"move", "mut", "pub", "ref", "return", "self", "Self", "static", "struct", "super",
		"trait", "true", "type", "unsafe", "use", "where", "while",
	)}
	javaSpec = &langSpec{lineComment: "//", blockComment: true, keywords: words(
		"abstract", "assert", "boolean", "break", "byte", "case", "catch", "char", "class",
		"const", "continue", "default", "do", "double", "else", "enum", "extends", "final",
		"finally", "float", "for", "if", "implements", "import", "instanceof", "int",
		"interface", "long", "new", "package", "private", "protected", "public", "return",
		"short", "static", "strictfp", "super", "switch", "synchronized", "this", "throw",
		"throws", "transient", "try", "void", "volatile", "while", "true", "false", "null",
	)}
	rbSpec = &langSpec{lineComment: "#", keywords: words(
		"alias", "and", "begin", "break", "case", "class", "def", "defined", "do", "else",
		"elsif", "end", "ensure", "false", "for", "if", "in", "module", "next", "nil", "not",
		"or", "redo", "rescue", "retry", "return", "self", "super", "then", "true", "undef",
		"unless", "until", "when", "while", "yield",
	)}
	shSpec = &langSpec{lineComment: "#", keywords: words(
		"case", "do", "done", "elif", "else", "esac", "fi", "for", "function", "if", "in",
		"select", "then", "until", "while", "export", "local", "readonly", "return", "true",
		"false",
	)}
	yamlSpec = &langSpec{lineComment: "#", keywords: words("true", "false", "null", "yes", "no")}
	jsonSpec = &langSpec{keywords: words("true", "false", "null")}
)

func words(ws ...string) map[string]bool {
	m := make(map[string]bool, len(ws))
	for _, w := range ws {
		m[w] = true
	}
	return m
}

func highlightLine(line string, lang *langSpec, inBlock *bool) string {
	var b strings.Builder
	i := 0
	for i < len(line) {
		if *inBlock {
			end := strings.Index(line[i:], "*/")
			if end < 0 {
				b.WriteString(span("com", line[i:]))
				return b.String()
			}
			b.WriteString(span("com", line[i:i+end+2]))
			i += end + 2
			*inBlock = false
			continue
		}
		if lang.blockComment && strings.HasPrefix(line[i:], "/*") {
			*inBlock = true
			end := strings.Index(line[i+2:], "*/")
			if end < 0 {
				b.WriteString(span("com", line[i:]))
				return b.String()
			}
			b.WriteString(span("com", line[i:i+2+end+2]))
			i += 2 + end + 2
			*inBlock = false
			continue
		}
		if lang.lineComment != "" && strings.HasPrefix(line[i:], lang.lineComment) {
			b.WriteString(span("com", line[i:]))
			return b.String()
		}
		c := line[i]
		if c == '"' || c == '\'' || c == '`' {
			end := i + 1
			for end < len(line) {
				if line[end] == '\\' && c != '`' {
					end += 2
					continue
				}
				if line[end] == c {
					end++
					break
				}
				end++
			}
			if end > len(line) {
				end = len(line)
			}
			b.WriteString(span("str", line[i:end]))
			i = end
			continue
		}
		if c >= '0' && c <= '9' {
			end := i + 1
			for end < len(line) {
				d := line[end]
				if (d >= '0' && d <= '9') || d == '.' || d == '_' || (d >= 'a' && d <= 'f') || (d >= 'A' && d <= 'F') || d == 'x' || d == 'X' {
					end++
					continue
				}
				break
			}
			b.WriteString(span("num", line[i:end]))
			i = end
			continue
		}
		if isIdentStart(c) {
			end := i + 1
			for end < len(line) && isIdentPart(line[end]) {
				end++
			}
			tok := line[i:end]
			if lang.keywords[tok] {
				b.WriteString(span("kw", tok))
			} else {
				b.WriteString(html.EscapeString(tok))
			}
			i = end
			continue
		}
		b.WriteString(html.EscapeString(string(c)))
		i++
	}
	return b.String()
}

func span(kind, text string) string {
	return `<span class="tok-` + kind + `">` + html.EscapeString(text) + `</span>`
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// highlightLanguageLabel is a small display label for a file's syntax.
func highlightLanguageLabel(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".py":
		return "Python"
	case ".go":
		return "Go"
	case ".js", ".mjs", ".cjs":
		return "JavaScript"
	case ".ts":
		return "TypeScript"
	case ".rs":
		return "Rust"
	case ".java":
		return "Java"
	case ".rb":
		return "Ruby"
	case ".sh", ".bash":
		return "Shell"
	case ".yml", ".yaml":
		return "YAML"
	case ".json":
		return "JSON"
	default:
		return ""
	}
}
