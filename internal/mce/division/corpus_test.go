package division

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// corpusSource is one file the property suite divides.
type corpusSource struct {
	RelPath string
	Lang    string
	Src     []byte
}

// loadDir walks root and returns every file whose extension maps to a
// division language. When skipTestdata is true, directories named
// "testdata" are pruned (used for the repo walk, so committed fixtures are
// not counted twice).
func loadDir(root string, skipTestdata bool) ([]corpusSource, error) {
	var out []corpusSource
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipTestdata && d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		lang := LangFor(d.Name())
		if lang == "" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, corpusSource{RelPath: filepath.ToSlash(rel), Lang: lang, Src: src})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// loadCorpus returns the M5-T2 property corpus: the committed testdata
// fixtures plus this repository's own Go sources (the "real repo" half of
// the M5-T1 corpus, minus testdata so fixtures are not double-counted).
func loadCorpus(t *testing.T) []corpusSource {
	t.Helper()
	fixtures, err := loadDir("testdata", false)
	if err != nil {
		t.Fatalf("load fixtures: %v", err)
	}
	repo, err := loadDir(filepath.Join("..", ".."), true)
	if err != nil {
		t.Fatalf("load repo corpus: %v", err)
	}
	return append(fixtures, repo...)
}

// edgeCases are byte-level edge inputs generated in-test rather than
// committed, so VCS newline normalization and the Gate G1 ".go under
// internal/" parse walk can never touch them.
func edgeCases() []corpusSource {
	return []corpusSource{
		{RelPath: "edge/empty.py", Lang: "python", Src: []byte{}},
		{RelPath: "edge/only-newline.txt", Lang: "text", Src: []byte("\n")},
		{RelPath: "edge/crlf.js", Lang: "javascript",
			Src: []byte("const a = 1;\r\nconst b = 2;\r\nfunction c() { return a; }\r\n")},
		{RelPath: "edge/no-trailing-newline.py", Lang: "python",
			Src: []byte("def a():\n    return 1\n\ndef b():\n    return 2")},
		{RelPath: "edge/long-line.js", Lang: "javascript",
			Src: []byte("const x = 1;\n" + strings.Repeat("// pad ", 4000) + "\n")},
		{RelPath: "edge/unicode-bytes.md", Lang: "markdown",
			Src: []byte("# Höhe\n\nÜber die Straße.\n\n## 日本語\n\nテキスト\n")},
		{RelPath: "edge/null-bytes.txt", Lang: "text",
			Src: append([]byte("before\n"), append(make([]byte, 3), []byte("\nafter\n")...)...)},
	}
}

// brokenCases are syntactically invalid sources that prove P5: a parse
// failure must degrade to the fallback splitter without losing a byte.
func brokenCases() []corpusSource {
	return []corpusSource{
		{RelPath: "broken/a.go", Lang: "go", Src: []byte("package a\n\nfunc ( {\n\treturn\n")},
		{RelPath: "broken/b.py", Lang: "python", Src: []byte("def (\n  return\nclass :\n\tpass\n")},
		{RelPath: "broken/c.js", Lang: "javascript", Src: []byte("function { const ) }\n")},
	}
}

