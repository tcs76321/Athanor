package gateway

import "errors"

// Sentinel errors returned by the §21.5 gateway (M4-T5,
// ADR-0017). All package-level errors are `errors.Is` comparable
// so call sites can branch on class without string matching.
//
// The split mirrors the five reasons a request can be denied at
// the policy layer:
//
//   - ErrDeniedOffList:   the host is not in `allow_list`.
//   - ErrDeniedDenyList:  the host is in `deny_list` (explicit
//                         override; documented smell).
//   - ErrDeniedPrivateIP: the host resolved to a private,
//                         loopback, link-local, multicast, or
//                         otherwise non-routable IP (the §21.5
//                         DNS-rebinding / SSRF defense).
//   - ErrIDNNotSupported: the host contains non-ASCII characters
//                         (IDN). T5 ships ASCII-only; IDN is
//                         a T6+ concern that requires the
//                         `golang.org/x/net/idn` package — see
//                         ADR-0017 §3.
//   - ErrInvalidURL:      the URL is malformed, uses a scheme
//                         other than http/https, or has an empty
//                         host.
//
// Rate limiting (T5.2) and the response size cap (T5.2) get
// their own sentinels in `client.go` because the package
// shape puts the rate-limit decision after policy evaluation.

var ErrDeniedOffList = errors.New("gateway: host not in allow_list")

var ErrDeniedDenyList = errors.New("gateway: host in deny_list")

var ErrDeniedPrivateIP = errors.New("gateway: host resolved to non-routable IP")

var ErrIDNNotSupported = errors.New("gateway: non-ASCII host rejected (IDN not supported in M4-T5)")

var ErrInvalidURL = errors.New("gateway: invalid URL")

// ErrRateLimited is returned when the §21.5 per-host
// rate-limit bucket is empty. The caller can surface
// this to the operator as "the gateway's bucket for
// host X is exhausted; back off and retry." The
// `network` event with `event=rate_limited` is the
// durable record.
var ErrRateLimited = errors.New("gateway: per-host rate limit exceeded")

// Reader Mode sentinels (M4-T6, ADR-0018 §3). Each maps to exactly
// one `reader_mode_rejected` audit reason, so the pairing {error,
// audit reason} is one-to-one and greppable:
//
//   - ErrReaderDisabled:    reader_mode_default=false; the caller
//                           receives ErrReaderDisabled and may use
//                           the raw fetched Response instead.
//   - ErrNotReadable:       the response's Content-Type is not
//                           text/html / application/xhtml+xml /
//                           text/plain.
//   - ErrNoReadableContent: readability extraction produced no main
//                           content (e.g. a JS-rendered page). The
//                           raw HTML is never returned as a fallback.
//   - ErrPromptInjection:   the extracted markdown tripped the
//                           prompt-injection heuristic. No markdown
//                           leaves the reader.
var ErrReaderDisabled = errors.New("gateway: reader mode disabled by config")

var ErrNotReadable = errors.New("gateway: response content-type is not readable HTML/text")

var ErrNoReadableContent = errors.New("gateway: readability extraction found no main content")

var ErrPromptInjection = errors.New("gateway: extracted markdown tripped prompt-injection heuristic")
