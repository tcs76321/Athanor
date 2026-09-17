package division

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

// TreeSitterStrategy (candidate A) chunks at the start byte of each
// meaningful named child of the parse-tree root. This is the real-AST
// candidate: one chunking model, many languages, error-recovery parsing.
//
// Children whose type is in the per-language skip set (package clause,
// imports, bare comments) do not get a boundary of their own; tiling
// folds them into the following chunk, which is the desired "header and
// imports attach to the first declaration" behavior.
//
// Tree-sitter is error-recovering: even a syntactically broken file
// yields a root spanning the whole document, so boundaries remain valid
// tiling offsets and P1/P2 hold without fallback.
type TreeSitterStrategy struct{}

func (TreeSitterStrategy) Name() string { return "tree-sitter" }

// tsGrammar pairs a language pointer with its root-child skip set.
type tsGrammar struct {
	lang  *sitter.Language
	skip  map[string]bool
	note  string
	valid bool
}

func tsGrammars() map[string]tsGrammar {
	return map[string]tsGrammar{
		"go": {
			lang: sitter.NewLanguage(tree_sitter_go.Language()),
			skip: map[string]bool{
				"package_clause":     true,
				"import_declaration": true,
				"comment":            true,
			},
			note:  "github.com/tree-sitter/tree-sitter-go v0.25.0",
			valid: true,
		},
		"python": {
			lang: sitter.NewLanguage(tree_sitter_python.Language()),
			skip: map[string]bool{
				"import_statement":      true,
				"import_from_statement": true,
				"comment":               true,
			},
			note:  "github.com/tree-sitter/tree-sitter-python v0.25.0",
			valid: true,
		},
		"javascript": {
			lang: sitter.NewLanguage(tree_sitter_javascript.Language()),
			skip: map[string]bool{
				"import_statement": true,
				"comment":          true,
			},
			note:  "github.com/tree-sitter/tree-sitter-javascript v0.25.0",
			valid: true,
		},
		// markdown: deliberately unsupported here — §10.1 prescribes
		// structural-header division for text/markdown, which strategy C
		// provides. Recorded as an explicit boundary of strategy A.
		"markdown": {note: "not attempted: header division (strategy C) is the §10.1 prescription for markdown"},
	}
}

func (TreeSitterStrategy) Divide(filePath, lang string, src []byte) []Chunk {
	g, ok := tsGrammars()[lang]
	if !ok || !g.valid || g.lang == nil {
		return fallbackChunks(filePath, lang, src)
	}
	return tsChunks(filePath, lang, src, g)
}

func tsChunks(filePath, lang string, src []byte, g tsGrammar) []Chunk {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(g.lang); err != nil {
		return fallbackChunks(filePath, lang, src)
	}
	tree := parser.Parse(src, nil)
	defer tree.Close()

	root := tree.RootNode()
	var bounds []int
	for i := uint(0); i < root.NamedChildCount(); i++ {
		child := root.NamedChild(i)
		if child == nil || g.skip[child.Kind()] {
			continue
		}
		if sb := int(child.StartByte()); sb > 0 && sb < len(src) {
			bounds = append(bounds, sb)
		}
	}
	if len(bounds) == 0 {
		return fallbackChunks(filePath, lang, src)
	}
	return buildChunks(filePath, lang, src, KindAST, bounds)
}
