package division

import "bytes"

// fallbackLines is the block size of the universal fallback splitter.
// It exists so that P5 (parse failure never loses bytes) holds for every
// strategy on every input: any strategy that cannot parse falls back to
// fixed line blocks, which tile by construction.
const fallbackLines = 120

// fallbackOffsets returns boundary offsets every ~fallbackLines lines.
func fallbackOffsets(src []byte) []int {
	var bounds []int
	off := 0
	line := 1
	for off < len(src) {
		nl := bytes.IndexByte(src[off:], '\n')
		if nl < 0 {
			break
		}
		off += nl + 1
		line++
		if (line-1)%fallbackLines == 0 {
			bounds = append(bounds, off)
		}
	}
	return bounds
}

// fallbackChunks is the last-resort division used when a strategy cannot
// parse the input (syntax error, unsupported language). Kind is reported
// as KindFallback so the Dormant Index can flag machine-derived chunks.
func fallbackChunks(filePath, lang string, src []byte) []Chunk {
	return buildChunks(filePath, lang, src, KindFallback, fallbackOffsets(src))
}
