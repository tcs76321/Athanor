package division

import "bytes"

// fallbackOffsets returns boundary offsets roughly every `lines` lines.
// This is the P5 safety net: any source a language strategy cannot parse
// is divided into fixed blocks that tile by construction, so no byte is
// ever lost (§10.1 "universal fallback").
func fallbackOffsets(src []byte, lines int) []int {
	if lines <= 0 {
		lines = DefaultFallbackLines
	}
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
		if (line-1)%lines == 0 && off < len(src) {
			bounds = append(bounds, off)
		}
	}
	return bounds
}

// fallbackChunks divides src into fixed line blocks (KindFallback).
func (d *Divider) fallbackChunks(filePath, lang string, src []byte) []Chunk {
	return d.bounded(filePath, lang, src, KindFallback, fallbackOffsets(src, d.fallbackLines))
}
