package division

import (
	"bytes"
	"testing"
)

// assertTiles checks P1 (byte-identical reassembly) and P2 (exact tiling)
// for a division of src. T2.3 grows this into the full corpus property
// suite; T2.2 only needs the contract pinned on small inputs.
func assertTiles(t *testing.T, chunks []Chunk, src []byte) {
	t.Helper()
	if len(chunks) == 0 {
		t.Fatal("division produced zero chunks")
	}
	if got := Reassemble(chunks); !bytes.Equal(got, src) {
		t.Fatalf("P1: reassembled %d bytes, want %d", len(got), len(src))
	}
	if chunks[0].ByteStart != 0 {
		t.Fatalf("P2: first chunk starts at %d, want 0", chunks[0].ByteStart)
	}
	if n := chunks[len(chunks)-1].ByteEnd; n != len(src) {
		t.Fatalf("P2: last chunk ends at %d, want %d", n, len(src))
	}
	for i := range chunks {
		c := chunks[i]
		if len(src) > 0 && c.ByteEnd <= c.ByteStart {
			t.Fatalf("P2: chunk %d has empty range [%d,%d)", i, c.ByteStart, c.ByteEnd)
		}
		if i > 0 && c.ByteStart != chunks[i-1].ByteEnd {
			t.Fatalf("P2: gap/overlap between chunk %d and %d", i-1, i)
		}
	}
}

func TestDivideGoUsesASTAndTiles(t *testing.T) {
	src := []byte("package demo\n\nimport \"fmt\"\n\nfunc A() { fmt.Println(\"a\") }\n\nfunc B() {}\n")
	chunks := New(Options{}).Divide("demo.go", "", src)
	assertTiles(t, chunks, src)
	if chunks[0].Kind != KindAST {
		t.Errorf("kind = %s, want ast", chunks[0].Kind)
	}
}

func TestDivideMarkdownUsesHeaders(t *testing.T) {
	src := []byte("# Title\n\nintro\n\n## Section\n\nbody\n")
	chunks := New(Options{}).Divide("doc.md", "", src)
	assertTiles(t, chunks, src)
	if chunks[0].Kind != KindHeader {
		t.Errorf("kind = %s, want header", chunks[0].Kind)
	}
}

func TestDivideUnknownFallsBack(t *testing.T) {
	src := []byte("line one\nline two\nline three\n")
	chunks := New(Options{}).Divide("data.xyz", "", src)
	assertTiles(t, chunks, src)
	if chunks[0].Kind != KindFallback {
		t.Errorf("kind = %s, want fallback", chunks[0].Kind)
	}
}

func TestDivideEmptySource(t *testing.T) {
	chunks := New(Options{}).Divide("empty.go", "", []byte{})
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	assertTiles(t, chunks, []byte{})
}

func TestDivideIsDeterministic(t *testing.T) {
	src := []byte("package demo\n\nfunc A() {}\n\nfunc B() {}\n")
	d := New(Options{})
	a := d.Divide("a.go", "", src)
	b := d.Divide("a.go", "", src)
	if len(a) != len(b) {
		t.Fatalf("chunk counts differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].ByteStart != b[i].ByteStart || a[i].ByteEnd != b[i].ByteEnd {
			t.Fatalf("chunk %d not deterministic: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestLangFor(t *testing.T) {
	cases := map[string]string{
		"a.go": "go", "a.py": "python", "a.js": "javascript", "a.mjs": "javascript",
		"a.md": "markdown", "a.txt": "text", "a.xyz": "",
	}
	for name, want := range cases {
		if got := LangFor(name); got != want {
			t.Errorf("LangFor(%q) = %q, want %q", name, got, want)
		}
	}
}
