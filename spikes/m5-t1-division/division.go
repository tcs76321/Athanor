// Package division is the M5-T1 spike (ROADMAP M5-T1, ARCHITECTURE §10.1):
// it exercises three strategies for losslessly dividing files into dormant
// chunks and proves the byte-for-byte reassembly property on a corpus
// spanning ≥3 languages.
//
// The contract every strategy must satisfy (the "byte-partition property"):
//
//	P1  reassemble(divide(src)) == src          (byte-identical)
//	P2  chunk ranges tile [0, len(src)) exactly — no gaps, no overlap
//	P3  LineStart/LineEnd recomputed from byte offsets match the fields
//	P4  division is deterministic: same input → same chunks
//	P5  a parse failure degrades to the fallback splitter and STILL
//	    satisfies P1/P2 — no strategy may lose a single byte
//
// Chunk boundaries are byte offsets; content slices the source. Because
// chunks tile by offset (not by node extent), reassembly is concatenation
// in byte order — this is the property M5-T2's chunk store and M5-T3's
// context_swap inherit.
//
// This is throwaway spike code: it lives in spikes/, has its own go.mod,
// and is never imported by internal/.
package division

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// Kind labels how a chunk's boundaries were derived.
type Kind string

const (
	KindAST        Kind = "ast"        // boundaries from a real parse tree (tree-sitter, go/ast)
	KindStructural Kind = "structural" // boundaries from a hand-written structural scanner (indent/brace tracking)
	KindHeader     Kind = "header"     // boundaries from a naive structural-header regex
	KindFallback   Kind = "fallback"   // parser failed or language unsupported; fixed line blocks
)

// Chunk is one dormant chunk (§10.1): a byte range of the source file
// plus the metadata the Dormant Index will publish (file path, line
// range, type). Content aliases src; callers must not mutate it.
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

// Strategy divides a byte source into chunks satisfying P1–P5.
type Strategy interface {
	Name() string
	Divide(filePath, lang string, src []byte) []Chunk
}

func lineOf(src []byte, off int) int {
	return 1 + bytes.Count(src[:off], []byte("\n"))
}

func chunkID(filePath string, start, end int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d-%d", filePath, start, end)))
	return hex.EncodeToString(sum[:])[:12]
}

// cleanedBoundaries normalizes raw boundary offsets: deduped, sorted,
// restricted to (0, len), with 0 always first.
func cleanedBoundaries(bounds []int, n int) []int {
	seen := map[int]bool{0: true}
	clean := []int{0}
	for _, b := range bounds {
		if b > 0 && b < n && !seen[b] {
			seen[b] = true
			clean = append(clean, b)
		}
	}
	sort.Ints(clean)
	return clean
}

// buildChunks tiles src into chunks at the given boundary offsets. This
// is the only place chunks are constructed, so P1/P2 hold by construction
// for every strategy; the tests exist to catch offset bugs in strategies.
func buildChunks(filePath, lang string, src []byte, kind Kind, boundaries []int) []Chunk {
	bs := cleanedBoundaries(boundaries, len(src))
	chunks := make([]Chunk, 0, len(bs))
	for i, start := range bs {
		end := len(src)
		if i+1 < len(bs) {
			end = bs[i+1]
		}
		lineEnd := lineOf(src, start)
		if end > start {
			lineEnd = lineOf(src, end-1)
		}
		chunks = append(chunks, Chunk{
			ID:        chunkID(filePath, start, end),
			FilePath:  filePath,
			Lang:      lang,
			Kind:      kind,
			ByteStart: start,
			ByteEnd:   end,
			LineStart: lineOf(src, start),
			LineEnd:   lineEnd,
			Content:   src[start:end],
		})
	}
	return chunks
}

// Reassemble concatenates chunk contents in byte order. For a division
// satisfying P1/P2 this returns exactly the original bytes.
func Reassemble(chunks []Chunk) []byte {
	sorted := make([]Chunk, len(chunks))
	copy(sorted, chunks)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ByteStart < sorted[j].ByteStart })
	n := 0
	for _, c := range sorted {
		n += len(c.Content)
	}
	out := make([]byte, 0, n)
	for _, c := range sorted {
		out = append(out, c.Content...)
	}
	return out
}
