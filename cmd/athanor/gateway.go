package main

import (
	"fmt"
	"log/slog"

	"github.com/tcs76321/athanor/cmd/athanor/scanners"
	"github.com/tcs76321/athanor/internal/airlock/scanner"
	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/gateway"
	"github.com/tcs76321/athanor/internal/store"
)

// GatewayParts is the §21.5 wiring result: the outbound Client
// (fetch) and the Reader (extraction). T7's fetch_url / search_web
// tools consume both; until then the construction is the structural
// proof that the daemon can reach the internet *through the gateway*
// and extract *through the Reader*.
type GatewayParts struct {
	Client gateway.Client
	Reader gateway.Reader
	// NewClientWithCap returns a Client sharing this bundle's
	// Policy/Resolver/events but with the given response-size cap
	// (M4-T7, ADR-0019 §6): the truncated-re-fetch seam. Nil is
	// valid (the adapter then skips the re-fetch); startGateway
	// always sets it.
	NewClientWithCap func(capBytes int64) (gateway.Client, error)
}

// startGateway constructs the §21.5 Internet Gated Reader (M4-T5,
// ADR-0017; Reader Mode M4-T6, ADR-0018) and returns both the
// outbound Client and the extraction Reader. The function is the
// single wire-up point between the daemon's config and the gateway
// package: every config knob the gateway cares about
// (default_policy, allow_list, rate_limit_per_minute,
// max_response_bytes, reader_mode_default) is read here, and the
// resulting types are passed to `gateway.NewPolicy`,
// `gateway.NewClient`, and `gateway.NewReader`.
//
// The function does *not* start any goroutines; the gateway's
// runtime is a per-Fetch call, and T7 (gateway-backed tools) is the
// first caller. The Reader is constructed with the scanner set
// `readScanners` (the in-tree prompt-injection heuristic plus YARA /
// ClamAV when installed; see newReaderScanners), so extraction is
// fail-closed from day one and known-issues M3 is closed.
func startGateway(stateDir string, st *store.Store, netCfg config.Network, readScanners []scanner.Scanner, logger *slog.Logger) (GatewayParts, error) {
	// The §21.5 gateway is the only outbound HTTP
	// door. Construction is the operator's
	// structural proof that a daemon boot
	// exercises the package; failures here are
	// fatal so an operator who fat-fingers the
	// config sees the error at startup, not at
	// first fetch.
	policy, err := gateway.NewPolicy(netCfg.DefaultPolicy, netCfg.AllowList, nil)
	if err != nil {
		return GatewayParts{}, fmt.Errorf("gateway: building policy: %w", err)
	}
	client, err := gateway.NewClient(gateway.Options{
		Policy:           policy,
		Resolver:         &gateway.NetworkResolver{}, // production: net.DefaultResolver
		Events:           st,                         // *store.Store satisfies gateway.EventLogger
		Logger:           logger,
		RatePerMinute:    netCfg.RateLimitPerMinute,
		MaxResponseBytes: netCfg.MaxResponseBytes,
		// Timeout: zero → gateway default (30s).
		// RoundTripper: nil → http.DefaultTransport.
		// Clock: nil → time.Now.
	})
	if err != nil {
		return GatewayParts{}, fmt.Errorf("gateway: building client: %w", err)
	}
	reader, err := gateway.NewReader(gateway.ReaderOptions{
		Enabled:  netCfg.ReaderMode(),
		Scanners: readScanners,
		Events:   st,
		Logger:   logger,
	})
	if err != nil {
		return GatewayParts{}, fmt.Errorf("gateway: building reader: %w", err)
	}
	logger.Info("gateway: constructed",
		"default_policy", netCfg.DefaultPolicy,
		"allow_list_size", len(netCfg.AllowList),
		"rate_limit_per_minute", netCfg.RateLimitPerMinute,
		"max_response_bytes", netCfg.MaxResponseBytes,
		"reader_mode", netCfg.ReaderMode(),
	)
	return GatewayParts{
		Client: client,
		Reader: reader,
		NewClientWithCap: func(capBytes int64) (gateway.Client, error) {
			if capBytes <= 0 {
				capBytes = 1 // fail-closed floor; a zero/negative cap is a caller bug
			}
			opts := gateway.Options{
				Policy:           policy,
				Resolver:         &gateway.NetworkResolver{},
				Events:           st,
				Logger:           logger,
				RatePerMinute:    netCfg.RateLimitPerMinute,
				MaxResponseBytes: capBytes,
			}
			return gateway.NewClient(opts)
		},
	}, nil
}

// newReaderScanners builds the scanner set the Reader Mode pipeline runs over
// extracted web content (known-issues M3). The in-tree prompt-injection
// heuristic is always present; YARA (with the resolved in-tree rule set) and
// ClamAV join when their binaries are installed. Unlike the ingress registry —
// which wires an absent external scanner as fail-closed VerdictUncertain — an
// absent scanner here is simply omitted: otherwise every fetched page would be
// refused on a host without ClamAV/YARA, which would break legitimate research.
func newReaderScanners(stateDir string, ac ingressConfig, logger *slog.Logger) []scanner.Scanner {
	out := []scanner.Scanner{scanner.NewPromptInjectionHeuristic(1)}
	if rules, err := resolveYARARuleSet(stateDir, ac.YaraRuleSet); err != nil {
		logger.Warn("gateway: yara rule set unavailable; fetched content is scanned by the injection heuristic only", "err", err)
	} else if y := (&scanners.YARA{RuleSet: rules}); y.Available() {
		out = append(out, newDriverAdapter(y))
	}
	if c := (&scanners.ClamAV{}); c.Available() {
		out = append(out, newDriverAdapter(c))
	}
	logger.Info("gateway: reader scanners constructed", "count", len(out))
	return out
}
