package scanner

import (
	"context"
	"fmt"
)

// Size is the in-tree `size` scanner. It enforces a hard
// upper bound on file size (the §21.3 ingress threshold;
// the value comes from `airlock.max_ingress_bytes` in
// config, default 100 MiB). Files at or under the limit
// pass; files over the limit are rejected.
//
// The scanner does not read the file body — it inspects
// `ScanInput.Size` only. This makes it cheap (O(1) per
// call) and lets the caller stream large files without
// loading them into memory just to size-check.
//
// # Configuration
//
// The threshold is fixed at construction time. A future
// commit can make it dynamic (registry-side
// configuration) without changing this file's contract.
type Size struct {
	MaxBytes int64
}

// Name is "size" (the registry key).
func (s *Size) Name() string { return "size" }

// NewSize constructs a Size scanner with the given limit.
// A non-positive limit panics: a "no limit" size scanner
// is the absence of the scanner, not a Size with MaxBytes=0.
func NewSize(maxBytes int64) *Size {
	if maxBytes <= 0 {
		panic("scanner: Size requires positive MaxBytes (omit the scanner for no limit)")
	}
	return &Size{MaxBytes: maxBytes}
}

// Scan reports VerdictRejected if in.Size > MaxBytes,
// VerdictClean otherwise. The Reason is a stable
// machine-readable string suitable for the audit row.
func (s *Size) Scan(ctx context.Context, in ScanInput) (ScanResult, error) {
	if in.Size > s.MaxBytes {
		return ScanResult{
			Verdict: VerdictRejected,
			Reason:  fmt.Sprintf("scanner:size:exceeds_max:%d>%d", in.Size, s.MaxBytes),
			Details: map[string]any{
				"size": in.Size,
				"max":  s.MaxBytes,
			},
		}, nil
	}
	return ScanResult{Verdict: VerdictClean}, nil
}
