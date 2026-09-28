package division

import (
	"sync"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

// tsGrammar pairs a language pointer with the root-child kinds that do not
// begin a chunk of their own: package clauses, imports, and bare comments
// fold into the following declaration via tiling (the "header and imports
// attach to the first declaration" behavior from the M5-T1 spike).
type tsGrammar struct {
	lang *sitter.Language
	skip map[string]bool
}

var (
	grammarOnce sync.Once
	grammars    map[string]tsGrammar
)

// grammarFor lazily builds the grammar registry exactly once (ADR-0021 §4).
// Language values are immutable and shared across goroutines.
func grammarFor(lang string) (tsGrammar, bool) {
	grammarOnce.Do(func() {
		grammars = map[string]tsGrammar{
			"go": {
				lang: sitter.NewLanguage(tree_sitter_go.Language()),
				skip: map[string]bool{
					"package_clause":     true,
					"import_declaration": true,
					"comment":            true,
				},
			},
			"python": {
				lang: sitter.NewLanguage(tree_sitter_python.Language()),
				skip: map[string]bool{
					"import_statement":      true,
					"import_from_statement": true,
					"comment":               true,
				},
			},
			"javascript": {
				lang: sitter.NewLanguage(tree_sitter_javascript.Language()),
				skip: map[string]bool{
					"import_statement": true,
					"comment":          true,
				},
			},
		}
	})
	g, ok := grammars[lang]
	return g, ok
}

// parserPool amortizes tree-sitter parser construction (ADR-0021 §4). A
// Parser is not safe for concurrent use, so each Divide call borrows one;
// the tree is closed before the parser is returned to the pool.
var parserPool = sync.Pool{New: func() any { return sitter.NewParser() }}

// tsChunks divides a code source with tree-sitter, chunking at the start
// byte of each meaningful named child of the parse-tree root. ok is false
// when there is no grammar or the parse fails, so the caller falls back
// (P5). Tree-sitter error-recovers on broken files: the root still spans
// the document, so boundaries remain valid tiling offsets.
func (d *Divider) tsChunks(filePath, lang string, src []byte) ([]Chunk, bool) {
	g, ok := grammarFor(lang)
	if !ok || g.lang == nil {
		return nil, false
	}
	parser, _ := parserPool.Get().(*sitter.Parser)
	if parser == nil {
		return nil, false
	}
	// Return the parser after the tree is closed (defers run LIFO).
	defer parserPool.Put(parser)
	if err := parser.SetLanguage(g.lang); err != nil {
		return nil, false
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, false
	}
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
		return nil, false
	}
	return d.bounded(filePath, lang, src, KindAST, bounds), true
}
