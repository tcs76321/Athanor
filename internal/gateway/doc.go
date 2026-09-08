// Package gateway is the §21.5 Internet Gated Reader (M4-T5,
// ADR-0017). It is the *only* door between the Core and the
// public internet, and the §21 containment story is "the agent
// reaches the internet through this package, nowhere else."
//
// # Architectural shape
//
// The package exposes a `Client` interface (`client.go`,
// T5.2) and a pure `Policy` type (`policy.go`, T5.1). The
// separation is deliberate:
//
//   - `Policy` is the configuration + decision function. Given
//     a URL, it returns an `EvalResult` saying whether the
//     request is allowed, denied, or requires a network
//     resolution to decide (the DNS-rebinding guard). It
//     performs no I/O of its own; resolution is injected via
//     a `Resolver` interface so tests are deterministic.
//
//   - `Client` (T5.2) wires `Policy` to a real `http.Client`
//     and owns the per-host rate-limit bucket, the response
//     size cap, the header sanitizer, and the `network` event
//     log append. Consumers of the gateway take a `Client`,
//     not the concrete type, so tests can substitute a fake
//     and so the T7 tool envelope, the M6 credential broker,
//     and any future consumer all share one seam.
//
// # Gate G1 rule 6
//
// The structural test in `internal/gate/gate_test.go` rule 6
// fails the build if any source file outside this package
// uses `http.Get`, `http.Post`, `http.NewRequestWithContext`,
// or `(*http.Client).Do`. The daemon's loopback server
// (`internal/server`) is unaffected — rule 6 is specifically
// about *outbound client* usage. Adding a new outbound HTTP
// call anywhere else in `internal/` is a build break by
// design.
//
// # What this package does NOT do
//
//   - §21.5 responsibility 7 (cloud-inference credential
//     injection) is M6.
//   - §21.6 Browser Mode is M6.
//   - Cross-pipeline correlation between network events and
//     artifacts is a future M5/M6 task.
//
// # Reader Mode (M4-T6, ADR-0018)
//
// `Client.Fetch` returns a raw `Response`; the `Reader` layer
// (`reader.go`) adds the §21.5 responsibility 6 pass on top:
// readability extraction → bluemonday sanitization → markdown
// rendering → prompt-injection scan. ADR-0018 records the pipeline
// decisions: depend on the maintained go-readability fork, only ever
// call `FromReader` (never `FromURL` — the gateway stays the single
// fetch point), and fail closed on non-HTML, unextractable, or
// injection-flagged content. The markdown renderer
// (`markdown.go`) must stay in this package so the §21.5
// containment story keeps a single egress surface.
package gateway
