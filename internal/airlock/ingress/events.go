package ingress

// EventName is the closed set of `airlock` category event
// names the ingress pipeline writes. The names are stable
// strings so log queries, audit dashboards, and tests can
// match on them.
type EventName string

const (
	EventAccepted         EventName = "accepted"
	EventQuarantined      EventName = "quarantined"
	EventRejected         EventName = "rejected"          // path-layer rejection; file remains in inbox
	EventDuplicateIgnored EventName = "duplicate_ignored" // content already processed
)

// EventData is the structured payload of an `airlock` event.
// The fields are a superset of what every event actually
// carries; omitted fields are not marshalled. Audit consumers
// (the morning digest, the security view) read these directly
// from the events table.
type EventData struct {
	Event           EventName      `json:"event"`
	SHA256          string         `json:"sha256,omitempty"`
	RelPath         string         `json:"relpath"`
	Reason          string         `json:"reason,omitempty"`
	StoredPath      string         `json:"stored_path,omitempty"`
	SourceSize      int64          `json:"source_size,omitempty"`
	Pipeline        string         `json:"pipeline,omitempty"`
	Scanners        map[string]any `json:"scanners,omitempty"`
	OriginalVerdict string         `json:"original_verdict,omitempty"`
}
