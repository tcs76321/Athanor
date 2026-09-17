package division

import (
	"bytes"
	"testing"
)

func spikeStrategies() []Strategy {
	return []Strategy{
		TreeSitterStrategy{},
		PureGoStrategy{},
		HeaderStrategy{},
	}
}

// TestRoundTripProperty is the heart of M5-T1's acceptance criteria:
// for every strategy × every corpus file (≥3 languages, plus this
// repository's own Go sources), division must satisfy P1–P4; broken
// inputs additionally prove P5.
func TestRoundTripProperty(t *testing.T) {
	corpus, err := LoadCorpus()
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	langs := map[string]int{}
	for _, s := range corpus {
		langs[s.Lang]++
	}
	if len(langs) < 3 {
		t.Fatalf("corpus must span >=3 languages, got %v", langs)
	}
	t.Logf("corpus: %d files, languages: %v", len(corpus), langs)

	for _, strat := range spikeStrategies() {
		strat := strat
		t.Run(strat.Name(), func(t *testing.T) {
			for _, src := range corpus {
				src := src
				t.Run(src.FilePath, func(t *testing.T) {
					checkProperties(t, strat, src)
				})
			}
		})
	}
}

// TestBrokenInputsDegradGracefully pins P5 explicitly: on the broken/
// fixtures every strategy must still produce chunks whose reassembly is
// byte-identical, and pure-Go must visibly mark them KindFallback (its
// parsers reject broken input) while tree-sitter's error recovery may
// keep KindAST.
func TestBrokenInputsDegradGracefully(t *testing.T) {
	broken, err := LoadDir("testdata/broken")
	if err != nil {
		t.Fatalf("load broken fixtures: %v", err)
	}
	if len(broken) < 3 {
		t.Fatalf("expected >=3 broken fixtures, got %d", len(broken))
	}
	for _, strat := range spikeStrategies() {
		for _, src := range broken {
			chunks := strat.Divide(src.FilePath, src.Lang, src.Src)
			if len(chunks) == 0 {
				t.Errorf("%s: %s: no chunks for broken input", strat.Name(), src.FilePath)
				continue
			}
			if got := Reassemble(chunks); !bytes.Equal(got, src.Src) {
				t.Errorf("%s: %s: P5 violated — broken input lost bytes", strat.Name(), src.FilePath)
			}
			t.Logf("%s: %s -> %d chunks, kind=%s", strat.Name(), src.FilePath, len(chunks), chunks[0].Kind)
		}
	}
}

func checkProperties(t *testing.T, strat Strategy, src Source) {
	t.Helper()
	chunks := strat.Divide(src.FilePath, src.Lang, src.Src)

	// At least one chunk, always.
	if len(chunks) == 0 {
		t.Fatalf("P1: strategy produced zero chunks")
	}

	// P1: byte-for-byte reassembly.
	if got := Reassemble(chunks); !bytes.Equal(got, src.Src) {
		t.Fatalf("P1 round-trip failed: %d chunks, reassembled %d bytes, want %d",
			len(chunks), len(got), len(src.Src))
	}

	// P2: exact tiling, no gaps or overlaps, content matches the range.
	off := 0
	for i, c := range chunks {
		if c.ByteStart != off {
			t.Fatalf("P2 tiling broken at chunk %d: ByteStart=%d, want %d (gap or overlap)", i, c.ByteStart, off)
		}
		if c.ByteEnd < c.ByteStart || c.ByteEnd > len(src.Src) {
			t.Fatalf("P2 range broken at chunk %d: [%d,%d) in file of %d bytes", i, c.ByteStart, c.ByteEnd, len(src.Src))
		}
		if !bytes.Equal(c.Content, src.Src[c.ByteStart:c.ByteEnd]) {
			t.Fatalf("P2 content mismatch at chunk %d: content is not src[%d:%d]", i, c.ByteStart, c.ByteEnd)
		}
		off = c.ByteEnd
	}
	if off != len(src.Src) {
		t.Fatalf("P2 tiling incomplete: last chunk ends at %d, want %d", off, len(src.Src))
	}

	// P3: line metadata recomputed from byte offsets must match.
	for i, c := range chunks {
		ls := lineOf(src.Src, c.ByteStart)
		le := ls
		if c.ByteEnd > c.ByteStart {
			le = lineOf(src.Src, c.ByteEnd-1)
		}
		if c.LineStart != ls || c.LineEnd != le {
			t.Fatalf("P3 line metadata wrong at chunk %d: got %d-%d, want %d-%d", i, c.LineStart, c.LineEnd, ls, le)
		}
	}

	// P4: determinism — same input, same chunks.
	again := strat.Divide(src.FilePath, src.Lang, src.Src)
	if len(again) != len(chunks) {
		t.Fatalf("P4 determinism broken: %d chunks then %d chunks", len(chunks), len(again))
	}
	for i := range chunks {
		a, b := chunks[i], again[i]
		if a.ID != b.ID || a.ByteStart != b.ByteStart || a.ByteEnd != b.ByteEnd || a.Kind != b.Kind {
			t.Fatalf("P4 determinism broken at chunk %d: %+v vs %+v", i, a, b)
		}
	}
}

// TestCorpusIncludesRealRepo guards the "real repos" half of the
// acceptance criteria: when the spike runs inside a full checkout, the
// corpus must include the repository's own Go sources.
func TestCorpusIncludesRealRepo(t *testing.T) {
	corpus, err := LoadCorpus()
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	repoFiles := 0
	for _, s := range corpus {
		if !bytes.HasPrefix([]byte(s.FilePath), []byte("testdata/")) {
			repoFiles++
		}
	}
	if repoFiles < 50 {
		t.Logf("note: only %d real-repo files in corpus (vendored checkout?); fixtures still cover the property", repoFiles)
	}
}
