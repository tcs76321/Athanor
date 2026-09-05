package main

import (
	"fmt"
	"log/slog"

	"github.com/tcs76321/athanor/internal/config"
	"github.com/tcs76321/athanor/internal/gateway"
	"github.com/tcs76321/athanor/internal/store"
)

// startGateway constructs the §21.5 Internet Gated
// Reader (M4-T5, ADR-0017) and returns the resulting
// Client. The function is the single wire-up point
// between the daemon's config and the gateway
// package: every config knob the gateway cares
// about (default_policy, allow_list,
// rate_limit_per_minute, max_response_bytes) is read
// here, and the resulting types are passed to
// `gateway.NewPolicy` and `gateway.NewClient`.
//
// The function does *not* start any goroutines; the
// gateway's runtime is a per-Fetch call, and T7
// (gateway-backed tools) is the first caller. The
// Client is returned so the caller holds a
// reference; the variable is currently unused after
// construction (T7 will use it) but the construction
// is the structural proof that the wire-up works.
func startGateway(stateDir string, st *store.Store, netCfg config.Network, logger *slog.Logger) (gateway.Client, error) {
	// The §21.5 gateway is the only outbound HTTP
	// door. Construction is the operator's
	// structural proof that a daemon boot
	// exercises the package; failures here are
	// fatal so an operator who fat-fingers the
	// config sees the error at startup, not at
	// first fetch.
	policy, err := gateway.NewPolicy(netCfg.DefaultPolicy, netCfg.AllowList, nil)
	if err != nil {
		return nil, fmt.Errorf("gateway: building policy: %w", err)
	}
	client, err := gateway.NewClient(gateway.Options{
		Policy:           policy,
		Resolver:         &gateway.NetworkResolver{}, // production: net.DefaultResolver
		Events:           st,                        // *store.Store satisfies gateway.EventLogger
		Logger:           logger,
		RatePerMinute:    netCfg.RateLimitPerMinute,
		MaxResponseBytes: netCfg.MaxResponseBytes,
		// Timeout: zero → gateway default (30s).
		// RoundTripper: nil → http.DefaultTransport.
		// Clock: nil → time.Now.
	})
	if err != nil {
		return nil, fmt.Errorf("gateway: building client: %w", err)
	}
	logger.Info("gateway: constructed",
		"default_policy", netCfg.DefaultPolicy,
		"allow_list_size", len(netCfg.AllowList),
		"rate_limit_per_minute", netCfg.RateLimitPerMinute,
		"max_response_bytes", netCfg.MaxResponseBytes,
	)
	return client, nil
}
