package scanner

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
)

// ZipBomb is the in-tree `zipbomb` scanner. It enforces the
// §21.3 zip-bomb rejection criteria:
//
//   - Total uncompressed size > ratio × compressed size
//   - Entry count > MaxEntries
//   - Any single-entry ratio > 50 (decompression-bomb defense)
//
// The scanner is streaming: it opens the zip via
// `archive/zip.NewReader` (which reads from an io.ReaderAt)
// and walks the entries. The compressed size is the size of
// the input bytes; the uncompressed size is the sum of each
// entry's declared uncompressed size (the actual decoded
// length, summed across entries).
//
// # Configuration
//
// MaxRatio is the total-uncompressed ÷ total-compressed
// threshold (default 100). MaxEntries is the per-archive
// count limit (default 10,000). SingleEntryRatio is the
// per-entry ratio threshold (default 50); an entry with a
// higher ratio is rejected even if the total stays under
// MaxRatio, because the decompression is the attack surface,
// not the aggregate.
type ZipBomb struct {
	MaxRatio         int
	MaxEntries       int
	SingleEntryRatio int
}

// Name is "zipbomb" (the registry key).
func (z *ZipBomb) Name() string { return "zipbomb" }

// NewZipBomb constructs a ZipBomb scanner with the given
// limits. Non-positive values panic.
func NewZipBomb(maxRatio, maxEntries, singleEntryRatio int) *ZipBomb {
	if maxRatio < 1 {
		panic("scanner: ZipBomb requires MaxRatio >= 1")
	}
	if maxEntries < 1 {
		panic("scanner: ZipBomb requires MaxEntries >= 1")
	}
	if singleEntryRatio < 1 {
		panic("scanner: ZipBomb requires SingleEntryRatio >= 1")
	}
	return &ZipBomb{
		MaxRatio:         maxRatio,
		MaxEntries:       maxEntries,
		SingleEntryRatio: singleEntryRatio,
	}
}

// Scan classifies in as a zip and applies the §21.3 rules.
// Non-zip input is VerdictClean (the scanner only rejects
// things that look like zip bombs; it does not validate
// the file is a zip).
func (z *ZipBomb) Scan(ctx context.Context, in ScanInput) (ScanResult, error) {
	// Need an io.ReaderAt to open the zip. Prefer the
	// in-memory Bytes if present; otherwise, scan
	// defensively by reading a bounded prefix — the
	// ingress pipeline's size scanner caps the file
	// at MaxBytes (default 100 MiB), so the entire
	// file is in memory at this point. If Bytes is
	// nil and Reader is non-nil, the scanner skips
	// (not a zip-bomb threat at this size).
	var rdr io.ReaderAt
	var totalSize int64
	if len(in.Bytes) > 0 {
		rdr = bytes.NewReader(in.Bytes)
		totalSize = int64(len(in.Bytes))
	} else if in.Reader != nil {
		// Read the entire stream into memory. This
		// is a one-shot scanner; the registry does
		// not retain the bytes. For files over the
		// size cap, the size scanner has already
		// rejected; we never get here.
		buf, err := io.ReadAll(in.Reader)
		if err != nil {
			return ScanResult{}, fmt.Errorf("scanner:zipbomb:read: %w", err)
		}
		rdr = bytes.NewReader(buf)
		totalSize = int64(len(buf))
	} else {
		// No input data: not a zip; pass.
		return ScanResult{Verdict: VerdictClean}, nil
	}
	zr, err := zip.NewReader(rdr, totalSize)
	if err != nil {
		// Not a zip: not a zip-bomb threat. Pass.
		return ScanResult{
			Verdict: VerdictClean,
			Details: map[string]any{"zip_error": err.Error()},
		}, nil
	}
	if len(zr.File) > z.MaxEntries {
		return ScanResult{
			Verdict: VerdictRejected,
			Reason: fmt.Sprintf("scanner:zipbomb:too_many_entries:%d>%d",
				len(zr.File), z.MaxEntries),
			Details: map[string]any{
				"entries":     len(zr.File),
				"max_entries": z.MaxEntries,
			},
		}, nil
	}
	var totalUncompressed uint64
	perEntry := make([]map[string]any, 0, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		// zip-bomb: a single entry whose declared
		// uncompressed size is more than
		// singleEntryRatio× the compressed size is
		// a bomb. archive/zip does not actually
		// decompress here (we'd need to call Open()
		// to do that, which costs disk + memory);
		// the declared sizes are the standard
		// zip-bomb heuristic.
		c := f.CompressedSize64
		u := f.UncompressedSize64
		if c > 0 && u/c > uint64(z.SingleEntryRatio) {
			return ScanResult{
				Verdict: VerdictRejected,
				Reason: fmt.Sprintf("scanner:zipbomb:entry_ratio:%s:%d>%d",
					path.Base(f.Name), u/c, z.SingleEntryRatio),
				Details: map[string]any{
					"entry":              path.Base(f.Name),
					"compressed":         c,
					"uncompressed":       u,
					"entry_ratio":        u / c,
					"single_entry_limit": z.SingleEntryRatio,
				},
			}, nil
		}
		totalUncompressed += u
		perEntry = append(perEntry, map[string]any{
			"name":         path.Base(f.Name),
			"compressed":   c,
			"uncompressed": u,
		})
	}
	if totalSize > 0 && totalUncompressed/uint64(totalSize) > uint64(z.MaxRatio) {
		return ScanResult{
			Verdict: VerdictRejected,
			Reason: fmt.Sprintf("scanner:zipbomb:total_ratio:%d>%d",
				totalUncompressed/uint64(totalSize), z.MaxRatio),
			Details: map[string]any{
				"compressed":   totalSize,
				"uncompressed": totalUncompressed,
				"total_ratio":  totalUncompressed / uint64(totalSize),
				"max_ratio":    z.MaxRatio,
				"per_entry":    perEntry,
			},
		}, nil
	}
	return ScanResult{
		Verdict: VerdictClean,
		Details: map[string]any{
			"compressed":   totalSize,
			"uncompressed": totalUncompressed,
			"entries":      len(zr.File),
		},
	}, nil
}