// lineStarts returns the byte offset at which each line begins.
func lineStarts(src []byte) []int {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// lineFor is an independent (binary-search) computation of the 1-based
// line containing off, used to cross-check Chunk.LineStart/LineEnd.
func lineFor(starts []int, off int) int {
	lo, hi := 0, len(starts)
	for lo < hi {
		mid := (lo + hi) / 2
		if starts[mid] <= off {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo // lo-1 is the index; 1-based line = (lo-1)+1 = lo
}

// checkProperties asserts P1–P3 for one division of src.
func checkProperties(t *testing.T, d *Divider, s corpusSource) {
	t.Helper()
	chunks := d.Divide(s.RelPath, s.Lang, s.Src)
	if len(chunks) == 0 {
		t.Fatalf("%s: zero chunks", s.RelPath)
	}
	// P1: byte-identical reassembly.
	if got := Reassemble(chunks); !bytes.Equal(got, s.Src) {
		t.Fatalf("%s: P1 reassembled %d bytes, want %d", s.RelPath, len(got), len(s.Src))
	}
	// P2: exact tiling.
	if chunks[0].ByteStart != 0 {
		t.Fatalf("%s: P2 first chunk starts at %d", s.RelPath, chunks[0].ByteStart)
	}
	if last := chunks[len(chunks)-1].ByteEnd; last != len(s.Src) {
		t.Fatalf("%s: P2 last chunk ends at %d, want %d", s.RelPath, last, len(s.Src))
	}
	starts := lineStarts(s.Src)
	seen := map[string]bool{}
	for i := range chunks {
		c := chunks[i]
		if i > 0 && c.ByteStart != chunks[i-1].ByteEnd {
			t.Fatalf("%s: P2 gap/overlap at chunk %d", s.RelPath, i)
		}
		if !bytes.Equal(c.Content, s.Src[c.ByteStart:c.ByteEnd]) {
			t.Fatalf("%s: chunk %d content != source range", s.RelPath, i)
		}
		// P3: line metadata, independently recomputed.
		if len(s.Src) > 0 {
			if want := lineFor(starts, c.ByteStart); c.LineStart != want {
				t.Fatalf("%s: chunk %d LineStart=%d want %d", s.RelPath, i, c.LineStart, want)
			}
			if want := lineFor(starts, c.ByteEnd-1); c.LineEnd != want {
				t.Fatalf("%s: chunk %d LineEnd=%d want %d", s.RelPath, i, c.LineEnd, want)
			}
		}
		// Chunk IDs must be unique within a source.
		if seen[c.ID] {
			t.Fatalf("%s: duplicate chunk ID %s", s.RelPath, c.ID)
		}
		seen[c.ID] = true
	}
}

// TestRoundTripProperty is M5-T2's acceptance test: for every corpus file,
// division must satisfy P1 (byte-identical reassembly), P2 (exact tiling),
// and P3 (line metadata). The corpus spans the committed fixtures, this
// repository's own Go sources, and in-test byte-level edge cases.
func TestRoundTripProperty(t *testing.T) {
	d := New(Options{})
	corpus := append(loadCorpus(t), edgeCases()...)
	langs := map[string]int{}
	for _, s := range corpus {
		langs[s.Lang]++
	}
	t.Logf("corpus: %d files, languages: %v", len(corpus), langs)
	if len(langs) < 4 {
		t.Fatalf("corpus must span >=4 languages, got %v", langs)
	}
	for _, s := range corpus {
		checkProperties(t, d, s)
	}
}

// TestEdgeCasesAreHermetic pins that the generated edge inputs divide
// cleanly. They are generated in-test (not committed) so VCS newline
// normalization and the Gate G1 ".go under internal/" parse walk cannot
// touch them.
func TestEdgeCasesAreHermetic(t *testing.T) {
	d := New(Options{})
	for _, s := range edgeCases() {
		checkProperties(t, d, s)
	}
}

// TestBrokenInputsDegrade proves P5: a parse failure degrades to the
// fallback splitter (or tree-sitter's error recovery) and never loses a
// byte. The sources are generated in-test, never written as .go fixtures.
func TestBrokenInputsDegrade(t *testing.T) {
	d := New(Options{})
	for _, s := range brokenCases() {
		chunks := d.Divide(s.RelPath, s.Lang, s.Src)
		if len(chunks) == 0 {
			t.Fatalf("%s: zero chunks for broken input", s.RelPath)
		}
		if got := Reassemble(chunks); !bytes.Equal(got, s.Src) {
			t.Fatalf("%s: P5 lost bytes on broken input", s.RelPath)
		}
		switch chunks[0].Kind {
		case KindAST, KindFallback:
			// both are acceptable: error recovery keeps AST boundaries,
			// otherwise the fallback tiles fixed blocks
		default:
			t.Errorf("%s: unexpected kind %q", s.RelPath, chunks[0].Kind)
		}
	}
}

// TestDeterminismAcrossCorpus pins P4: dividing the same source twice
// yields identical chunk IDs and byte ranges.
func TestDeterminismAcrossCorpus(t *testing.T) {
	d := New(Options{})
	for _, s := range append(loadCorpus(t), edgeCases()...) {
		a := d.Divide(s.RelPath, s.Lang, s.Src)
		b := d.Divide(s.RelPath, s.Lang, s.Src)
		if len(a) != len(b) {
			t.Fatalf("%s: chunk counts differ: %d vs %d", s.RelPath, len(a), len(b))
		}
		for i := range a {
			if a[i].ID != b[i].ID || a[i].ByteStart != b[i].ByteStart || a[i].ByteEnd != b[i].ByteEnd {
				t.Fatalf("%s: chunk %d not deterministic", s.RelPath, i)
			}
		}
	}
}

// goDeclOffsets returns the start byte offset of every top-level
// declaration in a Go source, using go/parser as independent ground truth.
// nil means the file did not parse (the caller skips it).
func goDeclOffsets(rel string, src []byte) map[int]bool {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	tf := fset.File(f.Pos())
	if tf == nil {
		return nil
	}
	truth := map[int]bool{}
	for _, decl := range f.Decls {
		if off := tf.Offset(decl.Pos()); off > 0 {
			truth[off] = true
		}
	}
	return truth
}

// TestBoundaryAgreementGo measures how well tree-sitter boundaries agree
// with go/parser declaration starts. Correctness is P1–P5 (the round-trip
// test); this is the quality proxy ADR-0020 describes. tree-sitter folds
// the package clause and imports into the first declaration, so recall is
// below 1 by design; precision should be ~1.
func TestBoundaryAgreementGo(t *testing.T) {
	d := New(Options{})
	var boundaries, hits, truthTotal, files int
	for _, s := range loadCorpus(t) {
		if s.Lang != "go" {
			continue
		}
		truth := goDeclOffsets(s.RelPath, s.Src)
		if truth == nil {
			continue
		}
		for _, c := range d.Divide(s.RelPath, s.Lang, s.Src) {
			if c.ByteStart == 0 {
				continue
			}
			boundaries++
			if truth[c.ByteStart] {
				hits++
			}
		}
		truthTotal += len(truth)
		files++
	}
	if files < 50 {
		t.Fatalf("expected a real Go corpus (>=50 files), got %d", files)
	}
	if boundaries == 0 || truthTotal == 0 {
		t.Fatal("no boundaries or no ground truth collected")
	}
	precision := float64(hits) / float64(boundaries)
	recall := float64(hits) / float64(truthTotal)
	t.Logf("Go boundary agreement: %d files, %d boundaries, precision=%.4f recall=%.4f",
		files, boundaries, precision, recall)
	if precision < 0.99 {
		t.Errorf("precision %.4f < 0.99 (every tree-sitter boundary should be a declaration start)", precision)
	}
	if recall < 0.80 {
		t.Errorf("recall %.4f < 0.80", recall)
	}
}
