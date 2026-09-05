package gateway

import (
	"net/http"
	"net/textproto"
	"strings"
)

// sanitizedRequestHeaders is the §21.5 fixed deny-list of
// header names (ADR-0017 §6). The list is on names only;
// header values are not inspected. A header named
// `X-Safe-Custom` carrying `Bearer <secret>` passes through;
// a header named `Cookie` is stripped regardless of value.
//
// The set is closed and small on purpose. Adding a new
// entry is a deliberate policy change: each entry's blast
// radius is "every outbound request the gateway makes,"
// and the cost of a typo is a silent credential leak.
//
// Keys are stored in canonical MIME form
// (`textproto.CanonicalMIMEHeaderKey`) so the
// `http.Header` lookup, which canonicalizes on Set/Get, is
// direct. A header added via `h[name] = ...` with a
// non-canonical key is matched in `sanitizeRequestHeaders`
// via the same canonicalization; see the comment there.
var sanitizedRequestHeaders = map[string]bool{
	"Cookie":              true,
	"Authorization":       true,
	"Proxy-Authorization": true,
	"X-Api-Key":           true,
	"X-Auth-Token":        true,
	"X-Secret":            true,
}

// gatewayUserAgent is the fixed User-Agent the gateway sets
// on every outbound request (ADR-0017 §6). The string is
// stable so allowlisted domains' operators can recognize
// Athanor in their access logs.
const gatewayUserAgent = "Athanor/0.1 (+local agent)"

// sanitizeRequestHeaders strips the deny-listed headers
// from `h` and returns the list of names that were removed.
// The returned slice is for the `network` event's
// `sanitized_headers` field; the caller logs it.
//
// The function mutates `h` in place and returns the
// removed names sorted for stable log output. The
// comparison is on the canonical MIME form so a header
// added with any case (`"cookie"`, `"Cookie"`,
// `"COOKIE"`) is matched uniformly.
func sanitizeRequestHeaders(h http.Header) []string {
	var removed []string
	for name := range h {
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if sanitizedRequestHeaders[canonical] {
			removed = append(removed, canonical)
		}
	}
	// Stable order for log output.
	if len(removed) > 0 {
		sortStrings(removed)
		for _, name := range removed {
			h.Del(name)
		}
	}
	return removed
}

// sortStrings is a tiny inlinable sort for short slices
// (the deny-list is 6 entries, so the typical
// `sanitized_headers` audit field is also short). Importing
// `sort` is heavier than 6 lines of insertion sort for the
// expected N; the function is a deliberate micro-optimization
// the project can revisit if the deny-list grows.
func sortStrings(s []string) {
	// Insertion sort. Stable, in-place, ~6 lines.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && strings.Compare(s[j-1], s[j]) > 0; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

