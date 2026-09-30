// M4-T2 + M4-T3: wire the §21.3 ingress pipeline into the
// daemon boot sequence. The watcher is constructed after
// the store is migrated and before the HTTP server starts;
// it runs in the background until the daemon shuts down.
//
// The scanner set is the ADR-0015 default:
//
//   - ingress: prompt-injection-heuristic, size, zipbomb,
//     clamav, yara (the heuristic + the file-format checks
//   - the external malware scanners).
//   - egress:  size, zipbomb, clamav, yara (no prompt-
//     injection scanner; LLM-generated data).
//   - user_prompt: prompt-injection-heuristic (long prompts
//     only; the size is enforced in the API handler).
//
// The external-binary scanners (ClamAV, YARA) live in
// cmd/athanor/scanners and shell out to subprocesses. The
// adapters degrade to VerdictUncertain on absent binary
// (fail-closed; ADR-0015 §"Trust boundaries").
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tcs76321/athanor/cmd/athanor/scanners"
	"github.com/tcs76321/athanor/internal/airlock/ingress"
	"github.com/tcs76321/athanor/internal/airlock/scanner"
	"github.com/tcs76321/athanor/internal/control"
	"github.com/tcs76321/athanor/internal/store"
)

// resolveYARARuleSet returns the path to a YARA rule
// file. If the operator has configured one, that path
// is used. Otherwise the in-tree baseline ruleset
// (embedded in the binary via go:embed) is materialized
// to <state-dir>/yara/injection.yar and that path is
// returned. Materializing to disk is required because
// the yara binary reads rules from a file, not from
// stdin. The materialized file is owned by the daemon
// and overwritten on every boot.
func resolveYARARuleSet(stateDir, configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	dir := filepath.Join(stateDir, "yara")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("airlock: create yara rule dir: %w", err)
	}
	dst := filepath.Join(dir, "injection.yar")
	if err := os.WriteFile(dst, []byte(scanner.DefaultYARARules()), 0o600); err != nil {
		return "", fmt.Errorf("airlock: write yara rules: %w", err)
	}
	return dst, nil
}

// startIngress constructs and starts the ingress Watcher.
// Returns the Watcher (whose Close must be called at
// shutdown) and the constructed registry.
//
// The inbox directory is `<state-dir>/workspace/inbox`.
// The quarantine directory is `<state-dir>/workspace/quarantine`,
// the sibling of `inbox` (the layout matches §4 of
// ARCHITECTURE.md).
func startIngress(
	ctx context.Context,
	stateDir string,
	store_ *store.Store,
	killSwitch *control.KillSwitch,
	cfg ingressConfig,
	logger *slog.Logger,
) (*ingress.Watcher, *scanner.Registry, error) {
	if !cfg.AirlockEnabled {
		logger.Info("airlock disabled by config; ingress not started")
		return nil, nil, nil
	}
	root := filepath.Join(stateDir, "workspace", "inbox")
	// Resolve the YARA rule-set path. Operators can
	// override via config; default is the in-tree
	// baseline ruleset, materialized to
	// <state-dir>/yara/injection.yar.
	yaraRuleSet, err := resolveYARARuleSet(stateDir, cfg.YaraRuleSet)
	if err != nil {
		return nil, nil, fmt.Errorf("airlock: yara rule set: %w", err)
	}
	cfg.YaraRuleSet = yaraRuleSet
	// In-tree scanners (no subprocess, no external dep).
	inTree := map[string]scanner.Scanner{
		"size":                       scanner.NewSize(cfg.MaxIngressBytes),
		"zipbomb":                    scanner.NewZipBomb(cfg.MaxUncompressedRatio, cfg.MaxZipEntries, 50),
		"prompt-injection-heuristic": scanner.NewPromptInjectionHeuristic(1),
	}
	// External-binary adapters. Absent binaries degrade
	// to VerdictUncertain (fail-closed); the airlock
	// registry is unaware of the absence, so a future
	// operator who removes clamd at runtime gets
	// graceful degradation without code changes.
	clamav := &scanners.ClamAV{}
	yara := &scanners.YARA{RuleSet: cfg.YaraRuleSet}
	if !clamav.Available() {
		logger.Warn("clamav binary not on PATH; clamav scanner will quarantine every file (fail-closed)")
	}
	if !yara.Available() {
		logger.Warn("yara binary or rule set unavailable; yara scanner will quarantine every file (fail-closed)")
	}
	// Bridge: each Driver wraps a thin scanner
	// implementation that returns VerdictUncertain on
	// absent binary. The bridge keeps the Scanner
	// interface clean (the registry never has to know
	// about subprocess lifecycle).
	allScanners := map[string]scanner.Scanner{
		"size":                       inTree["size"],
		"zipbomb":                    inTree["zipbomb"],
		"prompt-injection-heuristic": inTree["prompt-injection-heuristic"],
		"clamav":                     newDriverAdapter(clamav),
		"yara":                       newDriverAdapter(yara),
	}
	// Per-pipeline selection (ADR-0015 §"Trust boundaries,
	// not all text"). Egress must never carry a
	// prompt-injection scanner; user_prompt runs the
	// heuristic only.
	ingressList := []string{"prompt-injection-heuristic", "size", "zipbomb", "clamav", "yara"}
	egressList := []string{"size", "zipbomb", "clamav", "yara"}
	userPromptList := []string{"prompt-injection-heuristic"}
	reg, err := scanner.NewRegistry(allScanners, ingressList, egressList, userPromptList)
	if err != nil {
		return nil, nil, fmt.Errorf("building airlock scanner registry: %w", err)
	}
	qr := store.NewQuarantineRepo(store_)
	w, err := ingress.New(ctx, ingress.Options{
		Root:       root,
		Registry:   reg,
		Store:      store_,
		Quarantine: qr,
		Freezer:    killSwitch,
		Logger:     logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("starting ingress watcher: %w", err)
	}
	logger.Info("airlock ingress started", "inbox", root)
	return w, reg, nil
}

// driverAdapter bridges a scanners.Driver (which knows
// how to shell out to a binary) to the scanner.Scanner
// interface (which the registry consumes). The adapter
// is intentionally tiny: Available() decides whether the
// bridge returns VerdictUncertain immediately or invokes
// the driver's Scan. The fail-closed posture lives in
// this bridge: an absent binary is a VerdictUncertain
// (quarantine), not a VerdictClean (silent pass).
type driverAdapter struct {
	driver scanners.Driver
}

func newDriverAdapter(d scanners.Driver) *driverAdapter {
	return &driverAdapter{driver: d}
}

func (a *driverAdapter) Name() string { return a.driver.Name() }

func (a *driverAdapter) Scan(ctx context.Context, in scanner.ScanInput) (scanner.ScanResult, error) {
	if !a.driver.Available() {
		return scanner.ScanResult{
			Verdict: scanner.VerdictUncertain,
			Reason:  "scanner:" + a.driver.Name() + ":absent",
		}, nil
	}
	return a.driver.Scan(ctx, in)
}

// ingressConfig is the subset of the Airlock config the
// cmd layer needs. Kept here as a local struct so the cmd
// package doesn't have to import `config` types in tests
// that don't need them. The daemon's `config.Airlock`
// field maps 1:1 to this struct.
type ingressConfig struct {
	AirlockEnabled       bool
	MaxIngressBytes      int64
	MaxUncompressedRatio int
	MaxZipEntries        int
	YaraRuleSet          string
}
