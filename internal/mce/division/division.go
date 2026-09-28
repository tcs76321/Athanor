// Package division implements the §10.1 Full-Fidelity Division engine for
// the Multidimensional Context Engine (ARCHITECTURE §10.1; ROADMAP M5-T2;
// ADR-0020 strategy, ADR-0021 storage/identity).
//
// It splits a byte source into dormant chunks whose boundaries follow code
// structure (tree-sitter) or structural headers (markdown / plain text),
// with a fixed-line-block fallback for anything it cannot parse. The
// package is pure: no I/O beyond the bytes it is handed, no LLM import,
// no state beyond an optional pooled parser.
//
// The contract every division satisfies — the byte-partition property
// M5-T2's tests pin:
//
//	P1  Reassemble(Divide(src)) == src           (byte-identical)
//	P2  chunk byte ranges tile [0, len(src))     (no gaps, no overlap)
//	P3  LineStart/LineEnd recomputed from byte offsets match the fields
//	P4  division is deterministic                (same input → same chunks)
//	P5  a parse failure degrades to fixed-line blocks and still holds P1/P2
//
// The byte-exact guarantee is the tiling contract, not tree-sitter:
// tree-sitter buys boundary quality and one model across languages
// (ADR-0020), not correctness.
//
// Chunk IDs are deterministic content-derived handles (ADR-0021 §5), not
// the entity UUIDs of ARCHITECTURE §5: re-dividing an unchanged source
// yields the same IDs, so §17 indexing is idempotent.
package division

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Kind labels how a chunk's boundaries were derived.
type Kind string

const (
	// KindAST — boundaries from a real parse tree (tree-sitter).
	KindAST Kind = "ast"
	// KindStructural — boundaries from a hand-written structural scanner.
	// Reserved for a future language scanner; the §10.1 hybrid does not
	// emit it yet (present now so the DB CHECK set is closed).
	KindStructural Kind = "structural"
	// KindHeader — boundaries from a structural-header regex (markdown
	// headings, plain-text paragraphs).
	KindHeader Kind = "header"
	// KindFallback — fixed line blocks, used when a parser is unavailable
	// or rejects the input. Guarantees P5.
	KindFallback Kind = "fallback"
)

// DefaultFallbackLines is the block size of the universal fallback splitter
// (§10.1 P5). It is configurable via Options.FallbackLines.
const DefaultFallbackLines = 120

// Chunk is one dormant chunk (§10.1): a byte range of the source file plus
// the metadata the Dormant Index publishes (file path, line range, kind).
// Content is the exact slice of the source bytes; callers must not mutate
// it. Reassembly is byte-identical by construction (chunks tile the source).
type Chunk struct {
	ID        string
	FilePath  string
	Lang      string
	Kind      Kind
	ByteStart int
	ByteEnd   int
	LineStart int
	LineEnd   int
	Content   []byte
}

// Bytes returns the chunk's length in bytes.
func (c Chunk) Bytes() int { return c.ByteEnd - c.ByteStart }

// Options configures a Divider. The zero value is valid and uses the
// documented defaults.
type Options struct {
	// FallbackLines is the block size used when a source cannot be parsed
	// by its language strategy (P5). Zero selects DefaultFallbackLines.
	FallbackLines int
}

// Divider is the §10.1 division engine. It is safe for concurrent use:
// the grammar registry is built once and parsers are pooled.
type Divider struct {
	fallbackLines int
}

// New returns a Divider configured by opts.
func New(opts Options) *Divider {
	n := opts.FallbackLines
	if n <= 0 {
		n = DefaultFallbackLines
	}
	return &Divider{fallbackLines: n}
}

