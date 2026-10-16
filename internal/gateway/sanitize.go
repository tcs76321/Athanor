package gateway

import (
	"net/http"
	"net/textproto"
	"strings"
)

// allowedRequestHeaders is the §21.5 fixed ALLOW-list of header names
// (known-issues L12 replaced the earlier 6-name deny-list). An allow-list
// cannot be defeated by a credential header the author did not think of: any
// name not listed here is stripped before the request leaves the gateway.
// Values are not inspected, so a listed name carrying a secret still passes —
// callers must not put credentials in them.
//
// The set is closed and small on purpose. Adding an entry is a deliberate
// policy change: each entry's blast radius is "every outbound request the
// gateway makes."
//
// Keys are stored in canonical MIME form
// (`textproto.CanonicalMIMEHeaderKey`) so the `http.Header` lookup, which
// canonicalizes on Set/Get, is direct.
var allowedRequestHeaders = map[string]bool{
	"User-Agent":        true,
	"Accept":            true,
	"Accept-Language":   true,
	"Accept-Encoding":   true,
	"If-None-Match":     true,
	"If-Modified-Since": true,
}

// gatewayUserAgent is the fixed User-Agent the gateway sets
// on every outbound request (ADR-0017 §6). The string is
// stable so allowlisted domains' operators can recognize
// Athanor in their access logs.
const gatewayUserAgent = "Athanor/0.1 (+local agent)"

// sanitizeRequestHeaders strips every header not in
// allowedRequestHeaders from `h` and returns the list of names
// that were removed. The returned slice is for the `network`
// event's `sanitized_headers` field; the caller logs it.
//
// The function mutates `h` in place and returns the removed
// names sorted for stable log output.
func sanitizeRequestHeaders(h http.Header) []string {
	var removed, toDelete []string
	for name := range h {
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if !allowedRequestHeaders[canonical] {
			removed = append(removed, canonical)
			toDelete = append(toDelete, name)
		}
	}
	// Stable order for log output.
	if len(removed) > 0 {
		sortStrings(removed)
		for _, name := range toDelete {
			delete(h, name) // delete by the exact map key
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
