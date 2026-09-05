package gateway

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/tcs76321/athanor/internal/store"
)

// EventName is the closed set of `network` event names the
// §21.5 gateway writes (ADR-0017 §8). The names are stable
// strings so log queries and audit dashboards can match on
// them. The closed set today:
//
//   - EventFetched: a successful fetch (status 2xx, body
//     received — possibly truncated).
//   - EventTruncated: a successful fetch whose body was
//     truncated by the response size cap.
//   - EventDenied: a request the policy layer refused
//     (off-list, denylist, IDN, invalid URL).
//   - EventPrivateIP: a request the DNS-rebinding guard
//     refused (resolved to a non-routable IP).
//   - EventRateLimited: a request the per-host rate
//     limiter refused.
//   - EventError: a transport-layer failure (DNS error,
//     TCP error, TLS handshake error, timeout).
type EventName string

const (
	EventFetched     EventName = "fetched"
	EventTruncated   EventName = "truncated"
	EventDenied      EventName = "denied"
	EventPrivateIP   EventName = "private_ip"
	EventRateLimited EventName = "rate_limited"
	EventError       EventName = "error"
)

// EventLogger is the surface the audit helpers need.
// *store.Store satisfies it. Kept as an interface so
// tests can substitute a fake that records the events
// without a real database. The shape matches the
// `internalapi.EventLogger` interface (M2-T4) for
// consistency, even though `network` is a different
// category.
type EventLogger interface {
	AppendEvent(ctx context.Context, e store.Event) (int64, error)
}

// auditPayload is the on-the-wire shape of the `data`
// field for a `network` event. The top-level event row
// also carries a synthetic `event` key so a SQL filter
// on `events.data_json` can find rows without parsing
// JSON (the same pattern as the airlock audit payloads).
type auditPayload struct {
	Event            string   `json:"event"`
	URL              string   `json:"url"`
	Host             string   `json:"host"`
	Decision         string   `json:"decision"`
	ResolvedIPs      []string `json:"resolved_ips,omitempty"`
	SanitizedHeaders []string `json:"sanitized_headers,omitempty"`
	Status           int      `json:"status,omitempty"`
	BytesRead        int64    `json:"bytes_read,omitempty"`
	Truncated        bool     `json:"truncated,omitempty"`
	Reason           string   `json:"reason,omitempty"`
	DurationMS       int64    `json:"duration_ms"`
	RequestID        string   `json:"request_id"`
}

// appendAudit writes one `network` event. Errors are
// logged via slog but not fatal: the gateway's
// user-visible behavior (success / denial) is the
// operator-facing record; the audit row is the durable
// post-mortem. The pattern mirrors the airlock audit
// helpers (`internal/airlock/egress/audit.go`,
// `internal/airlock/ingress/audit.go`).
func appendAudit(ctx context.Context, logger *slog.Logger, events EventLogger, level store.EventLevel, payload auditPayload) {
	if events == nil {
		// A nil events is the "tests do not assert
		// on events" path. Nothing to do.
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		logger.Warn("gateway: audit marshal failed", "event", payload.Event, "err", err)
		return
	}
	if _, err := events.AppendEvent(ctx, store.Event{
		Category: "network",
		Level:    level,
		Data:     map[string]any{"event": payload.Event, "data": json.RawMessage(raw)},
	}); err != nil {
		logger.Warn("gateway: audit append failed", "event", payload.Event, "err", err)
	}
}