// Divide splits src into dormant chunks.
//
// lang selects the strategy; an empty lang is inferred from the file
// extension via LangFor. Code languages go through tree-sitter, markdown
// and plain text through the structural-header splitter, and everything
// else through the fixed-line fallback. A tree-sitter failure (no grammar,
// parse error) degrades to the fallback — P1/P2 always hold.
func (d *Divider) Divide(filePath, lang string, src []byte) []Chunk {
	if lang == "" {
		lang = LangFor(filePath)
	}
	switch lang {
	case "go", "python", "javascript":
		if chunks, ok := d.tsChunks(filePath, lang, src); ok {
			return chunks
		}
		return d.fallbackChunks(filePath, lang, src)
	case "markdown", "text":
		return d.headerChunks(filePath, lang, src)
	default:
		return d.fallbackChunks(filePath, lang, src)
	}
}

// Reassemble concatenates chunks in byte order. For chunks produced by
// Divide on the same source, the result is byte-identical to that source
// (P1). The input slice is not mutated.
func Reassemble(chunks []Chunk) []byte {
	ordered := make([]Chunk, len(chunks))
	copy(ordered, chunks)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ByteStart < ordered[j].ByteStart })
	var b bytes.Buffer
	for _, c := range ordered {
		b.Write(c.Content)
	}
	return b.Bytes()
}

// LangFor maps a filename to its division language; "" means unsupported
// (the caller's Divide will use the fallback splitter).
func LangFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".md", ".markdown":
		return "markdown"
	case ".txt":
		return "text"
	default:
		return ""
	}
}

// sourceHash is the SHA-256 of the whole source, the identity anchor for
// every chunk derived from it (ADR-0021 §5–6).
func sourceHash(src []byte) string {
	sum := sha256.Sum256(src)
	return hex.EncodeToString(sum[:])
}

// chunkID is the deterministic content-derived chunk handle (ADR-0021 §5):
// sha256(source_hash || start-end), truncated to 16 hex chars. Stable
// across re-division of unchanged content.
func chunkID(hash string, start, end int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d-%d", hash, start, end)))
	return hex.EncodeToString(sum[:])[:16]
}

// lineOf returns the 1-based line number containing byte offset off.
func lineOf(src []byte, off int) int {
	return 1 + bytes.Count(src[:off], []byte{'\n'})
}

// cutPoints normalizes raw boundary offsets into a sorted, de-duplicated
// list that always begins at 0 and ends at n. Interior offsets outside
// (0, n) are dropped. Consecutive pairs tile [0, n) exactly (P2).
func cutPoints(bounds []int, n int) []int {
	seen := map[int]bool{0: true, n: true}
	pts := []int{0}
	for _, b := range bounds {
		if b > 0 && b < n && !seen[b] {
			seen[b] = true
			pts = append(pts, b)
		}
	}
	pts = append(pts, n)
	sort.Ints(pts)
	return pts
}

// buildChunks turns raw boundary offsets into chunks that tile src. It is
// the single construction path for every strategy, which is what makes P1
// and P2 hold uniformly. An empty source yields exactly one empty chunk so
// callers never see a zero-chunk division.
func buildChunks(filePath, lang string, src []byte, kind Kind, bounds []int) []Chunk {
	n := len(src)
	hash := sourceHash(src)
	if n == 0 {
		return []Chunk{{
			ID: chunkID(hash, 0, 0), FilePath: filePath, Lang: lang, Kind: kind,
			ByteStart: 0, ByteEnd: 0, LineStart: 1, LineEnd: 1, Content: []byte{},
		}}
	}
	pts := cutPoints(bounds, n)
	out := make([]Chunk, 0, len(pts)-1)
	for i := 0; i+1 < len(pts); i++ {
		start, end := pts[i], pts[i+1]
		if end <= start {
			continue
		}
		out = append(out, Chunk{
			ID: chunkID(hash, start, end), FilePath: filePath, Lang: lang, Kind: kind,
			ByteStart: start, ByteEnd: end,
			LineStart: lineOf(src, start), LineEnd: lineOf(src, end-1),
			Content: src[start:end],
		})
	}
	if len(out) == 0 { // defensive: a non-empty source always tiles
		return []Chunk{{
			ID: chunkID(hash, 0, n), FilePath: filePath, Lang: lang, Kind: kind,
			ByteStart: 0, ByteEnd: n, LineStart: 1, LineEnd: lineOf(src, n-1), Content: src,
		}}
	}
	return out
}
