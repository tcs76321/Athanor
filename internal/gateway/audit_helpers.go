package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"time"

	"github.com/tcs76321/athanor/internal/store"
)

// readWithCap reads from `r` until EOF or until
// `maxBytes+1` bytes have been read. When the
// over-cap byte is observed, the function returns
// what it has read so far (≤ maxBytes) and sets
// `truncated = true`. The +1 sentinel is the standard
// pattern: you cannot know you are over the cap until
// you read one byte past it.
func readWithCap(r io.Reader, maxBytes int64) ([]byte, bool, error) {
	limited := io.LimitReader(r, maxBytes+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, err
	}
	if int64(len(buf)) > maxBytes {
		return buf[:maxBytes], true, nil
	}
	return buf, false, nil
}

// newRequestID generates a 16-character hex string
// for log correlation. The `crypto/rand` source is
// overkill for a log key but matches the project's
// pattern (`internal/internalapi` uses `crypto/rand`
// for the same reason — uniformity with the rest of
// the codebase).
func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// `crypto/rand` failure is unrecoverable
		// in normal operation; fall back to a
		// fixed marker so the audit row is still
		// identifiable.
		return "rand-failure"
	}
	return hex.EncodeToString(b[:])
}

// storeEventFromPolicy builds an audit event for a
// successful fetch (or a fetch the policy permitted
// but the transport / size cap interrupted). The
// `event` argument is an EventName constant.
func storeEventFromPolicy(requestID, event, rawURL string, policy EvalResult, duration time.Duration, status int, bytes int64, truncated bool) auditPayload {
	return auditPayload{
		Event:       event,
		URL:         rawURL,
		Host:        policy.Host,
		Decision:    policy.Reason,
		ResolvedIPs: ipsToStrings(policy.ResolvedIPs),
		Status:      status,
		BytesRead:   bytes,
		Truncated:   truncated,
		DurationMS:  duration.Milliseconds(),
		RequestID:   requestID,
	}
}

// rateLimitedEvent builds an audit event for a
// rate-limit refusal. The bucket's `hostPort` key is
// recorded in the `Host` field so the operator can
// see which host's bucket was empty. The `policy` is
// accepted for symmetry with the other event builders
// (a future revision may split "denied then
// rate-limited" from "allowed then rate-limited" using
// `policy.Decision`).
func rateLimitedEvent(requestID, rawURL string, policy EvalResult, hostPort string, duration time.Duration) auditPayload {
	_ = policy // reserved for future split
	return auditPayload{
		Event:      string(EventRateLimited),
		URL:        rawURL,
		Host:       hostPort,
		Decision:   "rate_limited",
		DurationMS: duration.Milliseconds(),
		RequestID:  requestID,
	}
}

// errorEvent builds an audit event for a transport
// failure.
func errorEvent(requestID, rawURL string, policy EvalResult, duration time.Duration, cause error) auditPayload {
	return auditPayload{
		Event:       string(EventError),
		URL:         rawURL,
		Host:        policy.Host,
		Decision:    policy.Reason,
		ResolvedIPs: ipsToStrings(policy.ResolvedIPs),
		Reason:      cause.Error(),
		DurationMS:  duration.Milliseconds(),
		RequestID:   requestID,
	}
}

// readerAppliedEvent builds the audit row for a successful Reader
// Mode extraction (M4-T6, ADR-0018 §6). The `mode` field records
// which extraction path produced the markdown (`readability` or
// `plain`); `markdownBytes` is the exact byte count handed to the
// caller.
func readerAppliedEvent(resp *Response, mode ReaderMode, markdownBytes int) auditPayload {
	// `Response.Decision` is a value `EvalResult` (client.go); the
	// default has empty Host/Reason, which is exactly the right audit
	// value when a caller constructs a Response without a decision.
	return auditPayload{
		Event:          string(EventReaderApplied),
		URL:            resp.URL,
		Host:           resp.Decision.Host,
		Decision:       resp.Decision.Reason,
		Status:         resp.StatusCode,
		BytesRead:      int64(len(resp.Body)),
		Truncated:      resp.Truncated,
		Mode:           string(mode),
		MarkdownBytes:  int64(markdownBytes),
		DurationMS:     0,
		RequestID:      newRequestID(),
	}
}

// readerRejectedEvent builds the audit row for a Reader Mode refusal.
// The `reason` is one of the stable strings in `reader.go`'s error
// mapping (not_readable, extraction_failed, no_readable_content,
// render_failed, md_parse_failed, prompt_injection, scan_error).
func readerRejectedEvent(resp *Response, reason string) auditPayload {
	return auditPayload{
		Event:      string(EventReaderRejected),
		URL:        resp.URL,
		Reason:     reason,
		DurationMS: 0,
		RequestID:  newRequestID(),
	}
}

// auditDenied writes a `network` event for a policy
// refusal. The EventName is mapped from the policy's
// Decision so the operator can grep on
// `event=private_ip` etc. without parsing the reason
// string.
func (c *httpClient) auditDenied(ctx context.Context, requestID, rawURL string, policy EvalResult, duration time.Duration) {
	eventName := string(EventDenied)
	if policy.Decision == DecisionDeniedPrivateIP {
		eventName = string(EventPrivateIP)
	}
	c.appendEvent(ctx, storeEventFromPolicy(requestID, eventName, rawURL, policy, duration, 0, 0, false))
}

// appendEvent is the gateway's seam into the audit
// log. It maps the event name to a `store.EventLevel`
// and delegates to the package-level `appendAudit`.
func (c *httpClient) appendEvent(ctx context.Context, payload auditPayload) {
	var level store.EventLevel
	switch payload.Event {
	case string(EventFetched), string(EventTruncated):
		level = store.EventInfo
	case string(EventDenied), string(EventPrivateIP), string(EventRateLimited):
		level = store.EventWarn
	case string(EventError):
		level = store.EventError
	default:
		level = store.EventInfo
	}
	appendAudit(ctx, c.logger, c.events, level, payload)
}

// ipsToStrings converts a slice of net.IP to a slice
// of dotted-quad / IPv6 string. Returns nil for an
// empty input so the JSON `omitempty` tag on
// `auditPayload.ResolvedIPs` suppresses the field.
func ipsToStrings(ips []net.IP) []string {
	if len(ips) == 0 {
		return nil
	}
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return out
}
